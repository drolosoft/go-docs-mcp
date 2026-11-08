package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/drolosoft/go-docs-mcp/internal/pdf"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// maxURLFileSize caps a read_url download at 50MB, enough for a long
// report and small enough that one call cannot fill the disk.
const maxURLFileSize = 50 * 1024 * 1024

// urlDownloadTimeout bounds a whole read_url download, headers and body
// together: a 50MB file fits in it on a slow line, and a server that stalls
// cannot hold the tool call open for ever.
const urlDownloadTimeout = 60 * time.Second

// urlDialTimeout bounds the TCP connect alone, so an unroutable host fails
// fast instead of using up the whole download timeout.
const urlDialTimeout = 10 * time.Second

// maxURLRedirects keeps net/http's default limit, which a custom
// CheckRedirect replaces.
const maxURLRedirects = 10

// errPrivateAddress marks a read_url target refused because it points inside
// the machine or its network (the classic SSRF to a cloud metadata endpoint).
var errPrivateAddress = errors.New("refusing to fetch a private, loopback or link-local address")

// nonPublicPrefixes are ranges netip has no predicate for that must not be
// fetched either: 0.0.0.0/8 reaches the local host on Linux, and
// 100.64.0.0/10 is carrier-grade NAT space, which Tailscale uses for the
// tailnet.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
}

func main() {
	// Determine documents directory: DOCS_MCP_DIR (primary), PDF_MCP_DIR (backward compat), DROLO_DOCS_DIR (legacy)
	docsDir := os.Getenv("DOCS_MCP_DIR")
	if docsDir == "" {
		docsDir = os.Getenv("PDF_MCP_DIR") // backward compat
	}
	if docsDir == "" {
		docsDir = os.Getenv("DROLO_DOCS_DIR") // legacy
	}
	if docsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("cannot determine home directory: %v", err)
		}
		docsDir = filepath.Join(home, ".docs-mcp", "documents")
	}

	// Expand ~ if present
	if strings.HasPrefix(docsDir, "~/") {
		home, _ := os.UserHomeDir()
		docsDir = filepath.Join(home, docsDir[2:])
	}

	// Ensure documents directory exists
	if _, err := os.Stat(docsDir); os.IsNotExist(err) {
		log.Printf("WARNING: documents directory does not exist: %s", docsDir)
		if err := os.MkdirAll(docsDir, 0755); err != nil {
			log.Fatalf("cannot create documents directory: %v", err)
		}
		log.Printf("Created documents directory: %s", docsDir)
	}

	// Initialize PDF reader
	reader := pdf.NewReader(docsDir)

	// Validate dependencies
	if err := reader.CheckDependencies(); err != nil {
		log.Printf("WARNING: %v", err)
	}

	// Check OCR dependencies (optional, warn only)
	for _, warning := range reader.CheckOCRDependencies() {
		log.Printf("WARNING: %s", warning)
	}
	if reader.HasOCR() {
		log.Printf("OCR support enabled (tesseract + pdftoppm available)")
	} else {
		log.Printf("OCR support disabled (install tesseract and poppler for OCR)")
	}

	s := newServer(reader, docsDir)

	// Run stdio transport
	stdio := server.NewStdioServer(s)
	if err := stdio.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// newServer builds the MCP server with every tool registered over reader.
// main serves it on stdio; tests call it directly to inspect the tools.
// docsDir is the documents directory the reader was created with, and extra
// options are appended to the server's own, which lets a test switch on
// checks such as output schema validation.
func newServer(reader *pdf.Reader, docsDir string, extra ...server.ServerOption) *server.MCPServer {
	options := []server.ServerOption{
		server.WithToolCapabilities(true),
		server.WithToolHandlerMiddleware(redactPathsMiddleware(docsDir, os.TempDir())),
	}
	options = append(options, extra...)

	s := server.NewMCPServer("go-docs-mcp", serverVersion(), options...)

	// Register tools (13 total, 12 when read_url is disabled)
	registerListDocuments(s, reader)
	registerReadDocument(s, reader)
	registerSearchDocument(s, reader)
	registerGetDocumentSummary(s, reader)
	registerGetDocumentMetadata(s, reader)
	registerExtractImages(s, reader)
	if urlToolEnabled() {
		registerReadURL(s, reader, newURLFetcher(urlDownloadTimeout))
	}
	registerOCRDocument(s, reader)
	registerListFormats(s, reader)
	registerReadImage(s, reader)
	registerGetDocumentOutline(s, reader)
	registerExtractTables(s, reader)
	registerConvertToMarkdown(s, reader)

	return s
}

// readOnlyHints returns the tool annotations shared by every tool here: it
// changes nothing, destroys nothing and answers the same for the same input.
// mcp.NewTool advertises the opposite by default (readOnlyHint false,
// destructiveHint true), which makes clients ask for confirmation on each
// call. openWorld is true only for a tool that reaches the network.
func readOnlyHints(openWorld bool) mcp.ToolOption {
	return func(tool *mcp.Tool) {
		mcp.WithReadOnlyHintAnnotation(true)(tool)
		mcp.WithDestructiveHintAnnotation(false)(tool)
		mcp.WithIdempotentHintAnnotation(true)(tool)
		mcp.WithOpenWorldHintAnnotation(openWorld)(tool)
	}
}

// redactPathsMiddleware rewrites the text of failed tool results so a path
// under one of roots reaches the client as its file name only. The reader's
// errors wrap os errors that carry full paths (the documents directory, the
// temporary files of read_url and pdfimages), and those reveal the user's
// home and layout to the model and whatever it talks to.
func redactPathsMiddleware(roots ...string) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result, err := next(ctx, request)
			if result == nil || !result.IsError {
				return result, err
			}

			for index, content := range result.Content {
				if text, ok := content.(mcp.TextContent); ok {
					text.Text = redactPaths(text.Text, roots...)
					result.Content[index] = text
				}
			}

			return result, err
		}
	}
}

// pathTerminators are the characters that end a path inside an error
// message: Go's os errors put a colon after it, net/http quotes it, and a
// space or a comma ends a sentence.
const pathTerminators = " \t\n:\"',;)"

// redactPaths replaces every path in message that starts with one of roots by
// its last element: "/home/ana/docs/report.pdf" becomes "report.pdf" and the
// root itself becomes "docs". A match must end at a path boundary, so a root
// "/tmp/a" leaves "/tmp/ab" alone.
func redactPaths(message string, roots ...string) string {
	for _, root := range roots {
		root = strings.TrimRight(root, string(filepath.Separator))
		if root == "" {
			continue
		}

		var redacted strings.Builder
		rest := message

		for {
			index := strings.Index(rest, root)
			if index < 0 {
				redacted.WriteString(rest)
				break
			}

			end := index + len(root)
			if end < len(rest) && rest[end] != filepath.Separator && !strings.ContainsRune(pathTerminators, rune(rest[end])) {
				redacted.WriteString(rest[:end])
				rest = rest[end:]
				continue
			}

			for end < len(rest) && !strings.ContainsRune(pathTerminators, rune(rest[end])) {
				end++
			}

			redacted.WriteString(rest[:index])
			redacted.WriteString(filepath.Base(rest[index:end]))
			rest = rest[end:]
		}

		message = redacted.String()
	}

	return message
}

// documentList is the structured answer of list_documents. MCP wants
// structuredContent to be a JSON object, so the list the text block prints
// bare travels here under a name.
type documentList struct {
	Documents []pdf.DocumentInfo `json:"documents"`
}

// formatList is the structured answer of list_formats, the same object its
// text block has always printed.
type formatList struct {
	Formats []pdf.FormatInfo `json:"formats"`
}

// documentOutline is the structured answer of get_document_outline.
type documentOutline struct {
	Outline []pdf.OutlineEntry `json:"outline"`
}

// documentTables is the structured answer of extract_tables.
type documentTables struct {
	Tables []pdf.TableData `json:"tables"`
}

// structuredResult answers a tool call with structured as structuredContent
// and printed as the text block. printed is the JSON the tool returned before
// it had an output schema, so clients that only read text see no change.
func structuredResult(structured, printed any) *mcp.CallToolResult {
	data, err := json.MarshalIndent(printed, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error marshaling results: %v", err))
	}

	return mcp.NewToolResultStructured(structured, string(data))
}

// nonNil returns items, or an empty slice when items is nil, so a
// structured list reaches the client as [] and not as null.
func nonNil[Item any](items []Item) []Item {
	if items == nil {
		return []Item{}
	}

	return items
}

// registerListDocuments adds list_documents, which lists the readable files
// of the documents directory.
func registerListDocuments(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("list_documents",
		mcp.WithDescription("List all documents in the configured directory with format detection and metadata (filename, pages, size). Use this to discover available documents before reading or searching. Read-only."),
		mcp.WithTitleAnnotation("List Documents"),
		readOnlyHints(false),
		mcp.WithOutputSchema[documentList](),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		docs, err := reader.ListDocuments(ctx)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error listing documents: %v", err)), nil
		}

		structured := documentList{Documents: nonNil(docs)}

		return structuredResult(structured, docs), nil
	})
}

func registerReadDocument(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("read_document",
		mcp.WithDescription("Read text content from a document with optional page selection. Use this when you need the raw text of a PDF, TXT, MD, CSV, or DOCX file; TXT/MD/CSV are returned verbatim, DOCX via pandoc; PDFs support page ranges (e.g. \"1-5\", \"1-3,7,10-12\") and auto-OCR fallback for scanned pages. Read-only."),
		mcp.WithTitleAnnotation("Read Document"),
		readOnlyHints(false),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The document filename to read"),
		),
		mcp.WithNumber("page",
			mcp.Description("Optional single page number to read (1-based, PDF only). If omitted, returns full text."),
		),
		mcp.WithString("pages",
			mcp.Description("Optional page ranges to read (PDF only), e.g. \"1-5\", \"10\", \"1-3,7,10-12\". Overrides 'page' if both provided."),
		),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filename, err := request.RequireString("filename")
		if err != nil {
			return mcp.NewToolResultError("filename parameter is required"), nil
		}

		// Check for pages range first (takes priority over single page)
		pagesStr := request.GetString("pages", "")
		if pagesStr != "" {
			text, err := reader.ReadDocumentPages(ctx, filename, pagesStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Error reading document pages: %v", err)), nil
			}
			return mcp.NewToolResultText(text), nil
		}

		page := request.GetInt("page", 0)
		text, err := reader.ReadDocument(ctx, filename, page)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error reading document: %v", err)), nil
		}

		return mcp.NewToolResultText(text), nil
	})
}

func registerSearchDocument(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("search_document",
		mcp.WithDescription("Search for text within a document and return matching lines with context and approximate page numbers. Use this when you need to find specific content without reading the entire document. Read-only."),
		mcp.WithTitleAnnotation("Search Document"),
		readOnlyHints(false),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The document filename to search"),
		),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("The search query (case-insensitive)"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filename, err := request.RequireString("filename")
		if err != nil {
			return mcp.NewToolResultError("filename parameter is required"), nil
		}

		query, err := request.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError("query parameter is required"), nil
		}

		result, err := reader.SearchDocument(ctx, filename, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error searching document: %v", err)), nil
		}

		return mcp.NewToolResultText(result), nil
	})
}

func registerGetDocumentSummary(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("get_document_summary",
		mcp.WithDescription("Get a quick summary by extracting the first 3 pages or ~100 lines of a document. Use this to preview document content before deciding to read it in full. Read-only."),
		mcp.WithTitleAnnotation("Get Document Summary"),
		readOnlyHints(false),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The document filename to summarize"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filename, err := request.RequireString("filename")
		if err != nil {
			return mcp.NewToolResultError("filename parameter is required"), nil
		}

		text, err := reader.GetDocumentSummary(ctx, filename)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting summary: %v", err)), nil
		}

		return mcp.NewToolResultText(text), nil
	})
}

// registerGetDocumentMetadata adds get_document_metadata, which reports a
// document's properties as pdfinfo sees them.
func registerGetDocumentMetadata(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("get_document_metadata",
		mcp.WithDescription("Get document metadata including title, author, dates, page count, and file size. Use this when you need document properties without reading its content; returns full PDF-specific fields (subject, creator, producer, version) for PDF files. Read-only."),
		mcp.WithTitleAnnotation("Get Document Metadata"),
		readOnlyHints(false),
		mcp.WithOutputSchema[pdf.DocumentMetadata](),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The document filename to get metadata for"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filename, err := request.RequireString("filename")
		if err != nil {
			return mcp.NewToolResultError("filename parameter is required"), nil
		}

		metadata, err := reader.GetDocumentMetadata(ctx, filename)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting metadata: %v", err)), nil
		}

		return structuredResult(metadata, metadata), nil
	})
}

func registerExtractImages(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("extract_images",
		mcp.WithDescription("Extract embedded images from a PDF as base64-encoded data, up to 10 per call. Use this when you need to retrieve figures, charts, or photos embedded in a PDF document. Read-only."),
		mcp.WithTitleAnnotation("Extract Images"),
		readOnlyHints(false),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The document filename to extract images from"),
		),
		mcp.WithNumber("page",
			mcp.Description("Optional page number to extract images from. If omitted, extracts from all pages."),
		),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filename, err := request.RequireString("filename")
		if err != nil {
			return mcp.NewToolResultError("filename parameter is required"), nil
		}

		page := request.GetInt("page", 0)

		images, err := reader.ExtractImages(ctx, filename, page)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error extracting images: %v", err)), nil
		}

		data, err := json.MarshalIndent(images, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error marshaling images: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	})
}

// urlToolEnabled reports whether read_url should be offered. Setting
// DOCS_MCP_DISABLE_URL=1 removes it, for installs that must never reach the
// network on a model's request.
func urlToolEnabled() bool {
	disabled, err := strconv.ParseBool(os.Getenv("DOCS_MCP_DISABLE_URL"))

	return err != nil || !disabled
}

func registerReadURL(s *server.MCPServer, reader *pdf.Reader, fetcher *urlFetcher) {
	tool := mcp.NewTool("read_url",
		mcp.WithDescription("Download a document from a URL and extract its text content (max 50MB). Use this when the document is hosted online rather than in the local directory; supports PDF and plain text URLs. Read-only, downloads to a temporary file."),
		mcp.WithTitleAnnotation("Read URL"),
		readOnlyHints(true),
		mcp.WithString("url",
			mcp.Required(),
			mcp.Description("The URL of the document to download and read"),
		),
		mcp.WithString("pages",
			mcp.Description("Optional page ranges to read, e.g. \"1-5\", \"10\", \"1-3,7,10-12\". If omitted, returns full text."),
		),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rawURL, err := request.RequireString("url")
		if err != nil {
			return mcp.NewToolResultError("url parameter is required"), nil
		}

		pagesStr := request.GetString("pages", "")

		text, err := downloadAndRead(ctx, fetcher, reader, rawURL, pagesStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error reading URL: %v", err)), nil
		}

		return mcp.NewToolResultText(text), nil
	})
}

func registerOCRDocument(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("ocr_document",
		mcp.WithDescription("Force OCR text extraction on a PDF, bypassing normal text extraction. Use this when read_document returns garbled or empty text from a scanned PDF; requires tesseract and pdftoppm. Read-only."),
		mcp.WithTitleAnnotation("OCR Document"),
		readOnlyHints(false),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The PDF filename to OCR"),
		),
		mcp.WithNumber("page",
			mcp.Description("Optional page number to OCR (1-based). If omitted, OCRs all pages."),
		),
		mcp.WithString("language",
			mcp.Description("Tesseract language code (default: DOCS_MCP_OCR_LANG, or \"eng\"). Use \"spa\" for Spanish, \"fra\" for French, etc. Run 'tesseract --list-langs' to see available languages."),
		),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !reader.HasOCR() {
			return mcp.NewToolResultError("OCR not available: tesseract and/or pdftoppm not installed. Install with: brew install tesseract poppler"), nil
		}

		filename, err := request.RequireString("filename")
		if err != nil {
			return mcp.NewToolResultError("filename parameter is required"), nil
		}

		page := request.GetInt("page", 0)
		// The reader falls back to DOCS_MCP_OCR_LANG and then "eng" when the
		// call brings no language, so the default here must stay empty.
		language := request.GetString("language", "")

		text, err := reader.OCRDocument(ctx, filename, page, language)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("OCR error: %v", err)), nil
		}

		if strings.TrimSpace(text) == "" {
			return mcp.NewToolResultText("[OCR completed but no text was detected on the page(s)]"), nil
		}

		return mcp.NewToolResultText(text), nil
	})
}

// downloadAndRead fetches rawURL and returns its text: PDFs through the
// reader, text formats as they came. The downloaded file is removed before
// returning.
func downloadAndRead(ctx context.Context, fetcher *urlFetcher, reader *pdf.Reader, rawURL, pagesStr string) (string, error) {
	path, err := fetcher.download(ctx, rawURL)
	if err != nil {
		return "", err
	}
	defer os.Remove(path)

	return readDownloaded(ctx, reader, path, pagesStr)
}

// readDownloaded extracts the text of a file saved by urlFetcher.download.
// The extension it was saved with says what it is; the reader's ReadFile only
// understands PDFs, so text formats are read here.
func readDownloaded(ctx context.Context, reader *pdf.Reader, path, pagesStr string) (string, error) {
	if strings.EqualFold(filepath.Ext(path), ".pdf") {
		if pagesStr != "" {
			return reader.ReadFilePages(ctx, path, pagesStr)
		}
		return reader.ReadFile(ctx, path)
	}

	if pagesStr != "" {
		return "", errors.New("page ranges are only supported for PDF documents; call read_url without pages to read the whole text")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read downloaded file: %w", err)
	}

	return string(data), nil
}

// urlFetcher downloads documents for read_url without letting a URL reach
// the machine itself or its private network.
type urlFetcher struct {
	client *http.Client

	// checkHost decides whether a host name may be contacted, before the
	// first request and on every redirect. Production uses
	// rejectPrivateHost; tests swap it to reach httptest servers.
	checkHost func(ctx context.Context, host string) error
}

// newURLFetcher returns the fetcher read_url uses: every download is bounded
// by timeout, and private addresses are refused both when the URL is checked
// and when the connection is dialled.
func newURLFetcher(timeout time.Duration) *urlFetcher {
	return newGuardedFetcher(timeout, rejectPrivateHost, rejectPrivateDial)
}

// newGuardedFetcher builds a fetcher with the given checks. checkDial runs
// on the address the dialer actually connects to, after DNS, so a name that
// resolves to a public address at check time and to a private one at dial
// time (DNS rebinding) is still refused; nil skips it.
func newGuardedFetcher(timeout time.Duration, checkHost func(context.Context, string) error, checkDial func(network, address string, conn syscall.RawConn) error) *urlFetcher {
	dialer := &net.Dialer{
		Timeout: urlDialTimeout,
		Control: checkDial,
	}

	// An environment proxy would make the dialer connect to the proxy, not
	// to the target, and the dial check would judge the wrong address.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialer.DialContext

	fetcher := &urlFetcher{checkHost: checkHost}
	fetcher.client = &http.Client{
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: fetcher.checkRedirect,
	}

	return fetcher
}

// checkRedirect applies the same target rules to every redirect hop, so a
// public URL cannot bounce the download to an internal one.
func (fetcher *urlFetcher) checkRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= maxURLRedirects {
		return fmt.Errorf("stopped after %d redirects", maxURLRedirects)
	}

	return fetcher.checkTarget(request.Context(), request.URL)
}

// checkTarget refuses URLs read_url must not fetch: any scheme other than
// http and https, a missing host, and a host checkHost rejects.
func (fetcher *urlFetcher) checkTarget(ctx context.Context, target *url.URL) error {
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q: only http and https are allowed", target.Scheme)
	}

	host := target.Hostname()
	if host == "" {
		return errors.New("URL has no host")
	}

	return fetcher.checkHost(ctx, host)
}

// download fetches rawURL into a temporary file whose extension matches the
// response's content type and returns its path. The caller removes the file.
func (fetcher *urlFetcher) download(ctx context.Context, rawURL string) (string, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	if err := fetcher.checkTarget(ctx, target); err != nil {
		return "", err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	resp, err := fetcher.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("failed to download document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	extension, err := extensionForContentType(resp.Header.Get("Content-Type"))
	if err != nil {
		return "", err
	}

	tmpFile, err := os.CreateTemp("", "go-docs-mcp-*"+extension)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}

	// Read one byte past the cap so a file of exactly 50MB is accepted and a
	// larger one is detected without downloading the rest.
	limited := io.LimitReader(resp.Body, maxURLFileSize+1)
	written, copyErr := io.Copy(tmpFile, limited)
	closeErr := tmpFile.Close()

	switch {
	case copyErr != nil:
		err = fmt.Errorf("failed to download document: %w", copyErr)
	case written > maxURLFileSize:
		err = errors.New("document exceeds maximum size of 50MB")
	case closeErr != nil:
		err = fmt.Errorf("failed to save document: %w", closeErr)
	}
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}

	return tmpFile.Name(), nil
}

// extensionForContentType maps a response's Content-Type to the extension
// the download is saved with. PDFs and text are accepted; a missing type and
// application/octet-stream are treated as PDF, as servers serving PDFs often
// send one or the other.
func extensionForContentType(contentType string) (string, error) {
	if contentType == "" {
		return ".pdf", nil
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", fmt.Errorf("unexpected content type: %s", contentType)
	}

	switch {
	case mediaType == "application/pdf", mediaType == "application/octet-stream":
		return ".pdf", nil
	case mediaType == "text/markdown", mediaType == "text/x-markdown":
		return ".md", nil
	case mediaType == "text/csv":
		return ".csv", nil
	case strings.HasPrefix(mediaType, "text/"):
		return ".txt", nil
	}

	return "", fmt.Errorf("unexpected content type: %s (expected application/pdf or text/*)", contentType)
}

// rejectPrivateHost returns errPrivateAddress when host is localhost or
// resolves to any address that is not public. Every resolved address must
// pass: a name with one public and one private record could be dialled on
// either.
func rejectPrivateHost(ctx context.Context, host string) error {
	lowerHost := strings.ToLower(strings.TrimSuffix(host, "."))
	if lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".localhost") {
		return fmt.Errorf("%w: %s", errPrivateAddress, host)
	}

	// A literal IP comes back as is, without a DNS query.
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("cannot resolve %s: %w", host, err)
	}

	for _, address := range addresses {
		if isNonPublicAddress(address) {
			return fmt.Errorf("%w: %s resolves to %s", errPrivateAddress, host, address.Unmap())
		}
	}

	return nil
}

// rejectPrivateDial is a net.Dialer Control hook: it runs with the resolved
// address just before connecting and refuses a non-public one.
func rejectPrivateDial(network, address string, _ syscall.RawConn) error {
	addressPort, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("cannot check dial address %s: %w", address, err)
	}

	if isNonPublicAddress(addressPort.Addr()) {
		return fmt.Errorf("%w: %s", errPrivateAddress, addressPort.Addr())
	}

	return nil
}

// isNonPublicAddress reports whether address belongs to the machine or a
// private network: RFC 1918 and IPv6 ULA, loopback, link-local (which holds
// the 169.254.169.254 metadata endpoint), unspecified, multicast and the
// nonPublicPrefixes. IPv4-mapped IPv6 is unmapped first so ::ffff:127.0.0.1
// is judged as 127.0.0.1.
func isNonPublicAddress(address netip.Addr) bool {
	address = address.Unmap().WithZone("")

	if !address.IsValid() ||
		address.IsPrivate() ||
		address.IsLoopback() ||
		address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() ||
		address.IsInterfaceLocalMulticast() ||
		address.IsMulticast() ||
		address.IsUnspecified() {
		return true
	}

	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}

	return false
}

// registerListFormats adds list_formats, which says which formats can be
// read on this machine and what each one needs installed.
func registerListFormats(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("list_formats",
		mcp.WithDescription("Show all supported document formats and their dependency installation status. Use this to check which formats are available and diagnose missing dependencies. Read-only."),
		mcp.WithTitleAnnotation("List Formats"),
		readOnlyHints(false),
		mcp.WithOutputSchema[formatList](),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		formats := formatList{Formats: nonNil(reader.ListFormats())}

		return structuredResult(formats, formats), nil
	})
}

func registerReadImage(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("read_image",
		mcp.WithDescription("Extract text from a standalone image file (PNG, JPG, TIFF, BMP) using OCR. Use this when you need to read text from an image rather than a document; supports multiple languages via tesseract. Read-only."),
		mcp.WithTitleAnnotation("Read Image"),
		readOnlyHints(false),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The image filename to OCR (must be in the documents directory)"),
		),
		mcp.WithString("language",
			mcp.Description("Tesseract language code (default: DOCS_MCP_OCR_LANG, or \"eng\"). Use \"spa\" for Spanish, \"fra\" for French, etc."),
		),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filename, err := request.RequireString("filename")
		if err != nil {
			return mcp.NewToolResultError("filename parameter is required"), nil
		}

		// The reader falls back to DOCS_MCP_OCR_LANG and then "eng" when the
		// call brings no language, so the default here must stay empty.
		language := request.GetString("language", "")

		text, err := reader.ReadImage(ctx, filename, language)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error reading image: %v", err)), nil
		}

		if strings.TrimSpace(text) == "" {
			return mcp.NewToolResultText("[OCR completed but no text was detected in the image]"), nil
		}

		return mcp.NewToolResultText(text), nil
	})
}

// registerGetDocumentOutline adds get_document_outline, which lists the
// headings of a document with their level and page.
func registerGetDocumentOutline(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("get_document_outline",
		mcp.WithDescription("Extract the heading structure and table of contents from a document. Use this to understand document organization before reading specific sections; detects numbered sections, ALL-CAPS headings, and markdown # headings. Read-only."),
		mcp.WithTitleAnnotation("Get Document Outline"),
		readOnlyHints(false),
		mcp.WithOutputSchema[documentOutline](),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The document filename to extract outline from"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filename, err := request.RequireString("filename")
		if err != nil {
			return mcp.NewToolResultError("filename parameter is required"), nil
		}

		outline, err := reader.GetDocumentOutline(ctx, filename)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error extracting outline: %v", err)), nil
		}

		// An empty outline has always printed "[]"; nonNil keeps it that way
		// in the text and in the structured content alike.
		entries := nonNil(outline)

		return structuredResult(documentOutline{Outline: entries}, entries), nil
	})
}

// registerExtractTables adds extract_tables, which finds rows and columns in
// a document's text.
func registerExtractTables(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("extract_tables",
		mcp.WithDescription("Extract table-like structures from a document, detecting pipe-delimited, tab-delimited, and multi-space-delimited columns. Use this when you need structured tabular data from a PDF, CSV, or text file. Read-only."),
		mcp.WithTitleAnnotation("Extract Tables"),
		readOnlyHints(false),
		mcp.WithOutputSchema[documentTables](),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The document filename to extract tables from"),
		),
		mcp.WithNumber("page",
			mcp.Description("Optional page number for PDFs (1-based). If omitted, extracts from all pages."),
		),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filename, err := request.RequireString("filename")
		if err != nil {
			return mcp.NewToolResultError("filename parameter is required"), nil
		}

		page := request.GetInt("page", 0)

		tables, err := reader.ExtractTables(ctx, filename, page)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error extracting tables: %v", err)), nil
		}

		// An empty result has always printed "[]"; nonNil keeps it that way
		// in the text and in the structured content alike.
		found := nonNil(tables)

		return structuredResult(documentTables{Tables: found}, found), nil
	})
}

func registerConvertToMarkdown(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("convert_to_markdown",
		mcp.WithDescription("Convert a document to clean Markdown format. Use this when you need structured, readable output from any document; for PDFs headings are detected and formatted, for TXT/CSV content is wrapped in code blocks, and MD files are returned as-is. Read-only."),
		mcp.WithTitleAnnotation("Convert to Markdown"),
		readOnlyHints(false),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The document filename to convert"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filename, err := request.RequireString("filename")
		if err != nil {
			return mcp.NewToolResultError("filename parameter is required"), nil
		}

		markdown, err := reader.ConvertToMarkdown(ctx, filename)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error converting to markdown: %v", err)), nil
		}

		return mcp.NewToolResultText(markdown), nil
	})
}

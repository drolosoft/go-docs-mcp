package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/drolosoft/go-docs-mcp/internal/pdf"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const maxURLFileSize = 50 * 1024 * 1024 // 50MB

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

	// Create MCP server
	s := server.NewMCPServer(
		"go-docs-mcp",
		serverVersion(),
		server.WithToolCapabilities(true),
	)

	// Register tools (13 total)
	registerListDocuments(s, reader)
	registerReadDocument(s, reader)
	registerSearchDocument(s, reader)
	registerGetDocumentSummary(s, reader)
	registerGetDocumentMetadata(s, reader)
	registerExtractImages(s, reader)
	registerReadURL(s, reader)
	registerOCRDocument(s, reader)
	registerListFormats(s, reader)
	registerReadImage(s, reader)
	registerGetDocumentOutline(s, reader)
	registerExtractTables(s, reader)
	registerConvertToMarkdown(s, reader)

	// Run stdio transport
	stdio := server.NewStdioServer(s)
	if err := stdio.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func registerListDocuments(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("list_documents",
		mcp.WithDescription("List all documents in the configured directory with format detection and metadata (filename, pages, size). Use this to discover available documents before reading or searching. Read-only."),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		docs, err := reader.ListDocuments()
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error listing documents: %v", err)), nil
		}

		data, err := json.MarshalIndent(docs, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error marshaling results: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerReadDocument(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("read_document",
		mcp.WithDescription("Read text content from a document with optional page selection. Use this when you need the raw text of a PDF, TXT, MD, CSV, or DOCX file; TXT/MD/CSV are returned verbatim, DOCX via pandoc; PDFs support page ranges (e.g. \"1-5\", \"1-3,7,10-12\") and auto-OCR fallback for scanned pages. Read-only."),
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
			text, err := reader.ReadDocumentPages(filename, pagesStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Error reading document pages: %v", err)), nil
			}
			return mcp.NewToolResultText(text), nil
		}

		page := request.GetInt("page", 0)
		text, err := reader.ReadDocument(filename, page)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error reading document: %v", err)), nil
		}

		return mcp.NewToolResultText(text), nil
	})
}

func registerSearchDocument(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("search_document",
		mcp.WithDescription("Search for text within a document and return matching lines with context and approximate page numbers. Use this when you need to find specific content without reading the entire document. Read-only."),
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

		result, err := reader.SearchDocument(filename, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error searching document: %v", err)), nil
		}

		return mcp.NewToolResultText(result), nil
	})
}

func registerGetDocumentSummary(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("get_document_summary",
		mcp.WithDescription("Get a quick summary by extracting the first 3 pages or ~100 lines of a document. Use this to preview document content before deciding to read it in full. Read-only."),
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

		text, err := reader.GetDocumentSummary(filename)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting summary: %v", err)), nil
		}

		return mcp.NewToolResultText(text), nil
	})
}

func registerGetDocumentMetadata(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("get_document_metadata",
		mcp.WithDescription("Get document metadata including title, author, dates, page count, and file size. Use this when you need document properties without reading its content; returns full PDF-specific fields (subject, creator, producer, version) for PDF files. Read-only."),
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

		metadata, err := reader.GetDocumentMetadata(filename)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting metadata: %v", err)), nil
		}

		data, err := json.MarshalIndent(metadata, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error marshaling metadata: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerExtractImages(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("extract_images",
		mcp.WithDescription("Extract embedded images from a PDF as base64-encoded data, up to 10 per call. Use this when you need to retrieve figures, charts, or photos embedded in a PDF document. Read-only."),
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

		images, err := reader.ExtractImages(filename, page)
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

func registerReadURL(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("read_url",
		mcp.WithDescription("Download a document from a URL and extract its text content (max 50MB). Use this when the document is hosted online rather than in the local directory; supports PDF and plain text URLs. Read-only, downloads to a temporary file."),
		mcp.WithString("url",
			mcp.Required(),
			mcp.Description("The URL of the document to download and read"),
		),
		mcp.WithString("pages",
			mcp.Description("Optional page ranges to read, e.g. \"1-5\", \"10\", \"1-3,7,10-12\". If omitted, returns full text."),
		),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		url, err := request.RequireString("url")
		if err != nil {
			return mcp.NewToolResultError("url parameter is required"), nil
		}

		pagesStr := request.GetString("pages", "")

		text, err := downloadAndReadPDF(reader, url, pagesStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error reading URL: %v", err)), nil
		}

		return mcp.NewToolResultText(text), nil
	})
}

func registerOCRDocument(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("ocr_document",
		mcp.WithDescription("Force OCR text extraction on a PDF, bypassing normal text extraction. Use this when read_document returns garbled or empty text from a scanned PDF; requires tesseract and pdftoppm. Read-only."),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The PDF filename to OCR"),
		),
		mcp.WithNumber("page",
			mcp.Description("Optional page number to OCR (1-based). If omitted, OCRs all pages."),
		),
		mcp.WithString("language",
			mcp.Description("Tesseract language code (default: \"eng\"). Use \"spa\" for Spanish, \"fra\" for French, etc. Run 'tesseract --list-langs' to see available languages."),
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
		language := request.GetString("language", "eng")

		text, err := reader.OCRDocument(filename, page, language)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("OCR error: %v", err)), nil
		}

		if strings.TrimSpace(text) == "" {
			return mcp.NewToolResultText("[OCR completed but no text was detected on the page(s)]"), nil
		}

		return mcp.NewToolResultText(text), nil
	})
}

func downloadAndReadPDF(reader *pdf.Reader, url, pagesStr string) (string, error) {
	// Download the PDF
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to download PDF: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Validate content type (allow application/pdf and application/octet-stream)
	contentType := resp.Header.Get("Content-Type")
	if contentType != "" &&
		!strings.Contains(contentType, "application/pdf") &&
		!strings.Contains(contentType, "application/octet-stream") {
		return "", fmt.Errorf("unexpected content type: %s (expected application/pdf)", contentType)
	}

	// Create temp file
	tmpFile, err := os.CreateTemp("", "go-docs-mcp-*.pdf")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Download with size limit
	limited := io.LimitReader(resp.Body, maxURLFileSize+1)
	n, err := io.Copy(tmpFile, limited)
	if err != nil {
		return "", fmt.Errorf("failed to download PDF: %w", err)
	}
	if n > maxURLFileSize {
		return "", fmt.Errorf("PDF exceeds maximum size of 50MB")
	}

	tmpFile.Close()

	// Extract text from the downloaded PDF
	if pagesStr != "" {
		return reader.ReadFilePages(tmpFile.Name(), pagesStr)
	}
	return reader.ReadFile(tmpFile.Name())
}

func registerListFormats(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("list_formats",
		mcp.WithDescription("Show all supported document formats and their dependency installation status. Use this to check which formats are available and diagnose missing dependencies. Read-only."),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		formats := reader.ListFormats()

		result := map[string]interface{}{
			"formats": formats,
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error marshaling results: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerReadImage(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("read_image",
		mcp.WithDescription("Extract text from a standalone image file (PNG, JPG, TIFF, BMP) using OCR. Use this when you need to read text from an image rather than a document; supports multiple languages via tesseract. Read-only."),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The image filename to OCR (must be in the documents directory)"),
		),
		mcp.WithString("language",
			mcp.Description("Tesseract language code (default: \"eng\"). Use \"spa\" for Spanish, \"fra\" for French, etc."),
		),
	)

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filename, err := request.RequireString("filename")
		if err != nil {
			return mcp.NewToolResultError("filename parameter is required"), nil
		}

		language := request.GetString("language", "eng")

		text, err := reader.ReadImage(filename, language)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error reading image: %v", err)), nil
		}

		if strings.TrimSpace(text) == "" {
			return mcp.NewToolResultText("[OCR completed but no text was detected in the image]"), nil
		}

		return mcp.NewToolResultText(text), nil
	})
}

func registerGetDocumentOutline(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("get_document_outline",
		mcp.WithDescription("Extract the heading structure and table of contents from a document. Use this to understand document organization before reading specific sections; detects numbered sections, ALL-CAPS headings, and markdown # headings. Read-only."),
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

		outline, err := reader.GetDocumentOutline(filename)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error extracting outline: %v", err)), nil
		}

		if len(outline) == 0 {
			return mcp.NewToolResultText("[]"), nil
		}

		data, err := json.MarshalIndent(outline, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error marshaling outline: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerExtractTables(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("extract_tables",
		mcp.WithDescription("Extract table-like structures from a document, detecting pipe-delimited, tab-delimited, and multi-space-delimited columns. Use this when you need structured tabular data from a PDF, CSV, or text file. Read-only."),
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

		tables, err := reader.ExtractTables(filename, page)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error extracting tables: %v", err)), nil
		}

		if len(tables) == 0 {
			return mcp.NewToolResultText("[]"), nil
		}

		data, err := json.MarshalIndent(tables, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error marshaling tables: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerConvertToMarkdown(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("convert_to_markdown",
		mcp.WithDescription("Convert a document to clean Markdown format. Use this when you need structured, readable output from any document; for PDFs headings are detected and formatted, for TXT/CSV content is wrapped in code blocks, and MD files are returned as-is. Read-only."),
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

		markdown, err := reader.ConvertToMarkdown(filename)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error converting to markdown: %v", err)), nil
		}

		return mcp.NewToolResultText(markdown), nil
	})
}

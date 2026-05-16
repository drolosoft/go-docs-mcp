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

	"github.com/juanatsap/go-pdf-mcp/internal/pdf"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const maxURLFileSize = 50 * 1024 * 1024 // 50MB

func main() {
	// Determine documents directory: PDF_MCP_DIR (primary), DROLO_DOCS_DIR (backward compat)
	docsDir := os.Getenv("PDF_MCP_DIR")
	if docsDir == "" {
		docsDir = os.Getenv("DROLO_DOCS_DIR") // backward compatibility
	}
	if docsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("cannot determine home directory: %v", err)
		}
		docsDir = filepath.Join(home, ".pdf-mcp", "documents")
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
		"go-pdf-mcp",
		"3.0.0",
		server.WithToolCapabilities(true),
	)

	// Register tools (8 total)
	registerListDocuments(s, reader)
	registerReadDocument(s, reader)
	registerSearchDocument(s, reader)
	registerGetDocumentSummary(s, reader)
	registerGetDocumentMetadata(s, reader)
	registerExtractImages(s, reader)
	registerReadURL(s, reader)
	registerOCRDocument(s, reader)

	// Run stdio transport
	stdio := server.NewStdioServer(s)
	if err := stdio.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func registerListDocuments(s *server.MCPServer, reader *pdf.Reader) {
	tool := mcp.NewTool("list_documents",
		mcp.WithDescription("List all available PDF documents with metadata (filename, title, pages, size)"),
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
		mcp.WithDescription("Read the text content of a PDF document. Specify a single page with 'page', or multiple pages with 'pages' (e.g. \"1-5\", \"1-3,7,10-12\"). If neither is provided, returns full text."),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The PDF filename to read"),
		),
		mcp.WithNumber("page",
			mcp.Description("Optional single page number to read (1-based). If omitted, returns full text."),
		),
		mcp.WithString("pages",
			mcp.Description("Optional page ranges to read, e.g. \"1-5\", \"10\", \"1-3,7,10-12\". Overrides 'page' if both provided."),
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
		mcp.WithDescription("Search for text within a PDF document. Returns matching lines with context and approximate page numbers."),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The PDF filename to search"),
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
		mcp.WithDescription("Get a summary of a PDF document (first 3 pages of text)."),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The PDF filename to summarize"),
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
		mcp.WithDescription("Get full PDF metadata: title, author, subject, creator, producer, dates, page count, file size, and PDF version."),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The PDF filename to get metadata for"),
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
		mcp.WithDescription("Extract images from a PDF document as base64-encoded data. Returns up to 10 images per call."),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("The PDF filename to extract images from"),
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
		mcp.WithDescription("Download a PDF from a URL and extract its text content. Max file size: 50MB."),
		mcp.WithString("url",
			mcp.Required(),
			mcp.Description("The URL of the PDF to download and read"),
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
		mcp.WithDescription("Force OCR text extraction on a PDF document, bypassing pdftotext. Useful for image-based/scanned PDFs or when pdftotext returns garbled text. Requires tesseract and pdftoppm."),
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
	tmpFile, err := os.CreateTemp("", "go-pdf-mcp-*.pdf")
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

# go-docs-mcp

Go MCP server serving documents (PDF, TXT, MD, CSV, images) to LLMs via stdio.

## Build & Run

```bash
make build          # ./go-docs-mcp binary
make install        # /usr/local/bin/go-docs-mcp
make test           # go test -v -race ./...
DOCS_MCP_DIR=~/docs ./go-docs-mcp   # custom dir
```

## Architecture

- `main.go` — MCP server init, 12 tool registrations, stdio transport
- `internal/pdf/reader.go` — Document extraction (pdftotext/pdfinfo/pdfimages/tesseract), mtime cache, search, metadata, images, OCR, outlines, tables

## Key Decisions

- Uses `pdftotext -layout` for text extraction (preserves formatting)
- mtime-based cache: re-extracts only when file changes
- Path traversal blocked via `filepath.Base()` + extension allowlist check
- Page tracking uses form feed (`\f`) characters from pdftotext output
- Homebrew path detection for macOS (`/opt/homebrew/bin/`)
- Env var: `DOCS_MCP_DIR` (primary), `PDF_MCP_DIR` (backward compat), `DROLO_DOCS_DIR` (legacy)
- Default directory: `~/.docs-mcp/documents/`
- URL downloads limited to 50MB, validated Content-Type
- Image extraction limited to 10 per call to avoid huge responses
- Supports multiple document types: PDF, TXT, MD, CSV, DOCX (with pandoc), images (with tesseract)

## Tools

| Tool | Params | Notes |
|------|--------|-------|
| `list_documents` | none | Scans docsDir for supported files, returns JSON array |
| `read_document` | `filename` (req), `page` (opt), `pages` (opt) | Full text, single page, or page ranges |
| `search_document` | `filename` (req), `query` (req) | Case-insensitive, 2-line context |
| `get_document_summary` | `filename` (req) | First 3 pages |
| `get_document_metadata` | `filename` (req) | Full PDF metadata via pdfinfo |
| `extract_images` | `filename` (req), `page` (opt) | Extract images as base64 (max 10) |
| `read_url` | `url` (req), `pages` (opt) | Download PDF from URL + extract text |
| `ocr_document` | `filename` (req), `page` (opt), `language` (opt) | Force OCR on PDF |
| `list_formats` | none | Show supported formats and dependency status |
| `read_image` | `filename` (req), `language` (opt) | OCR standalone image file |
| `get_document_outline` | `filename` (req) | Extract heading structure |
| `extract_tables` | `filename` (req), `page` (opt) | Extract table-like structures |

## Dependencies

- `github.com/mark3labs/mcp-go` — MCP SDK
- External: `pdftotext`, `pdfinfo`, `pdfimages` (poppler)
- Optional: `tesseract` (OCR), `pandoc` (DOCX)

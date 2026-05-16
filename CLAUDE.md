# go-pdf-mcp

Go MCP server serving PDF documents to LLMs via stdio.

## Build & Run

```bash
make build          # ./go-pdf-mcp binary
make install        # /usr/local/bin/go-pdf-mcp
make test           # go test -v -race ./...
PDF_MCP_DIR=~/pdfs ./go-pdf-mcp   # custom dir
```

## Architecture

- `main.go` — MCP server init, 7 tool registrations, stdio transport
- `internal/pdf/reader.go` — PDF extraction (pdftotext/pdfinfo/pdfimages), mtime cache, search, metadata, images

## Key Decisions

- Uses `pdftotext -layout` for text extraction (preserves formatting)
- mtime-based cache: re-extracts only when file changes
- Path traversal blocked via `filepath.Base()` + `.pdf` suffix check
- Page tracking uses form feed (`\f`) characters from pdftotext output
- Homebrew path detection for macOS (`/opt/homebrew/bin/`)
- Env var: `PDF_MCP_DIR` (primary), `DROLO_DOCS_DIR` (backward compat fallback)
- URL downloads limited to 50MB, validated Content-Type
- Image extraction limited to 10 per call to avoid huge responses

## Tools

| Tool | Params | Notes |
|------|--------|-------|
| `list_documents` | none | Scans docsDir, returns JSON array |
| `read_document` | `filename` (req), `page` (opt), `pages` (opt) | Full text, single page, or page ranges |
| `search_document` | `filename` (req), `query` (req) | Case-insensitive, 2-line context |
| `get_document_summary` | `filename` (req) | First 3 pages |
| `get_document_metadata` | `filename` (req) | Full PDF metadata via pdfinfo |
| `extract_images` | `filename` (req), `page` (opt) | Extract images as base64 (max 10) |
| `read_url` | `url` (req), `pages` (opt) | Download PDF from URL + extract text |

## Dependencies

- `github.com/mark3labs/mcp-go` — MCP SDK
- External: `pdftotext`, `pdfinfo`, `pdfimages` (poppler)

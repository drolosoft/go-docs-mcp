# drolo-mcp-docs

Go MCP server serving PDF documents to LLMs via stdio.

## Build & Run

```bash
make build          # ./drolo-mcp-docs binary
make install        # /usr/local/bin/drolo-mcp-docs
make test           # go test -v -race ./...
DROLO_DOCS_DIR=~/pdfs ./drolo-mcp-docs   # custom dir
```

## Architecture

- `main.go` — MCP server init, 4 tool registrations, stdio transport
- `internal/pdf/reader.go` — PDF extraction (pdftotext/pdfinfo), mtime cache, search, security

## Key Decisions

- Uses `pdftotext -layout` for text extraction (preserves formatting)
- mtime-based cache: re-extracts only when file changes
- Path traversal blocked via `filepath.Base()` + `.pdf` suffix check
- Page tracking uses form feed (`\f`) characters from pdftotext output
- Homebrew path detection for macOS (`/opt/homebrew/bin/`)

## Tools

| Tool | Params | Notes |
|------|--------|-------|
| `list_documents` | none | Scans docsDir, returns JSON array |
| `read_document` | `filename` (req), `page` (opt) | Full text or single page |
| `search_document` | `filename` (req), `query` (req) | Case-insensitive, 2-line context |
| `get_document_summary` | `filename` (req) | First 3 pages |

## Dependencies

- `github.com/mark3labs/mcp-go` — MCP SDK
- External: `pdftotext`, `pdfinfo` (poppler)

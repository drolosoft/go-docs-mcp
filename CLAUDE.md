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

- `main.go` — MCP server init, 13 tool registrations, stdio transport
- `internal/pdf/reader.go` — Document extraction (pdftotext/pdfinfo/pdfimages/tesseract), mtime cache, search, metadata, images, OCR, outlines, tables

## Key Decisions

- Uses `pdftotext -layout` for text extraction (preserves formatting)
- mtime-based cache: re-extracts only when file changes
- Confinement: `filepath.Base()` + extension allowlist, then `filepath.EvalSymlinks` of the file must stay under the resolved `DOCS_MCP_DIR` (symlinks out are rejected and not listed)
- Every external process runs with `exec.CommandContext` and a per-operation deadline; page ranges are capped at 200 and checked against the real page count
- Tools are registered with read-only / non-destructive / idempotent annotations; only `read_url` is open-world, and `DOCS_MCP_DISABLE_URL=1` removes it
- Page tracking uses form feed (`\f`) characters from pdftotext output
- Homebrew path detection for macOS (`/opt/homebrew/bin/`)
- Env vars: `DOCS_MCP_DIR` (primary), `PDF_MCP_DIR` (backward compat), `DROLO_DOCS_DIR` (legacy), `DOCS_MCP_OCR_LANG` (tesseract language, default `eng`), `DOCS_MCP_DISABLE_URL`
- Default directory: `~/.docs-mcp/documents/`
- URL downloads: 60 s timeout, 50MB cap, `application/pdf` or `text/*`, private/loopback/link-local hosts refused on every hop
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
| `read_url` | `url` (req), `pages` (opt) | Download a PDF or text document from a URL + extract text |
| `convert_to_markdown` | `filename` (req) | Convert a document to Markdown |
| `ocr_document` | `filename` (req), `page` (opt), `language` (opt) | Force OCR on PDF |
| `list_formats` | none | Show supported formats and dependency status |
| `read_image` | `filename` (req), `language` (opt) | OCR standalone image file |
| `get_document_outline` | `filename` (req) | Extract heading structure |
| `extract_tables` | `filename` (req), `page` (opt) | Extract table-like structures |

## Dependencies

- `github.com/mark3labs/mcp-go` — MCP SDK
- External: `pdftotext`, `pdfinfo`, `pdfimages` (poppler)
- Optional: `tesseract` (OCR), `pandoc` (DOCX)

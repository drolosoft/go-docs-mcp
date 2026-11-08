# go-docs-mcp

Go MCP server serving documents (PDF, TXT, MD, CSV, Office files, EPUB, HTML, RTF, IPYNB, images) to LLMs via stdio.

## Build & Run

```bash
make build          # ./go-docs-mcp binary
make install        # /usr/local/bin/go-docs-mcp
make test           # go test -v -race ./...
DOCS_MCP_DIR=~/docs ./go-docs-mcp   # custom dir
```

## Architecture

- `main.go` — MCP server init, 13 tool registrations, instructions, stdio transport
- `resources.go` — `docs://{filename}` resources, list rebuilt on every `resources/list`
- `internal/pdf/reader.go` — Document extraction (pdftotext/pdfinfo/pdfimages/tesseract), mtime cache, search, metadata, images, OCR, outlines, tables

## Key Decisions

- Uses `pdftotext -layout` for text extraction (preserves formatting)
- mtime-based cache: re-extracts only when file changes; bounded by `DOCS_MCP_CACHE_MB` (default 256, 0 disables) with LRU eviction, a text larger than the cap is not cached; summary, outline and tables share it
- Confinement: `filepath.Base()` + extension allowlist, then `filepath.EvalSymlinks` of the file must stay under the resolved `DOCS_MCP_DIR` (symlinks out are rejected and not listed)
- Every external process runs with `exec.CommandContext` and a per-operation deadline; page ranges are capped at 200 and checked against the real page count
- Tools are registered with read-only / non-destructive / idempotent annotations; only `read_url` is open-world, and `DOCS_MCP_DISABLE_URL=1` removes it
- Page tracking uses form feed (`\f`) characters from pdftotext output
- Homebrew path detection for macOS (`/opt/homebrew/bin/`)
- Env vars: `DOCS_MCP_DIR` (primary), `PDF_MCP_DIR` (backward compat), `DROLO_DOCS_DIR` (legacy), `DOCS_MCP_OCR_LANG` (tesseract language, default `eng`), `DOCS_MCP_DISABLE_URL`, `DOCS_MCP_URL_PROXY` (opt-in proxy for read_url), `DOCS_MCP_CACHE_MB` (text cache cap)
- Default directory: `~/.docs-mcp/documents/`
- Text tools page their output: `offset` + `max_chars` (default 50000, cap 200000) with a trailer naming the next offset; `search_document` has `max_results`; `extract_images` returns PNG image content, skips masks, scales to 1600 px, 8 MB budget
- Structured output (`outputSchema` + `structuredContent`) on list_documents, list_formats, get_document_metadata, get_document_outline, extract_tables (text JSON kept for old clients), and on the paged read_document, convert_to_markdown and ocr_document (`text`, `next_offset`, `total_chars`)
- URL downloads: 60 s timeout, 50MB cap, `application/pdf` or `text/*`, private/loopback/link-local hosts refused on every hop
- Image extraction: `pdfimages -list` first, only rows of type `image` (soft masks skipped), `-png`, box-filtered to 1600 px, 4 MB per image, `max_images` (default 10) and 8 MB per answer
- Tool results over 9 MB are replaced with an error (`server.WithToolResultSizeLimit`)
- Supports: PDF, TXT, MD, CSV, and through pandoc DOCX, XLSX, PPTX, ODT, EPUB, HTML, RTF, IPYNB; images (PNG, JPG, WebP, TIFF, BMP) with tesseract

## Tools

| Tool | Params | Notes |
|------|--------|-------|
| `list_documents` | none | Scans docsDir for supported files and images, returns JSON array with `type` |
| `read_document` | `filename` (req), `page`, `pages`, `offset`, `max_chars` (opt) | Full text, single page, or page ranges, paged |
| `search_document` | `filename` (req), `query` (req), `max_results` (opt) | Case-insensitive, 2-line context, 50 matches default, 500 max |
| `get_document_summary` | `filename` (req) | First 3 pages |
| `get_document_metadata` | `filename` (req) | Full PDF metadata via pdfinfo |
| `extract_images` | `filename` (req), `page`, `max_images` (opt) | PNG image content, masks skipped, 1600 px, 8 MB |
| `read_url` | `url` (req), `pages` (opt) | Download a PDF or text document from a URL + extract text |
| `convert_to_markdown` | `filename` (req), `offset`, `max_chars` (opt) | Convert a document to Markdown (GFM via pandoc for Office formats) |
| `ocr_document` | `filename` (req), `page`, `language`, `offset`, `max_chars` (opt) | Force OCR on PDF; progress notifications when the call has a progress token |
| `list_formats` | none | Show supported formats and dependency status |
| `read_image` | `filename` (req), `language` (opt) | OCR standalone image file |
| `get_document_outline` | `filename` (req) | Extract heading structure |
| `extract_tables` | `filename` (req), `page` (opt) | Extract table-like structures |

## Dependencies

- `github.com/mark3labs/mcp-go` — MCP SDK
- External: `pdftotext`, `pdfinfo`, `pdfimages` (poppler)
- Optional: `tesseract` (OCR), `pandoc` (Office, EPUB, HTML, RTF, IPYNB)

## CI and release

- `.github/workflows/ci.yml`: gofmt, vet, `go test -race -cover` with poppler, tesseract and pandoc installed, govulncheck; Ubuntu and macOS.
- `.github/workflows/release.yml`: goreleaser on tag `v*`, notes from the matching CHANGELOG section.
- Homebrew formula lives in `drolosoft/homebrew-tap`; the Claude Code plugin in `drolosoft/claude-plugins`.

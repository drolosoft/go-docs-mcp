<p align="center">
  <img src="assets/icon.png" alt="Go-Docs MCP" width="128" height="128">
</p>

<h1 align="center">Go-Docs MCP</h1>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.27.3+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go"></a>
  <a href="https://glama.ai/mcp/servers/drolosoft/go-docs-mcp"><img src="https://glama.ai/mcp/servers/drolosoft/go-docs-mcp/badges/score.svg" alt="go-docs-mcp MCP server"></a>
  <a href="https://github.com/drolosoft/go-docs-mcp/releases"><img src="https://img.shields.io/github/v/release/drolosoft/go-docs-mcp" alt="GitHub Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
</p>

> **Install and Go.** One command, single binary. Your AI reads any document — PDF, text, Markdown, Office files, EPUB, HTML, notebooks, images.

MCP server for multi-format document access — read, search, extract images, OCR, and fetch documents from URLs via the [Model Context Protocol](https://modelcontextprotocol.io). 13 tools, 14 formats, zero configuration.

```bash
go install github.com/drolosoft/go-docs-mcp@latest
# That's it. Single binary, starts in milliseconds.
```

For a deeper look at why an MCP server beats a direct tool, see **[Why MCP?](doc/WHY-MCP.md)**

---

## 🏆 Why Go-Docs MCP?

Every other document MCP server handles **one format** — a PDF server for PDFs, a DOCX server for DOCX. You'd need three separate servers to read three formats.

| | Go-Docs MCP | Others |
|--|:---:|:---:|
| Single binary, no runtime | **Yes** | Need Node/Python |
| `go install` one-liner | **Yes** | npm+deps or pip+venv |
| Multi-format (14 types) | **Yes** | One format each |
| Full-text search | **Yes** | Partial or none |
| OCR (scanned PDFs + images) | **Yes** | Rare |
| Image & table extraction | **Yes** | Partial |
| Document outline | **Yes** | Rare |
| Fetch from URL | **Yes** | Rare |
| Confined to one directory, read-only | **Yes** | Varies |
| Smart caching | **Yes** | No |
| Fully offline | **Yes** | Yes |

Go-Docs MCP reads them all from a single binary — fast, secure, and dependency-free at runtime.

---

## 📋 Features — 13 Tools

| Category | Tool | Description |
|----------|------|-------------|
| **Discovery** | `list_documents` | List all documents with metadata (format, pages, size) |
| **Discovery** | `list_formats` | List supported formats and dependency status |
| **Reading** | `read_document` | Full text, specific page, or page ranges from any format |
| **Reading** | `read_url` | Download from URL and extract text (50MB max) |
| **Reading** | `get_document_summary` | First 3 pages as a quick overview |
| **Search** | `search_document` | Case-insensitive full-text search with context |
| **Analysis** | `get_document_metadata` | Title, author, dates, version, page count |
| **Analysis** | `get_document_outline` | Heading structure deduced from the text |
| **Analysis** | `extract_tables` | Tables as structured data |
| **Analysis** | `extract_images` | Images as PNG image content, scaled to fit (max 10 per call, 8 MB budget) |
| **OCR** | `ocr_document` | Force OCR on scanned/image-based PDFs |
| **OCR** | `read_image` | Extract text from PNG, JPG, WebP, TIFF, BMP via OCR |
| **Export** | `convert_to_markdown` | Convert any document to clean Markdown |

**Highlights:**

- **Fast** — mtime-based in-memory caching avoids redundant extraction
- **Multi-format** — PDF, text, Markdown, CSV, DOCX, XLSX, PPTX, ODT, EPUB, HTML, RTF, Jupyter notebooks and images from one server
- **Paged output** — `offset` and `max_chars` on the text tools, so a 200 MB file never floods the model
- **Resources** — every document is also a `docs://` resource, so Claude Code can @-mention it
- **OCR** — automatic fallback to tesseract for scanned documents
- **Secure** — directory-locked with path traversal prevention
- **Portable** — works on macOS and Linux

---

## 📄 Supported Formats

| Format | Dependencies | Notes |
|--------|-------------|-------|
| PDF | poppler (`pdftotext`, `pdfinfo`, `pdfimages`, `pdftoppm`) | Full support — text, images, metadata, OCR fallback |
| TXT, MD, CSV | None | Native, zero dependencies. CSV is parsed with `encoding/csv`, so quoted newlines work |
| DOCX, XLSX, PPTX, ODT | pandoc (optional) | Office documents, text and tables |
| EPUB, HTML, RTF, IPYNB | pandoc (optional) | Books, web pages, rich text and Jupyter notebooks |
| Images (PNG, JPG, WebP, TIFF, BMP) | tesseract (optional) | OCR text extraction; listed by `list_documents` with `"type": "image"` |

`convert_to_markdown` returns GitHub-flavoured Markdown for every pandoc-backed format.

---

## 📦 Prerequisites

- **Go 1.27.3+** ([install](https://go.dev/doc/install))
- **poppler** — required for PDF support
- **tesseract** _(optional)_ — enables OCR for scanned docs and images
- **pandoc** _(optional)_ — enables DOCX support

```bash
# macOS
brew install poppler
brew install tesseract        # optional: OCR
brew install pandoc           # optional: DOCX, XLSX, PPTX, ODT, EPUB, HTML, RTF, IPYNB

# Debian/Ubuntu
apt install poppler-utils
apt install tesseract-ocr     # optional: OCR
apt install pandoc            # optional: Office, EPUB, HTML, RTF, IPYNB

# Fedora/RHEL
dnf install poppler-utils
dnf install tesseract         # optional: OCR
dnf install pandoc            # optional: Office, EPUB, HTML, RTF, IPYNB
```

> **Note:** TXT, MD, and CSV work out of the box with zero dependencies. Install only what you need.

---

## 🚀 Installation

### Homebrew (macOS and Linux)

```bash
brew install drolosoft/tap/go-docs-mcp   # brings poppler, tesseract and pandoc with it
```

### Prebuilt binaries

Every release on the [releases page](https://github.com/drolosoft/go-docs-mcp/releases) ships archives for macOS (Apple Silicon and Intel), Linux (amd64 and arm64) and Windows (amd64), with a `checksums.txt` to verify them.

### Docker

```bash
docker build -t go-docs-mcp .
docker run -i --rm -v /path/to/your/documents:/documents go-docs-mcp
```

The image runs as a non-root user, includes poppler, tesseract (English data) and pandoc, and serves `/documents`.

### From source

```bash
go install github.com/drolosoft/go-docs-mcp@latest
```

### Build locally

```bash
git clone https://github.com/drolosoft/go-docs-mcp.git
cd go-docs-mcp
make build      # produces ./go-docs-mcp
make install    # installs to /usr/local/bin/
```

---

## ⚙️ Configuration

Go-Docs MCP reads documents from a configured directory. Set `DOCS_MCP_DIR` to change it:

| Variable | Default | Description |
|----------|---------|-------------|
| `DOCS_MCP_DIR` | `~/.docs-mcp/documents/` | Directory containing documents to serve |
| `PDF_MCP_DIR` | _(legacy alias)_ | Backward-compatible alias for `DOCS_MCP_DIR` |
| `DOCS_MCP_OCR_LANG` | `eng` | Tesseract language used when a call brings none |
| `DOCS_MCP_CACHE_MB` | `256` | Upper bound of the in-memory text cache; least recently used documents are evicted first, `0` turns it off |
| `DOCS_MCP_DISABLE_URL` | _(unset)_ | Set to `1` to remove the `read_url` tool |

Place your documents in the directory and the server finds them automatically. All supported formats are detected.

### Resources

Every document is also exposed as an MCP resource with the URI `docs://<filename>` (percent-encoded). Clients that support resources, such as Claude Code with `@docs://report.pdf`, can pull a document into the conversation without calling a tool. The list is rebuilt on every `resources/list`, so files added after startup show up.

### Structured output

`list_documents`, `list_formats`, `get_document_metadata`, `get_document_outline` and `extract_tables` declare an output schema and return `structuredContent` next to the JSON text they always returned, so older clients see no change.

---

## 💡 Usage

### With Claude Code

Register the server from the terminal:

```bash
claude mcp add docs -e DOCS_MCP_DIR=/path/to/your/documents -- go-docs-mcp
```

Or add it to the `.mcp.json` of the project you want it in:

```json
{
  "mcpServers": {
    "docs": {
      "command": "go-docs-mcp",
      "env": {
        "DOCS_MCP_DIR": "/path/to/your/documents"
      }
    }
  }
}
```

### With Claude Desktop

Add to `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS):

```json
{
  "mcpServers": {
    "docs": {
      "command": "/usr/local/bin/go-docs-mcp",
      "env": {
        "DOCS_MCP_DIR": "/path/to/your/documents"
      }
    }
  }
}
```

### With any MCP client

The server communicates over **stdio** using JSON-RPC 2.0:

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | go-docs-mcp
```

---

## 📖 Tool Reference

### `list_documents`

Lists all documents in the configured directory with format detection.

**Parameters:** None

**Example output:**
```json
[
  {
    "filename": "architecture-guide.pdf",
    "title": "architecture-guide",
    "type": "pdf",
    "pages": 42,
    "size_bytes": 1048576
  },
  {
    "filename": "notes.md",
    "title": "notes",
    "type": "markdown",
    "pages": 0,
    "size_bytes": 4096
  },
  {
    "filename": "receipt.png",
    "title": "receipt",
    "type": "image",
    "pages": 0,
    "size_bytes": 220144
  }
]
```

---

### `list_formats`

Lists all supported document formats and their dependency status.

**Parameters:** None

---

### `read_document`

Reads the extracted text content of a document. Automatically falls back to OCR if the document is image-based/scanned and `pdftotext` returns empty text.

Long texts come in pages. A page that does not reach the end finishes with `[truncated: showing chars 0-50000 of 123456; call again with offset=50000]`, and the structured result carries `text`, `next_offset` (absent on the last page) and `total_chars`. Offsets count characters, not bytes.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The document filename to read |
| `page` | number | No | Single page number (1-based). Omit for full text. |
| `pages` | string | No | Page ranges, e.g. "1-5", "10", "1-3,7,10-12". Overrides `page`. |
| `offset` | number | No | Character offset to start from (default 0). |
| `max_chars` | number | No | Characters to return (default 50000, at most 200000). |

`convert_to_markdown` and `ocr_document` take the same two parameters.

**Example input:**
```json
{
  "filename": "architecture-guide.pdf",
  "pages": "1-3,10-12"
}
```

---

### `search_document`

Searches within a document for lines matching a query. Returns matches with 2 lines of context and approximate page numbers.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The document filename to search |
| `query` | string | Yes | Search query (case-insensitive) |
| `max_results` | number | No | Matches to show (default 50, at most 500). The header still counts all of them. |

**Example output:**
```
Found 3 matches for 'microservice' in architecture-guide.pdf:

--- Match 1 (page ~2, line 45) ---
  The system is composed of several
> microservice components that communicate
  via gRPC and message queues.
```

---

### `get_document_summary`

Returns the text from the first 3 pages of a document as a quick summary.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The document filename to summarize |

---

### `get_document_metadata`

Returns full document metadata.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The document filename to get metadata for |

**Example output:**
```json
{
  "title": "Architecture Guide",
  "author": "Jane Doe",
  "subject": "System Design",
  "creator": "LaTeX",
  "producer": "pdfTeX",
  "creation_date": "Thu May 15 10:30:00 2025",
  "modification_date": "Thu May 15 10:30:00 2025",
  "pages": 42,
  "file_size_bytes": 1048576,
  "pdf_version": "1.5"
}
```

---

### `get_document_outline`

Extracts the heading structure of a document as a structured list. For PDFs the outline is deduced from the extracted text (short lines that look like headings), not read from the PDF bookmarks; for Markdown and text files it comes from `#` headings.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The document filename to extract outline from |

---

### `extract_tables`

Extracts tables from a document as structured data.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The document filename to extract tables from |
| `page` | number | No | Specific page to extract from. Omit for all pages. |

---

### `extract_images`

Extracts the images of a PDF and returns each one as MCP image content (`image/png`), so clients that render images show them. Alpha masks and stencil masks are skipped, images are scaled down to 1600 px on the longer side, an image still over 4 MB after scaling is shrunk until it fits, and the images of one answer stay under 8 MB. A text item before the images describes them and says why any was left out.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The document filename to extract images from |
| `page` | number | No | Specific page to extract from. Omit for all pages. |
| `max_images` | number | No | Images to return (default 10, at most 50). |

**Example output:** a text item that describes the images, then one `image/png` item per image.
```json
{
  "images": [
    {
      "page": 1,
      "index": 0,
      "format": "png",
      "mime_type": "image/png",
      "width": 1600,
      "height": 1200,
      "original_width": 2048,
      "original_height": 1536
    }
  ],
  "found": 3,
  "notes": ["returned the first 1 of 3 images; raise max_images or ask for one page to see the others"]
}
```

---

### `read_url`

Downloads a document from a URL and extracts its text content. Accepts `application/pdf` and `text/*` responses, up to 50MB. Set `DOCS_MCP_DISABLE_URL=1` to remove this tool entirely.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `url` | string | Yes | The URL of the document to download and read |
| `pages` | string | No | Page ranges to extract, e.g. "1-5". Omit for full text. |

**Example input:**
```json
{
  "url": "https://example.com/report.pdf",
  "pages": "1-3"
}
```

---

### `ocr_document`

Forces OCR on a PDF document using tesseract. Useful for scanned/image-based PDFs or when `pdftotext` returns garbled text. Requires `tesseract` and `pdftoppm`.

> **Note:** `read_document` already auto-detects image-based PDFs and falls back to OCR. Use `ocr_document` when you want to force OCR regardless, or need to specify a non-English language.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The PDF filename to OCR |
| `page` | number | No | Specific page to OCR (1-based). Omit for all pages. |
| `language` | string | No | Tesseract language code (default: `DOCS_MCP_OCR_LANG`, then `eng`). Use `spa`, `fra`, etc. |
| `offset`, `max_chars` | number | No | Same paging as `read_document`. |

**Example input:**
```json
{
  "filename": "scanned-contract.pdf",
  "page": 1,
  "language": "spa"
}
```

---

### `read_image`

Extracts text from an image file using OCR. Supports PNG, JPG, WebP, TIFF and BMP. Requires `tesseract`.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The image filename to read (PNG, JPG, WebP, TIFF, BMP) |
| `language` | string | No | Tesseract language code (default: `DOCS_MCP_OCR_LANG`, then `eng`). |

**Example input:**
```json
{
  "filename": "receipt.png",
  "language": "eng"
}
```

---

## 🔒 Security

- **Confined to one directory** — every file is resolved with its symlinks and must end up inside `DOCS_MCP_DIR`. A symlink that points outside is not listed and cannot be read. Filenames with path separators or `../` are rejected before that.
- **Extension filter** — only supported formats are served.
- **Read-only** — the server never writes inside `DOCS_MCP_DIR`, and every tool is announced to clients as read-only, non-destructive and idempotent.
- **Timeouts on every external process** — poppler, tesseract and pandoc run under a context deadline (60 s per extraction, 120 s per OCR page, 10 min per OCR call) and are killed when it passes. Page ranges are checked against the real page count and capped at 200 pages per call.
- **`read_url`** — 60 s timeout, 50MB limit, `application/pdf` or `text/*` only, temp files removed right after reading. Hosts that resolve to private, loopback, link-local or cloud metadata addresses are refused, on the first request and on every redirect. Set `DOCS_MCP_DISABLE_URL=1` to drop the tool from the server if your documents may contain untrusted instructions.
- **Bounded results** — text tools return pages of at most 200,000 characters, search shows at most 500 matches and images stop at 8 MB per call; any tool result over 9 MB is replaced with an error. The text cache is capped by `DOCS_MCP_CACHE_MB`.
- **Error messages** carry file names, never absolute paths of the host.

How to report a problem is in [SECURITY.md](SECURITY.md).

---

## 🛠️ Development

```bash
make build     # Build the binary
make test      # Run tests with race detector
make clean     # Remove build artifacts
```

CI runs gofmt, vet, the race tests with poppler, tesseract and pandoc installed, and govulncheck on every push. A tag `v*` builds the release archives with goreleaser. See [CONTRIBUTING.md](CONTRIBUTING.md).

### Project structure

```
go-docs-mcp/
  main.go              # MCP server setup, 13 tool registrations
  resources.go         # docs:// resources
  internal/
    pdf/
      reader.go        # Document extraction, caching, search, metadata, images, OCR
  Makefile             # Build targets
  go.mod               # Module definition
```

---

## 🦙 Glama Score

<p align="center">
  <a href="https://glama.ai/mcp/servers/drolosoft/go-docs-mcp"><img src="https://glama.ai/mcp/servers/drolosoft/go-docs-mcp/badges/card.svg" alt="Go-Docs MCP on Glama"></a>
</p>

---

## 📄 License

[MIT](LICENSE) - Copyright 2026 Drolosoft

---

## 💛 Support

<p align="center">
<a href="https://buymeacoffee.com/juan.andres.morenorub.io"><img src="https://cdn.buymeacoffee.com/buttons/v2/default-yellow.png" alt="Buy Me A Coffee" height="50"></a>
</p>

---

**[Drolosoft](https://drolosoft.com)** — *Tools we wish existed*

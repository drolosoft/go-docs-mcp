<p align="center">
  <img src="assets/icon.png" alt="Go-Docs MCP" width="128" height="128">
</p>

<h1 align="center">Go-Docs MCP</h1>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go"></a>
  <a href="https://glama.ai/mcp/servers/drolosoft/go-docs-mcp"><img src="https://glama.ai/mcp/servers/drolosoft/go-docs-mcp/badges/score.svg" alt="go-docs-mcp MCP server"></a>
  <a href="https://github.com/drolosoft/go-docs-mcp/releases"><img src="https://img.shields.io/github/v/release/drolosoft/go-docs-mcp" alt="GitHub Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
  <img src="https://img.shields.io/badge/macOS-000000?logo=apple&logoColor=white" alt="macOS">
  <img src="https://img.shields.io/badge/Linux-FCC624?logo=linux&logoColor=black" alt="Linux">
</p>

> **Install and Go.** One command, single binary. Your AI reads any document — PDF, text, Markdown, DOCX, images.

MCP server for multi-format document access — read, search, extract images, OCR, and fetch documents from URLs via the [Model Context Protocol](https://modelcontextprotocol.io). 12 tools, 6 formats, zero configuration.

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
| Multi-format (6 types) | **Yes** | One format each |
| Full-text search | **Yes** | Partial or none |
| OCR (scanned PDFs + images) | **Yes** | Rare |
| Image & table extraction | **Yes** | Partial |
| Document outline | **Yes** | Rare |
| Fetch from URL | **Yes** | Rare |
| Dir-locked, read-only | **Yes** | Varies |
| Smart caching | **Yes** | No |
| Fully offline | **Yes** | Yes |

Go-Docs MCP reads them all from a single binary — fast, secure, and dependency-free at runtime.

---

## 📋 Features — 12 Tools

| Category | Tool | Description |
|----------|------|-------------|
| **Discovery** | `list_documents` | List all documents with metadata (format, pages, size) |
| **Discovery** | `list_formats` | List supported formats and dependency status |
| **Reading** | `read_document` | Full text, specific page, or page ranges from any format |
| **Reading** | `read_url` | Download from URL and extract text (50MB max) |
| **Reading** | `get_document_summary` | First 3 pages as a quick overview |
| **Search** | `search_document` | Case-insensitive full-text search with context |
| **Analysis** | `get_document_metadata` | Title, author, dates, version, page count |
| **Analysis** | `get_document_outline` | Table of contents / bookmarks |
| **Analysis** | `extract_tables` | Tables as structured data |
| **Analysis** | `extract_images` | Images as base64 (max 10 per call) |
| **OCR** | `ocr_document` | Force OCR on scanned/image-based PDFs |
| **OCR** | `read_image` | Extract text from PNG, JPG, TIFF via OCR |

**Highlights:**

- **Fast** — mtime-based in-memory caching avoids redundant extraction
- **Multi-format** — PDF, TXT, MD, CSV, DOCX, and images from one server
- **OCR** — automatic fallback to tesseract for scanned documents
- **Secure** — directory-locked with path traversal prevention
- **Portable** — works on macOS and Linux

---

## 📄 Supported Formats

| Format | Dependencies | Notes |
|--------|-------------|-------|
| PDF | poppler (`pdftotext`, `pdfinfo`, `pdfimages`, `pdftoppm`) | Full support — text, images, metadata, OCR fallback |
| TXT, MD, CSV | None | Native, zero dependencies |
| DOCX | pandoc (optional) | Word document extraction |
| Images (PNG, JPG, TIFF) | tesseract (optional) | OCR text extraction |

---

## 📦 Prerequisites

- **Go 1.25+** ([install](https://go.dev/doc/install))
- **poppler** — required for PDF support
- **tesseract** _(optional)_ — enables OCR for scanned docs and images
- **pandoc** _(optional)_ — enables DOCX support

```bash
# macOS
brew install poppler
brew install tesseract        # optional: OCR
brew install pandoc           # optional: DOCX

# Debian/Ubuntu
apt install poppler-utils
apt install tesseract-ocr     # optional: OCR
apt install pandoc            # optional: DOCX

# Fedora/RHEL
dnf install poppler-utils
dnf install tesseract         # optional: OCR
dnf install pandoc            # optional: DOCX
```

> **Note:** TXT, MD, and CSV work out of the box with zero dependencies. Install only what you need.

---

## 🚀 Installation

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

Place your documents in the directory and the server finds them automatically. All supported formats are detected.

---

## 💡 Usage

### With Claude Code

Add to your `.claude/settings.json`:

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
    "format": "pdf",
    "title": "architecture-guide",
    "pages": 42,
    "size_bytes": 1048576
  },
  {
    "filename": "notes.md",
    "format": "markdown",
    "title": "notes",
    "size_bytes": 4096
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

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The document filename to read |
| `page` | number | No | Single page number (1-based). Omit for full text. |
| `pages` | string | No | Page ranges, e.g. "1-5", "10", "1-3,7,10-12". Overrides `page`. |

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

Extracts the document outline (table of contents / bookmarks) as a structured list.

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

Extracts images from a document as base64-encoded data. Returns up to 10 images per call.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The document filename to extract images from |
| `page` | number | No | Specific page to extract from. Omit for all pages. |

**Example output:**
```json
[
  {
    "page": 1,
    "index": 0,
    "format": "jpeg",
    "width": 800,
    "height": 600,
    "data_base64": "/9j/4AAQSkZJRg..."
  }
]
```

---

### `read_url`

Downloads a document from a URL and extracts its text content. Maximum file size: 50MB.

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
| `language` | string | No | Tesseract language code (default: `eng`). Use `spa`, `fra`, etc. |

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

Extracts text from an image file using OCR. Supports PNG, JPG, and TIFF. Requires `tesseract`.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The image filename to read (PNG, JPG, TIFF) |
| `language` | string | No | Tesseract language code (default: `eng`). |

**Example input:**
```json
{
  "filename": "receipt.png",
  "language": "eng"
}
```

---

## 🔒 Security

- **Directory-locked** — only files within `DOCS_MCP_DIR` are accessible
- **Path traversal prevention** — filenames sanitized; `../` rejected
- **Extension filter** — only supported formats served
- **Read-only** — no write operations
- **URL downloads** — 50MB limit, Content-Type validated, temp files cleaned immediately

---

## 🛠️ Development

```bash
make build     # Build the binary
make test      # Run tests with race detector
make clean     # Remove build artifacts
```

### Project structure

```
go-docs-mcp/
  main.go              # MCP server setup, 12 tool registrations
  internal/
    pdf/
      reader.go        # Document extraction, caching, search, metadata, images, OCR
  Makefile             # Build targets
  go.mod               # Module definition
```

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

<p align="center">
  <img src="assets/icon.png" alt="go-docs-mcp" width="128" height="128">
</p>

<h1 align="center">go-docs-mcp</h1>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
  <a href="https://modelcontextprotocol.io"><img src="https://img.shields.io/badge/MCP-compatible-blue" alt="MCP"></a>
</p>

> **Install and Go.** One command, single binary. Your AI reads any document — PDF, text, Markdown, DOCX, images.

Go MCP server for multi-format document access — read, search, extract images, OCR, and fetch documents from URLs via the [Model Context Protocol](https://modelcontextprotocol.io).

```bash
go install github.com/drolosoft/go-docs-mcp@latest
# That's it. Single binary, starts in milliseconds.
```

For a deeper look at why an MCP server beats a direct tool, see **[Why MCP?](doc/WHY-MCP.md)**

---

## 🏆 Why go-docs-mcp?

|  | Capability | go-docs-mcp | Node/TS MCPs | Python MCPs | Rust MCPs |
|--|-----------|:---:|:---:|:---:|:---:|
| ⚡ | Single binary, no runtime | ✅ | ❌ Node required | ❌ Python required | ✅ |
| 📦 | `go install` one-liner | ✅ | ❌ npm + deps | ❌ pip + venv | ❌ cargo build |
| 📄 | Multi-format (PDF+TXT+MD+DOCX+CSV) | ✅ | ❌ single format | ❌ single format | ❌ single format |
| 🔍 | Full-text search with context | ✅ | ⚠️ some | ✅ | ✅ |
| 👁️ | OCR (scanned PDFs + images) | ✅ | ❌ | ❌ | ⚠️ some |
| 🖼️ | Image extraction (base64) | ✅ | ⚠️ some | ✅ | ❌ |
| 📊 | Table extraction | ✅ | ⚠️ some | ✅ | ✅ |
| 📑 | Document outline / TOC | ✅ | ❌ | ❌ | ⚠️ some |
| 🌐 | Fetch documents from URL | ✅ | ⚠️ some | ❌ | ❌ |
| 🔒 | Directory-locked, read-only | ✅ | ⚠️ varies | ⚠️ varies | ✅ |
| 💾 | Smart caching (mtime-based) | ✅ | ❌ | ❌ | ❌ |
| 🏠 | Self-hosted / fully offline | ✅ | ✅ | ✅ | ✅ |

Every other document MCP server handles **one format** — a PDF server for PDFs, a DOCX server for DOCX. You'd need three tools to read three formats. go-docs-mcp reads them all from a single binary.

---

## 📋 Features — 12 Tools in 5 Categories

| Category | Tool | Description |
|----------|------|-------------|
| **Discovery** | `list_documents` | List all available documents with metadata (filename, format, page count, size) |
| **Discovery** | `list_formats` | List supported document formats and their dependencies |
| **Reading** | `read_document` | Read full text, a specific page, or page ranges from any supported document |
| **Reading** | `read_url` | Download a document from a URL and extract its text content |
| **Reading** | `get_document_summary` | Get the first 3 pages of text as a quick overview |
| **Search** | `search_document` | Case-insensitive full-text search with context and page hints |
| **Analysis** | `get_document_metadata` | Get full document metadata (title, author, dates, version, etc.) |
| **Analysis** | `get_document_outline` | Extract document outline / table of contents |
| **Analysis** | `extract_tables` | Extract tables from documents as structured data |
| **Analysis** | `extract_images` | Extract images from a document as base64-encoded data (max 10 per call) |
| **OCR** | `ocr_document` | Force OCR on a PDF — for scanned/image-based documents or garbled text |
| **OCR** | `read_image` | Extract text from an image file (PNG, JPG, TIFF) via OCR |

- **Fast** — mtime-based in-memory caching avoids redundant extraction
- **Multi-format** — PDF, TXT, MD, CSV, DOCX, and images from a single server
- **OCR** — automatic fallback to tesseract for image-based/scanned documents
- **Secure** — directory-locked access with path traversal prevention
- **Simple** — single binary, stdio transport, zero configuration required
- **Portable** — works on macOS and Linux

---

## 📄 Supported Formats

| Format | Dependencies | Notes |
|--------|-------------|-------|
| PDF | poppler (`pdftotext`, `pdfinfo`, `pdfimages`, `pdftoppm`) | Full support — text, images, metadata, OCR fallback |
| TXT, MD, CSV | None | Native, zero dependencies |
| DOCX | pandoc or Go lib (optional) | Word document extraction |
| Images (PNG, JPG, TIFF) | tesseract (optional) | OCR text extraction from image files |

---

## 📦 Prerequisites

- **Go 1.25+** ([install](https://go.dev/doc/install))
- **poppler** (provides `pdftotext`, `pdfinfo`, `pdfimages`, `pdftoppm`) — required for PDF support
- **tesseract** _(optional, for OCR support — scanned PDFs and images)_
- **pandoc** _(optional, for DOCX support)_

```bash
# macOS
brew install poppler
brew install tesseract        # optional: enables OCR for scanned docs + images
brew install pandoc           # optional: enables DOCX support

# Debian/Ubuntu
apt install poppler-utils
apt install tesseract-ocr     # optional: enables OCR for scanned docs + images
apt install pandoc            # optional: enables DOCX support

# Fedora/RHEL
dnf install poppler-utils
dnf install tesseract         # optional: enables OCR for scanned docs + images
dnf install pandoc            # optional: enables DOCX support
```

> **Format note:** TXT, MD, and CSV work out of the box with zero dependencies. PDF requires poppler. DOCX requires pandoc. Images require tesseract. Install only what you need.

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

The server reads documents from a configured directory. Set `DOCS_MCP_DIR` to change it:

| Variable | Default | Description |
|----------|---------|-------------|
| `DOCS_MCP_DIR` | `~/.docs-mcp/documents/` | Directory containing document files to serve |
| `PDF_MCP_DIR` | _(backward compat alias)_ | Legacy alias — works the same as `DOCS_MCP_DIR` |

Place your documents in the directory and the server will find them automatically. All supported formats (PDF, TXT, MD, CSV, DOCX, images) are detected.

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

Add to your Claude Desktop configuration (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS):

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

The server communicates over **stdio** using JSON-RPC 2.0. Launch the binary and pipe JSON-RPC messages to stdin:

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

Reads the extracted text content of a document. For PDFs, automatically falls back to OCR if the document is image-based/scanned and `pdftotext` returns empty text.

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

Forces OCR (Optical Character Recognition) on a PDF document using tesseract. Useful for scanned/image-based PDFs or when `pdftotext` returns garbled text. Requires `tesseract` and `pdftoppm` to be installed.

> **Note:** `read_document` already auto-detects image-based PDFs and falls back to OCR. Use `ocr_document` when you want to force OCR regardless, or need to specify a non-English language.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The PDF filename to OCR |
| `page` | number | No | Specific page to OCR (1-based). Omit to OCR all pages. |
| `language` | string | No | Tesseract language code (default: `eng`). Use `spa` for Spanish, `fra` for French, etc. |

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

Extracts text from an image file using OCR. Supports PNG, JPG, and TIFF. Requires `tesseract` to be installed.

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

- **Directory-locked**: Only files within the configured `DOCS_MCP_DIR` are accessible
- **Path traversal prevention**: Filenames are sanitized to their base component; `../` is rejected
- **Extension filter**: Only supported document formats are served; requests for other file types are denied
- **No write operations**: The server is strictly read-only
- **URL downloads**: Limited to 50MB, Content-Type validated, temp files cleaned up immediately

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

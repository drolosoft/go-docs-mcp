<p align="center">
  <img src="assets/icon.png" alt="go-pdf-mcp" width="128" height="128">
</p>

<h1 align="center">go-pdf-mcp</h1>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
  <a href="https://modelcontextprotocol.io"><img src="https://img.shields.io/badge/MCP-compatible-blue" alt="MCP"></a>
</p>

> **Install and Go.** One command, single binary, no Node, no Python, no config. Your AI reads PDFs.

Go MCP server for PDF document access — read, search, extract images, and fetch PDFs from URLs via the [Model Context Protocol](https://modelcontextprotocol.io).

```bash
go install github.com/drolosoft/go-pdf-mcp@latest
# That's it. 10MB binary, starts in milliseconds.
```

---

## 🏆 Why go-pdf-mcp?

| Feature | go-pdf-mcp | SylphxAI (TS) | hanweg (Python) | kreuzberg (Rust) |
|---------|:---:|:---:|:---:|:---:|
| Single binary install | ✅ | ❌ | ❌ | ❌ |
| `go install` one-liner | ✅ | ❌ | ❌ | ❌ |
| No Node/Python runtime | ✅ | ❌ | ❌ | ✅ |
| Read from URL | ✅ | ❌ | ❌ | ❌ |
| OCR for scanned PDFs | ✅ | ❌ | ❌ | ✅ |
| Image extraction (base64) | ✅ | ❌ | ❌ | ✅ |
| Page range syntax | ✅ | ✅ | ❌ | ❌ |
| Full-text search | ✅ | ❌ | ✅ | ✅ |
| Self-hosted / offline | ✅ | ✅ | ✅ | ✅ |
| License | MIT | MIT | MIT | MIT |

---

## 📋 Features

| Tool | Description |
|------|-------------|
| `list_documents` | List all available PDFs with metadata (filename, title, page count, size) |
| `read_document` | Read full text, a specific page, or page ranges from a PDF (auto-OCR fallback) |
| `search_document` | Case-insensitive full-text search with context and page hints |
| `get_document_summary` | Get the first 3 pages of text as a quick overview |
| `get_document_metadata` | Get full PDF metadata (title, author, dates, version, etc.) |
| `extract_images` | Extract images from a PDF as base64-encoded data (max 10 per call) |
| `read_url` | Download a PDF from a URL and extract its text content |
| `ocr_document` | Force OCR on a PDF — for scanned/image-based documents or garbled text |

- **Fast** — mtime-based in-memory caching avoids redundant extraction
- **OCR** — automatic fallback to tesseract for image-based/scanned PDFs
- **Secure** — directory-locked access with path traversal prevention, `.pdf` only
- **Simple** — single binary, stdio transport, zero configuration required
- **Portable** — works on macOS and Linux with poppler utilities

---

## 📦 Prerequisites

- **Go 1.22+** ([install](https://go.dev/doc/install))
- **poppler** (provides `pdftotext`, `pdfinfo`, `pdfimages`, `pdftoppm`)
- **tesseract** _(optional, for OCR support)_

```bash
# macOS
brew install poppler
brew install tesseract        # optional: enables OCR for scanned PDFs

# Debian/Ubuntu
apt install poppler-utils
apt install tesseract-ocr     # optional: enables OCR for scanned PDFs

# Fedora/RHEL
dnf install poppler-utils
dnf install tesseract         # optional: enables OCR for scanned PDFs
```

> **OCR note:** Without tesseract, everything works except OCR. Scanned/image-based PDFs will return empty text. With tesseract installed, `read_document` automatically detects image-based pages and falls back to OCR. The `ocr_document` tool lets you force OCR on any document.

---

## 🚀 Installation

### From source

```bash
go install github.com/drolosoft/go-pdf-mcp@latest
```

### Build locally

```bash
git clone https://github.com/drolosoft/go-pdf-mcp.git
cd go-pdf-mcp
make build      # produces ./go-pdf-mcp
make install    # installs to /usr/local/bin/
```

---

## ⚙️ Configuration

The server reads PDFs from a documents directory. Set `PDF_MCP_DIR` to change it:

| Variable | Default | Description |
|----------|---------|-------------|
| `PDF_MCP_DIR` | `~/.pdf-mcp/documents/` | Directory containing PDF files to serve |
| `DROLO_DOCS_DIR` | _(fallback)_ | Backward-compatible alias for `PDF_MCP_DIR` |

Place your PDF files in the documents directory and the server will find them automatically.

---

## 💡 Usage

### With Claude Code

Add to your `.claude/settings.json`:

```json
{
  "mcpServers": {
    "pdf": {
      "command": "go-pdf-mcp",
      "env": {
        "PDF_MCP_DIR": "/path/to/your/pdfs"
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
    "pdf": {
      "command": "/usr/local/bin/go-pdf-mcp",
      "env": {
        "PDF_MCP_DIR": "/path/to/your/pdfs"
      }
    }
  }
}
```

### With any MCP client

The server communicates over **stdio** using JSON-RPC 2.0. Launch the binary and pipe JSON-RPC messages to stdin:

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | go-pdf-mcp
```

---

## 📖 Tool Reference

### `list_documents`

Lists all PDF files in the configured documents directory.

**Parameters:** None

**Example output:**
```json
[
  {
    "filename": "architecture-guide.pdf",
    "title": "architecture-guide",
    "pages": 42,
    "size_bytes": 1048576
  }
]
```

---

### `read_document`

Reads the extracted text content of a PDF document. Automatically falls back to OCR if the PDF is image-based/scanned and `pdftotext` returns empty text.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The PDF filename to read |
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

Searches within a PDF for lines matching a query. Returns matches with 2 lines of context and approximate page numbers.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The PDF filename to search |
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
| `filename` | string | Yes | The PDF filename to summarize |

---

### `get_document_metadata`

Returns full PDF metadata extracted via `pdfinfo`.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The PDF filename to get metadata for |

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

### `extract_images`

Extracts images from a PDF document as base64-encoded data. Returns up to 10 images per call.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The PDF filename to extract images from |
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

Downloads a PDF from a URL and extracts its text content. Maximum file size: 50MB.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `url` | string | Yes | The URL of the PDF to download and read |
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

## 🔒 Security

- **Directory-locked**: Only files within the configured `PDF_MCP_DIR` are accessible
- **Path traversal prevention**: Filenames are sanitized to their base component; `../` is rejected
- **Extension filter**: Only `.pdf` files are served; requests for other file types are denied
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
go-pdf-mcp/
  main.go              # MCP server setup, 8 tool registrations
  internal/
    pdf/
      reader.go        # PDF extraction, caching, search, metadata, images, OCR
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

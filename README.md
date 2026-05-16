# drolo-mcp-docs

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![MCP](https://img.shields.io/badge/MCP-compatible-blue)](https://modelcontextprotocol.io)

**Go MCP server for PDF document access** — read, search, and summarize PDFs for LLMs via the [Model Context Protocol](https://modelcontextprotocol.io).

---

## Features

| Tool | Description |
|------|-------------|
| `list_documents` | List all available PDFs with metadata (filename, title, page count, size) |
| `read_document` | Read full text or a specific page from a PDF |
| `search_document` | Case-insensitive full-text search with context and page hints |
| `get_document_summary` | Get the first 3 pages of text as a quick overview |

- **Fast** — mtime-based in-memory caching avoids redundant extraction
- **Secure** — directory-locked access with path traversal prevention, `.pdf` only
- **Simple** — single binary, stdio transport, zero configuration required
- **Portable** — works on macOS and Linux with poppler utilities

## Prerequisites

- **Go 1.22+** ([install](https://go.dev/doc/install))
- **poppler** (provides `pdftotext` and `pdfinfo`)

```bash
# macOS
brew install poppler

# Debian/Ubuntu
apt install poppler-utils

# Fedora/RHEL
dnf install poppler-utils
```

## Installation

### From source

```bash
go install github.com/juanatsap/drolo-mcp-docs@latest
```

### Build locally

```bash
git clone https://github.com/juanatsap/drolo-mcp-docs.git
cd drolo-mcp-docs
make build      # produces ./drolo-mcp-docs
make install    # installs to /usr/local/bin/
```

## Configuration

The server reads PDFs from a documents directory. Set `DROLO_DOCS_DIR` to change it:

| Variable | Default | Description |
|----------|---------|-------------|
| `DROLO_DOCS_DIR` | `~/.drolo/documents/` | Directory containing PDF files to serve |

Place your PDF files in the documents directory and the server will find them automatically.

## Usage

### With Claude Code

Add to your `.claude/settings.json`:

```json
{
  "mcpServers": {
    "drolo-docs": {
      "command": "drolo-mcp-docs",
      "env": {
        "DROLO_DOCS_DIR": "/path/to/your/pdfs"
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
    "drolo-docs": {
      "command": "/usr/local/bin/drolo-mcp-docs",
      "env": {
        "DROLO_DOCS_DIR": "/path/to/your/pdfs"
      }
    }
  }
}
```

### With any MCP client

The server communicates over **stdio** using JSON-RPC 2.0. Launch the binary and pipe JSON-RPC messages to stdin:

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | drolo-mcp-docs
```

## Tool Reference

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

Reads the extracted text content of a PDF document.

**Parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `filename` | string | Yes | The PDF filename to read |
| `page` | number | No | Page number (1-based). Omit for full text. |

**Example input:**
```json
{
  "filename": "architecture-guide.pdf",
  "page": 1
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

## Security

- **Directory-locked**: Only files within the configured `DROLO_DOCS_DIR` are accessible
- **Path traversal prevention**: Filenames are sanitized to their base component; `../` is rejected
- **Extension filter**: Only `.pdf` files are served; requests for other file types are denied
- **No write operations**: The server is strictly read-only

## Development

```bash
make build     # Build the binary
make test      # Run tests with race detector
make clean     # Remove build artifacts
```

### Project structure

```
drolo-mcp-docs/
  main.go              # MCP server setup, tool registration
  internal/
    pdf/
      reader.go        # PDF extraction, caching, search
  Makefile             # Build targets
  go.mod               # Module definition
```

## License

[MIT](LICENSE) - Copyright 2026 Drolosoft

## Author

Built by [Drolosoft](https://github.com/juanatsap).

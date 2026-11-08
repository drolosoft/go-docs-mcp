# Changelog

All notable changes to go-docs-mcp are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [v1.3.0] - 2026-11-08

Security and dependency release. No tool was added or removed, and no tool
changed its parameters; clients that worked with v1.2.0 keep working.

### Security

- Documents are confined to `DOCS_MCP_DIR` for real: every file is resolved
  through its symlinks and must stay under the resolved directory. A symlink
  that points outside is no longer listed by `list_documents` and cannot be
  read by any tool. Before this release the check was only on the file name,
  so a link inside the directory could serve any file of the host.
- Every external process (poppler, tesseract, pandoc) now runs under the
  request context with a deadline (60 s per extraction, 120 s per OCR page,
  10 min per OCR call) and is killed when it passes. A hung process can no
  longer freeze the server's stdio workers.
- Page ranges are checked against the real page count of the PDF and capped
  at 200 pages per call. Asking for `pages="1-60"` on a one-page document
  used to launch 59 rendering processes and an OCR pass.
- `read_url` has a 60 s timeout, refuses hosts that resolve to private,
  loopback, link-local, carrier-grade NAT (100.64.0.0/10) or cloud metadata
  addresses on the first request, on every redirect and again when the
  socket connects, and can be removed from the server entirely with
  `DOCS_MCP_DISABLE_URL=1`. To keep that check meaningful, `read_url` no
  longer honours `HTTP_PROXY`/`HTTPS_PROXY`.
- Error messages no longer include absolute paths of the host.

### Changed

- Every tool is announced to MCP clients with its annotations: a human title,
  read-only, non-destructive and idempotent. Only `read_url` is marked as
  open-world. Clients used to see every tool as destructive, because the SDK
  defaults were never overridden.
- `read_url` accepts `text/*` responses, as its description always promised,
  and stores the download with an extension that matches the content type.
- OCR language comes from the call's `language` parameter, then from
  `DOCS_MCP_OCR_LANG`, then `eng`. The value is validated before it reaches
  tesseract.
- pandoc is looked up in the Homebrew directories as well, like poppler and
  tesseract, so DOCX works from clients that start with a minimal `PATH`.
- Dependencies: `github.com/mark3labs/mcp-go` v1.5.0, `golang.org/x/text`
  current release, Go 1.27.3. `govulncheck` reports no reachable
  vulnerability. GO-2026-6911 (mcp-go, SSRF through an
  unvalidated Location header, no fix released) is in a required module but
  not called by go-docs-mcp, which uses only the stdio transport.
- README, CLAUDE.md and doc/WHY-MCP corrected: 13 tools everywhere, Go 1.27.3,
  Claude Code configuration through `claude mcp add` or `.mcp.json`, the
  outline described as deduced from the text, and an honest Security section.

### Added

- `contract_test.go`: the server must expose exactly these 13 tools, by name,
  under the current MCP protocol.
- `SECURITY.md` and this changelog.

## [v1.2.0] - 2026-08-17

- `read_document`, `search_document` and `get_document_summary` read TXT, MD,
  CSV and DOCX, not only PDF.
- `convert_to_markdown` tool.
- Go 1.27.1 in `go.mod` and `golang:1.27-alpine` in the Dockerfile.

## [v1.1.0] and [v1.0.0]

Initial public releases: 12 tools over PDF, TXT, MD, CSV, DOCX and images,
stdio transport, mtime cache, OCR fallback with tesseract.

[Unreleased]: https://github.com/drolosoft/go-docs-mcp/compare/v1.3.0...HEAD
[v1.3.0]: https://github.com/drolosoft/go-docs-mcp/compare/v1.2.0...v1.3.0
[v1.2.0]: https://github.com/drolosoft/go-docs-mcp/compare/v1.1.0...v1.2.0

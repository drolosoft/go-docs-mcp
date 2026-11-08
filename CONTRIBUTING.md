# Contributing to go-docs-mcp

## Build and run

You need Go at the version in `go.mod` (1.27.3 or later).

```sh
make build                        # ./go-docs-mcp
DOCS_MCP_DIR=~/docs ./go-docs-mcp # serve a directory over stdio
```

## External tools

The server is a single Go binary, but it calls these programs at run time:

| Tool | Used for | macOS | Debian/Ubuntu |
|------|----------|-------|---------------|
| poppler (`pdftotext`, `pdfinfo`, `pdfimages`, `pdftoppm`) | PDF text, metadata, images | `brew install poppler` | `apt install poppler-utils` |
| tesseract | OCR of scanned PDFs and images | `brew install tesseract` | `apt install tesseract-ocr tesseract-ocr-eng` |
| pandoc | DOCX | `brew install pandoc` | `apt install pandoc` |

Tests that need a missing tool skip themselves. Install all three before you
send a change to the reader, so the tests run the real binaries. CI does.

## Tests first

Write the failing test, then the code that makes it pass. A bug fix comes
with the test that reproduces the bug. The tests for the tool contract are
in `contract_test.go`, and the reader tests are in
`internal/pdf/reader_test.go`.

## Before a pull request

Run the same checks as CI:

```sh
make check   # gofmt, go vet, govulncheck, go test -race -cover
```

`make snapshot` builds the release binary for your machine with GoReleaser,
and `make docker` builds the container image.

## Commits

- In English, with a conventional prefix: `feat:`, `fix:`, `docs:`, `test:`,
  `build:`, `ci:`, `chore:`, `refactor:`.
- One concern per commit. A formatting change, a dependency bump and a fix
  are three commits.
- The subject says what changes, in the imperative, under 72 characters.
  The body says why, when that is not obvious.

User-facing changes also get a line in the `## [Unreleased]` section of
`CHANGELOG.md`. A release tag `vX.Y.Z` needs its own `## [vX.Y.Z]` section,
because the release notes are taken from it.

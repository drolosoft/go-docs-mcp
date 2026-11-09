# 🚀 Installation

go-docs-mcp is one Go binary. It reads text, Markdown and CSV on its own and
calls three external programs for the rest:

| Program | Needed for |
|---------|------------|
| poppler (`pdftotext`, `pdfinfo`, `pdfimages`, `pdftoppm`) | PDF |
| tesseract | OCR of scanned PDFs and of images |
| pandoc | DOCX, XLSX, PPTX, ODT, EPUB, HTML, RTF, IPYNB |

Install only what you need. What each format can do is in
[formats.md](formats.md). The server looks for these programs in
`/opt/homebrew/bin` and `/usr/local/bin` first, then in `PATH`.

---

## 🍺 Homebrew (macOS and Linux)

```bash
brew install drolosoft/tap/go-docs-mcp
```

The formula installs the release binary and brings poppler, tesseract and
pandoc with it. Nothing else is needed.

---

## 🧩 Claude Code plugin

The plugin registers the server in Claude Code. It does not contain the
binary, so install the binary first (Homebrew above, or `go install` below),
then:

```bash
claude plugin marketplace add drolosoft/claude-plugins
claude plugin install go-docs-mcp@drolosoft-marketplace
```

The plugin serves `~/.docs-mcp/documents`. To serve another folder, register
the server yourself with `claude mcp add`, as shown in the
[README](../README.md#-usage), instead of using the plugin.

---

## 📦 Release binaries

Each [release](https://github.com/drolosoft/go-docs-mcp/releases) has five
archives and a `checksums.txt`:

`<version>` is the release number without the "v", `1.4.1` for instance.

| Archive | System |
|---------|--------|
| `go-docs-mcp_<version>_darwin_arm64.tar.gz` | macOS, Apple silicon |
| `go-docs-mcp_<version>_darwin_amd64.tar.gz` | macOS, Intel |
| `go-docs-mcp_<version>_linux_amd64.tar.gz` | Linux, x86-64 |
| `go-docs-mcp_<version>_linux_arm64.tar.gz` | Linux, ARM64 |
| `go-docs-mcp_<version>_windows_amd64.zip` | Windows, x86-64 |

Each archive holds the `go-docs-mcp` binary (`go-docs-mcp.exe` on Windows),
`LICENSE`, `README.md` and `CHANGELOG.md`. Download the archive and
`checksums.txt` into the same folder and check it before unpacking:

```bash
shasum -a 256 -c --ignore-missing checksums.txt
# go-docs-mcp_<version>_darwin_arm64.tar.gz: OK

tar -xzf go-docs-mcp_<version>_darwin_arm64.tar.gz
sudo mv go-docs-mcp /usr/local/bin/
```

On Linux, `sha256sum -c --ignore-missing checksums.txt` does the same.
Install poppler, tesseract and pandoc separately (see
[External programs](#-external-programs)).

---

## 🐹 go install

Needs Go 1.27.3 or later.

```bash
go install github.com/drolosoft/go-docs-mcp@latest
```

The binary goes to `$(go env GOPATH)/bin`, usually `~/go/bin`. Install the
external programs separately.

To build from a clone instead:

```bash
git clone https://github.com/drolosoft/go-docs-mcp.git
cd go-docs-mcp
make build      # ./go-docs-mcp
make install    # /usr/local/bin/go-docs-mcp
```

---

## 🐳 Docker

From v1.5.0 every release publishes a multi-arch image (`linux/amd64` and
`linux/arm64`) to the GitHub Container Registry, tagged with the version
without the `v` and with `latest`:

```bash
docker run -i --rm -v /path/to/your/documents:/documents:ro ghcr.io/drolosoft/go-docs-mcp:latest
```

`-i` keeps stdin open, which is where MCP talks to the server; `--rm` removes
the container when the client closes it. The image is Alpine with poppler,
tesseract with the English and Spanish language data, and pandoc. It runs as
a non-root user and serves `/documents`. The server never writes into that
folder, so it can be mounted read-only. It reports the release version in
`serverInfo`.

OCR reads English unless the call names a language. To make Spanish the
default, or to read both, set `DOCS_MCP_OCR_LANG`:

```bash
docker run -i --rm -e DOCS_MCP_OCR_LANG=eng+spa \
  -v /path/to/your/documents:/documents:ro ghcr.io/drolosoft/go-docs-mcp:latest
```

Other languages need their tesseract data, which the image does not carry.
Build your own image from it and add the Alpine package, for example
`tesseract-ocr-data-fra` for French. The other settings in
[Configuration](#-configuration) are passed the same way, with `-e`.

To use the image from Claude Code:

```bash
claude mcp add docs -- docker run -i --rm -v /path/to/your/documents:/documents:ro ghcr.io/drolosoft/go-docs-mcp:latest
```

To build it from a clone of the repository instead:

```bash
docker build --build-arg VERSION=<version> -t go-docs-mcp .
```

`<version>` is the number the binary reports, without the `v`; without
the argument it reports `dev`.
`scripts/docker-smoke.sh` builds the image for your machine and checks it over
MCP with a PDF, a DOCX and a PNG mounted read-only.

---

## 💻 Claude Desktop

Claude Desktop starts MCP servers with a short `PATH` that leaves out most of
what your shell has. Two things follow:

- Give `command` the full path of the binary: `which go-docs-mcp` prints it.
- poppler, tesseract and pandoc are found if they are in `/opt/homebrew/bin`
  or `/usr/local/bin`, which is where Homebrew puts them. Homebrew installs
  work with no extra setting.

Add the server to `~/Library/Application Support/Claude/claude_desktop_config.json`
on macOS:

```json
{
  "mcpServers": {
    "docs": {
      "command": "/opt/homebrew/bin/go-docs-mcp",
      "env": {
        "DOCS_MCP_DIR": "/path/to/your/documents"
      }
    }
  }
}
```

If the external programs are in another folder (MacPorts uses
`/opt/local/bin`, for example), add a `PATH` to `env` that includes it. This
value replaces the `PATH` the server gets, so keep the system folders in it:

```json
"env": {
  "DOCS_MCP_DIR": "/path/to/your/documents",
  "PATH": "/opt/local/bin:/usr/bin:/bin"
}
```

Restart Claude Desktop after changing the file.

---

## 🧰 External programs

Without Homebrew, install the programs with the system package manager:

```bash
# macOS
brew install poppler
brew install tesseract        # optional: OCR
brew install pandoc           # optional: Office, EPUB, HTML, RTF, IPYNB

# Debian/Ubuntu
apt install poppler-utils
apt install tesseract-ocr     # optional: OCR
apt install pandoc            # optional: Office, EPUB, HTML, RTF, IPYNB

# Fedora/RHEL
dnf install poppler-utils
dnf install tesseract         # optional: OCR
dnf install pandoc            # optional: Office, EPUB, HTML, RTF, IPYNB
```

Distribution packages of pandoc can lack some readers (Ubuntu's has no `xlsx`
or `pptx`). `list_formats` shows which formats the installed pandoc can read,
and [formats.md](formats.md#-pandoc-formats) explains why. For OCR in another
language, install its tesseract data too (`tesseract --list-langs` lists what
is there).

At start the server writes a warning to stderr when a poppler program or
tesseract is missing, and says whether OCR is on. A missing pandoc shows only
in `list_formats`.

---

## 🔧 Configuration

All settings are environment variables, and all are optional.

| Variable | Default | What it does |
|----------|---------|--------------|
| `DOCS_MCP_DIR` | `~/.docs-mcp/documents/` | The folder the server reads. It is created at start if it does not exist, and a leading `~/` is expanded. When it is not set, the older names `PDF_MCP_DIR` and then `DROLO_DOCS_DIR` are read. |
| `DOCS_MCP_OCR_LANG` | `eng` | The tesseract language when a call names none, for `ocr_document`, `read_image` and the OCR fallback. Lowercase codes such as `spa` or `chi_sim`, joined with `+` to use several (`eng+spa`). |
| `DOCS_MCP_CACHE_MB` | `256` | Size of the in-memory text cache in megabytes. `0` turns it off. The least recently used document is dropped first, and a text larger than the whole cache is not stored. A negative or unreadable value falls back to `256`. |
| `DOCS_MCP_DISABLE_URL` | unset | `1` removes the `read_url` tool, so the server never reaches the network. |
| `DOCS_MCP_URL_PROXY` | unset | `1` lets `read_url` use `HTTP_PROXY` and `HTTPS_PROXY`. Off by default, because behind a proxy the check on the address `read_url` connects to sees the proxy and not the real host; with the proxy on, only the check on the host name remains. |

The two switches accept `1`, `t`, `T`, `true`, `TRUE` and `True` as on (Go's
`strconv.ParseBool`).

#!/usr/bin/env bash
# Cross-check of both MCP protocol eras with a client that is not mcp-go: the
# Python MCP SDK 2.x in its three modes (legacy, 2026-07-28 and auto).
#
#   scripts/crosscheck-python-client/run.sh
#
# It builds the binary from this checkout into a temporary folder, writes the
# two text fixtures and runs crosscheck.py with uv, which fetches the SDK into
# an isolated environment. It needs go and uv, and exits non-zero when any
# mode fails. This check is run by hand before a release; CI does not run it.
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

(cd "$repo_root" && go build -buildvcs=false -o "$work_dir/go-docs-mcp" .)

mkdir "$work_dir/docs"
printf '# Hola\n\nCross-check fixture.\n' > "$work_dir/docs/prueba.md"
printf 'plain text fixture\n' > "$work_dir/docs/notas.txt"

GO_DOCS_MCP_BIN="$work_dir/go-docs-mcp" GO_DOCS_MCP_DOCS="$work_dir/docs" \
  uv run --isolated --with "mcp>=2,<3" python "$repo_root/scripts/crosscheck-python-client/crosscheck.py"

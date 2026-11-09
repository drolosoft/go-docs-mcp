#!/usr/bin/env bash
# Smoke test of the Docker image, run before tagging a release.
#
#   scripts/docker-smoke.sh 1.5.0                    builds the image for this
#                                                    machine, then tests it
#   scripts/docker-smoke.sh 1.5.0 ghcr.io/drolosoft/go-docs-mcp:1.5.0
#                                                    tests an image that is
#                                                    already built or published
#
# It speaks MCP over stdin to the container, with a PDF, a DOCX and a PNG in a
# folder mounted read-only on /documents, the way the README tells users to run
# it. The fixtures are made with the image's own pdftoppm and pandoc, so the
# script only needs docker, jq and a POSIX shell, on macOS or Linux.
set -euo pipefail

version="${1:?usage: scripts/docker-smoke.sh VERSION [IMAGE]}"
image="${2:-}"

# Seconds to wait for one answer. OCR of a page under QEMU is the slow case.
response_timeout=120

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
docs="$work/documents"
mkdir "$docs"

server_pid=""

# cleanup closes the server's stdin, waits for the container to stop and
# removes the temporary folder, also when a check fails half way.
cleanup() {
	exec 3>&- 2>/dev/null || true
	if [ -n "$server_pid" ]; then
		wait "$server_pid" 2>/dev/null || true
	fi
	rm -rf "$work"
}
trap cleanup EXIT

# fail prints the reason, the server's stderr when there is one, and stops.
fail() {
	echo "FAIL: $*" >&2
	if [ -s "$work/err" ]; then
		echo "--- server stderr" >&2
		cat "$work/err" >&2
	fi
	exit 1
}

# pass prints one checked result.
pass() {
	echo "ok   $*"
}

# build_image builds the image for this machine's architecture. It also builds
# once without VERSION, to prove that a plain local build still says "dev".
build_image() {
	image="go-docs-mcp:smoke"
	docker build -q --build-arg "VERSION=$version" -t "$image" "$repo_root" >/dev/null
	docker build -q -t go-docs-mcp:smoke-dev "$repo_root" >/dev/null

	local dev_version
	dev_version="$(initialize_version go-docs-mcp:smoke-dev)"
	[ "$dev_version" = "dev" ] || fail "a build without VERSION reports \"$dev_version\", want \"dev\""
	pass "build without VERSION reports dev"
}

# initialize_version starts a throwaway container, sends initialize and prints
# the version in serverInfo.
#
# @param $1 the image to start
initialize_version() {
	printf '%s\n' "$(initialize_request 0)" |
		docker run --rm -i "$1" 2>/dev/null |
		head -n 1 |
		jq -r '.result.serverInfo.version'
}

# initialize_request prints the JSON-RPC initialize request with the given id.
initialize_request() {
	printf '{"jsonrpc":"2.0","id":%s,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"docker-smoke","version":"1"}}}' "$1"
}

# write_pdf writes a one-page PDF with a title and two Spanish lines. The
# cross-reference table needs the byte offset of every object, so each object
# is appended to the file and its offset is the file size before it. \361 is
# "ñ" in WinAnsiEncoding.
#
# @param $1 the PDF path
write_pdf() {
	local path="$1"
	local content="BT /F1 32 Tf 72 680 Td (go-docs-mcp smoke PDF) Tj 0 -60 Td (El ni\\361o come pi\\361a) Tj 0 -50 Td (en la monta\\361a de Fuencaliente) Tj ET"
	local objects=(
		"<< /Type /Catalog /Pages 2 0 R >>"
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>"
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>"
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>"
		"<< /Length ${#content} >>
stream
$content
endstream"
	)
	local offsets=()
	local index

	printf '%%PDF-1.4\n' >"$path"
	for index in "${!objects[@]}"; do
		offsets+=("$(wc -c <"$path" | tr -d ' ')")
		printf '%d 0 obj\n%s\nendobj\n' "$((index + 1))" "${objects[$index]}" >>"$path"
	done

	local cross_reference
	cross_reference="$(wc -c <"$path" | tr -d ' ')"
	printf 'xref\n0 %d\n0000000000 65535 f \n' "$((${#objects[@]} + 1))" >>"$path"
	for index in "${offsets[@]}"; do
		printf '%010d 00000 n \n' "$index" >>"$path"
	done
	printf 'trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n' "$((${#objects[@]} + 1))" "$cross_reference" >>"$path"
}

# make_fixtures writes smoke.pdf, then renders it to smoke.png with pdftoppm
# and converts a Markdown file to smoke.docx with pandoc, both from the image.
# The image has no fonts, and the PDF names Helvetica without embedding it, so
# the render runs as root to add DejaVu first and hands the PNG back to the
# current user. pandoc runs as the current user so it can write into the
# folder on Linux too, where the image's "docs" user would not own it.
make_fixtures() {
	local owner
	owner="$(id -u):$(id -g)"

	write_pdf "$docs/smoke.pdf"
	printf '# Smoke DOCX\n\nThe DOCX fixture of the go-docs-mcp Docker smoke test.\n' >"$docs/smoke-source.md"

	docker run --rm --user 0 -v "$docs:/work" -w /work --entrypoint sh "$image" -c \
		"apk add --no-cache --quiet font-dejavu && pdftoppm -png -r 150 -singlefile smoke.pdf smoke && chown $owner smoke.png"
	docker run --rm --user "$owner" -v "$docs:/work" -w /work \
		--entrypoint pandoc "$image" -o smoke.docx smoke-source.md
	rm "$docs/smoke-source.md"
}

# start_server runs the container with the documents read-only and keeps its
# stdin open on file descriptor 3, so requests go in one at a time and the
# server does not see end of file before the last answer.
start_server() {
	mkfifo "$work/in"
	docker run --rm -i -v "$docs:/documents:ro" "$image" <"$work/in" >"$work/out" 2>"$work/err" &
	server_pid=$!
	exec 3>"$work/in"
}

# send writes one JSON-RPC message to the server.
send() {
	printf '%s\n' "$1" >&3
}

# await_response waits for the answer with the given id and prints it. The
# server also writes notifications, which have no id and are skipped.
#
# @param $1 the request id
await_response() {
	local id="$1"
	local tries=$((response_timeout * 5))
	local line=""

	while [ "$tries" -gt 0 ]; do
		line="$(grep -m 1 -E "\"id\":${id}[,}]" "$work/out" || true)"
		if [ -n "$line" ]; then
			printf '%s\n' "$line"
			return
		fi
		kill -0 "$server_pid" 2>/dev/null || fail "the server stopped before answering request $id"
		sleep 0.2
		tries=$((tries - 1))
	done
	fail "no answer to request $id after ${response_timeout}s"
}

# call_tool sends tools/call and prints the text of the first content item.
# A tool error stops the smoke.
#
# @param $1 the request id
# @param $2 the tool name
# @param $3 the arguments as a JSON object
call_tool() {
	local response
	send "{\"jsonrpc\":\"2.0\",\"id\":$1,\"method\":\"tools/call\",\"params\":{\"name\":\"$2\",\"arguments\":$3}}"
	response="$(await_response "$1")"
	if [ "$(jq -r '.result.isError // false' <<<"$response")" = "true" ] || [ "$(jq -r 'has("error")' <<<"$response")" = "true" ]; then
		fail "$2 $3 answered an error: $response"
	fi
	jq -r '.result.content[0].text' <<<"$response"
}

# expect_text checks that a tool answer contains a string and prints the
# first line of the answer next to the check.
#
# @param $1 what was checked
# @param $2 the answer
# @param $3 the string it must contain
expect_text() {
	grep -q -F "$3" <<<"$2" || fail "$1: answer has no \"$3\": $2"
	pass "$1: $(tr '\n' ' ' <<<"$2" | cut -c 1-90)"
}

command -v jq >/dev/null || fail "jq is needed to read the answers"

if [ -z "$image" ]; then
	build_image
fi

labels="$(docker inspect --format '{{json .Config.Labels}}' "$image")"
[ "$(jq -r '."io.modelcontextprotocol.server.name"' <<<"$labels")" = "io.github.drolosoft/go-docs-mcp" ] ||
	fail "label io.modelcontextprotocol.server.name is not io.github.drolosoft/go-docs-mcp: $labels"
pass "label io.modelcontextprotocol.server.name"

make_fixtures
start_server

send "$(initialize_request 1)"
reported="$(await_response 1 | jq -r '.result.serverInfo.version')"
[ "$reported" = "$version" ] || fail "serverInfo.version is \"$reported\", want \"$version\""
pass "initialize reports version $reported"
send '{"jsonrpc":"2.0","method":"notifications/initialized"}'

send '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
tool_count="$(await_response 2 | jq '.result.tools | length')"
[ "$tool_count" = "13" ] || fail "tools/list has $tool_count tools, want 13"
pass "tools/list has 13 tools"

expect_text "list_documents" "$(call_tool 3 list_documents '{}')" "smoke.png"
expect_text "read_document smoke.pdf" "$(call_tool 4 read_document '{"filename":"smoke.pdf"}')" "go-docs-mcp smoke PDF"
expect_text "read_document smoke.docx" "$(call_tool 5 read_document '{"filename":"smoke.docx"}')" "Smoke DOCX"
expect_text "read_image smoke.png eng" "$(call_tool 6 read_image '{"filename":"smoke.png","language":"eng"}')" "smoke"
expect_text "read_image smoke.png spa" "$(call_tool 7 read_image '{"filename":"smoke.png","language":"spa"}')" "niño"

# The folder is mounted read-only and already exists, so the server must start
# without trying to create it.
if grep -q -E "cannot create|Created documents directory" "$work/err"; then
	fail "the server tried to create /documents"
fi
pass "read-only /documents: server did not try to create it"

echo "docker smoke passed for $image"

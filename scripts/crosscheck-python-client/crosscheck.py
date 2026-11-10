"""Cross-check go-docs-mcp against the Python MCP SDK 2.x on both protocol eras.

The binary serves the legacy era (an `initialize` handshake, newest revision
2025-11-25) and the stateless 2026-07-28 era from the same process. This
script spawns it over stdio once per client mode and checks that every mode
negotiates the expected revision and sees the same surface: the 13 tools, a
working `list_documents` call and the `docs://` resources of the fixtures.

Usage:
    scripts/crosscheck-python-client/run.sh

run.sh builds the binary, writes the two fixtures and starts this file with
uv. To run it by hand, GO_DOCS_MCP_BIN names the binary and GO_DOCS_MCP_DOCS
a directory holding notas.txt and prueba.md.
"""

import asyncio
import json
import os
import sys
from importlib.metadata import version

from mcp.client import Client
from mcp.client.stdio import StdioServerParameters, stdio_client

# Both come from run.sh; a missing one stops the script with a KeyError that
# names the variable.
BINARY = os.environ["GO_DOCS_MCP_BIN"]
DOCS_DIR = os.environ["GO_DOCS_MCP_DOCS"]

# The revision each client mode must end up speaking. "auto" probes the
# modern era first, so against a dual-era server it must land on 2026-07-28.
EXPECTED_VERSIONS = {
    "legacy": "2025-11-25",
    "2026-07-28": "2026-07-28",
    "auto": "2026-07-28",
}

EXPECTED_TOOLS = {
    "convert_to_markdown",
    "extract_images",
    "extract_tables",
    "get_document_metadata",
    "get_document_outline",
    "get_document_summary",
    "list_documents",
    "list_formats",
    "ocr_document",
    "read_document",
    "read_image",
    "read_url",
    "search_document",
}

# The fixtures the caller puts in the documents directory.
EXPECTED_FILES = {"notas.txt", "prueba.md"}
EXPECTED_RESOURCES = {f"docs://{name}" for name in EXPECTED_FILES}


def spawn_server():
    """Return a stdio transport that starts the binary on the fixtures.

    The environment is kept so the binary still finds pdftotext and pandoc
    on PATH; only the documents directory is pinned.
    """
    env = {**os.environ, "DOCS_MCP_DIR": DOCS_DIR}

    return stdio_client(StdioServerParameters(command=BINARY, env=env))


async def listed_filenames(client):
    """Call list_documents and return the filenames from its JSON text block."""
    result = await client.call_tool("list_documents", {})
    if result.is_error:
        raise AssertionError(f"list_documents returned an error: {result.content}")

    text = next(block.text for block in result.content if block.type == "text")

    documents = json.loads(text)

    return {document["filename"] for document in documents}


async def check_mode(mode):
    """Drive one client mode and return the list of failed checks."""
    failures = []

    async with Client(spawn_server(), mode=mode) as client:
        negotiated = client.protocol_version
        if negotiated != EXPECTED_VERSIONS[mode]:
            failures.append(f"protocol {negotiated}, expected {EXPECTED_VERSIONS[mode]}")

        tools = {tool.name for tool in (await client.list_tools()).tools}
        if tools != EXPECTED_TOOLS:
            missing = sorted(EXPECTED_TOOLS - tools)
            extra = sorted(tools - EXPECTED_TOOLS)
            failures.append(f"tools differ: missing {missing}, extra {extra}")

        files = await listed_filenames(client)
        if not EXPECTED_FILES <= files:
            failures.append(f"list_documents returned {sorted(files)}")

        resources = {str(resource.uri) for resource in (await client.list_resources()).resources}
        if not EXPECTED_RESOURCES <= resources:
            failures.append(f"resources/list returned {sorted(resources)}")

    status = "ok" if not failures else "FAIL"
    print(
        f"mcp {version('mcp')} mode={mode} protocol={negotiated} "
        f"tools={len(tools)} files={sorted(files)} resources={sorted(resources)} {status}"
    )
    for failure in failures:
        print(f"  - {failure}")

    return failures


async def main():
    """Run every mode in turn and exit non-zero if any check failed."""
    failed = False

    for mode in EXPECTED_VERSIONS:
        try:
            failures = await check_mode(mode)
        except Exception as error:
            # The SDK wraps transport errors in task groups; show the cause.
            while isinstance(error, ExceptionGroup) and len(error.exceptions) == 1:
                error = error.exceptions[0]

            print(f"mcp {version('mcp')} mode={mode} FAIL: {type(error).__name__}: {error}")
            failures = [str(error)]

        failed = failed or bool(failures)

    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    asyncio.run(main())

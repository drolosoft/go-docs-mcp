"""MCP server for drolo-docs."""
from __future__ import annotations
import json, logging, sys
from mcp.server import Server
from mcp.server.stdio import stdio_server
from mcp.types import TextContent, Tool
from drolo_docs.pdf_reader import PDFReader

logger = logging.getLogger("drolo-docs")

def create_server() -> Server:
    server = Server("drolo-docs")
    reader = PDFReader()

    @server.list_tools()
    async def list_tools() -> list[Tool]:
        return [
            Tool(name="list_documents", description="List all available PDF documents with filename, title, page count, and file size.",
                 inputSchema={"type": "object", "properties": {}, "required": []}),
            Tool(name="read_document", description="Read the full text of a PDF document, or a specific page.",
                 inputSchema={"type": "object", "properties": {"filename": {"type": "string", "description": "PDF filename"},
                     "page": {"type": "integer", "description": "1-based page number (omit for all)"}}, "required": ["filename"]}),
            Tool(name="search_document", description="Search within a PDF for text matching a query.",
                 inputSchema={"type": "object", "properties": {"filename": {"type": "string", "description": "PDF to search"},
                     "query": {"type": "string", "description": "Search text (case-insensitive)"}}, "required": ["filename", "query"]}),
            Tool(name="get_document_summary", description="Get first 2 pages of a PDF as overview.",
                 inputSchema={"type": "object", "properties": {"filename": {"type": "string", "description": "PDF filename"}}, "required": ["filename"]}),
        ]

    @server.call_tool()
    async def call_tool(name: str, arguments: dict) -> list[TextContent]:
        try:
            if name == "list_documents":
                return [TextContent(type="text", text=json.dumps(reader.list_documents(), indent=2, ensure_ascii=False))]
            elif name == "read_document":
                fn = arguments.get("filename")
                if not fn: raise ValueError("filename is required")
                return [TextContent(type="text", text=reader.read_document(fn, page=arguments.get("page")))]
            elif name == "search_document":
                fn, q = arguments.get("filename"), arguments.get("query")
                if not fn: raise ValueError("filename is required")
                if not q: raise ValueError("query is required")
                r = reader.search_document(fn, q)
                return [TextContent(type="text", text=json.dumps(r, indent=2, ensure_ascii=False) if r else f"No matches for '{q}' in {fn}")]
            elif name == "get_document_summary":
                fn = arguments.get("filename")
                if not fn: raise ValueError("filename is required")
                return [TextContent(type="text", text=reader.get_document_summary(fn))]
            else:
                raise ValueError(f"Unknown tool: {name}")
        except (FileNotFoundError, ValueError, RuntimeError) as exc:
            return [TextContent(type="text", text=f"Error: {exc}")]
        except Exception as exc:
            logger.exception("Unexpected error in tool %s", name)
            return [TextContent(type="text", text=f"Internal error: {exc}")]
    return server

async def run_server() -> None:
    server = create_server()
    async with stdio_server() as (read_stream, write_stream):
        await server.run(read_stream, write_stream, server.create_initialization_options())

def main() -> None:
    import asyncio
    if "--help" in sys.argv or "-h" in sys.argv:
        print("drolo-docs: MCP server for PDF document access\n")
        print("Usage: python -m drolo_docs.server\n       drolo-docs\n")
        print("Environment variables:\n  DROLO_DOCS_DIR  (default: ~/.drolo/documents/)\n")
        print("Runs as stdio MCP server for Claude Code / Drolo integration.")
        sys.exit(0)
    logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(name)s] %(levelname)s: %(message)s", stream=sys.stderr)
    asyncio.run(run_server())

if __name__ == "__main__":
    main()

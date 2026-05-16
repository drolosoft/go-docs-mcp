# drolo-docs

Go MCP server for PDF document access. Serves PDFs from ~/.drolo/documents/.

## Quick Start
make build
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | ./drolo-docs

## Install
make install

## Tools
- list_documents — list available PDFs
- read_document — read full or page text
- search_document — search within a document
- get_document_summary — first 3 pages overview

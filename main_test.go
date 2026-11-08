package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/drolosoft/go-docs-mcp/internal/pdf"
	"github.com/mark3labs/mcp-go/server"
)

// listedTool is the part of a tools/list entry these tests look at, decoded
// from the JSON a client receives so the wire names are checked too.
type listedTool struct {
	Name        string `json:"name"`
	Annotations struct {
		Title           string `json:"title"`
		ReadOnlyHint    *bool  `json:"readOnlyHint"`
		DestructiveHint *bool  `json:"destructiveHint"`
		IdempotentHint  *bool  `json:"idempotentHint"`
		OpenWorldHint   *bool  `json:"openWorldHint"`
	} `json:"annotations"`
}

// expectedTitles is the human title every tool must advertise.
var expectedTitles = map[string]string{
	"list_documents":        "List Documents",
	"read_document":         "Read Document",
	"search_document":       "Search Document",
	"get_document_summary":  "Get Document Summary",
	"get_document_metadata": "Get Document Metadata",
	"extract_images":        "Extract Images",
	"read_url":              "Read URL",
	"ocr_document":          "OCR Document",
	"list_formats":          "List Formats",
	"read_image":            "Read Image",
	"get_document_outline":  "Get Document Outline",
	"extract_tables":        "Extract Tables",
	"convert_to_markdown":   "Convert to Markdown",
}

// newTestServer builds the server the way main does, over an empty documents
// directory.
func newTestServer(t *testing.T) (*server.MCPServer, string) {
	t.Helper()

	docsDir := t.TempDir()

	return newServer(pdf.NewReader(docsDir), docsDir), docsDir
}

// handle sends one JSON-RPC message to the server and returns the response
// as JSON.
func handle(t *testing.T, mcpServer *server.MCPServer, message string) []byte {
	t.Helper()

	response := mcpServer.HandleMessage(context.Background(), json.RawMessage(message))

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	return data
}

// listTools calls tools/list and decodes the tools it returns.
func listTools(t *testing.T, mcpServer *server.MCPServer) []listedTool {
	t.Helper()

	data := handle(t, mcpServer, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	var envelope struct {
		Result struct {
			Tools []listedTool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode tools/list: %v\n%s", err, data)
	}

	return envelope.Result.Tools
}

// hintIs reports whether an optional hint is present and has the wanted value.
func hintIs(hint *bool, want bool) bool {
	return hint != nil && *hint == want
}

func TestToolsListAdvertisesReadOnlyHints(t *testing.T) {
	mcpServer, _ := newTestServer(t)

	tools := listTools(t, mcpServer)

	if len(tools) != len(expectedTitles) {
		t.Fatalf("tools/list returned %d tools, want %d", len(tools), len(expectedTitles))
	}

	for _, tool := range tools {
		want, known := expectedTitles[tool.Name]
		if !known {
			t.Errorf("unexpected tool %q", tool.Name)
			continue
		}

		if tool.Annotations.Title != want {
			t.Errorf("%s: title = %q, want %q", tool.Name, tool.Annotations.Title, want)
		}
		if !hintIs(tool.Annotations.ReadOnlyHint, true) {
			t.Errorf("%s: readOnlyHint is not true", tool.Name)
		}
		if !hintIs(tool.Annotations.DestructiveHint, false) {
			t.Errorf("%s: destructiveHint is not false", tool.Name)
		}
		if !hintIs(tool.Annotations.IdempotentHint, true) {
			t.Errorf("%s: idempotentHint is not true", tool.Name)
		}

		// Only read_url leaves the machine: it downloads from the network.
		wantOpenWorld := tool.Name == "read_url"
		if !hintIs(tool.Annotations.OpenWorldHint, wantOpenWorld) {
			t.Errorf("%s: openWorldHint is not %v", tool.Name, wantOpenWorld)
		}
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/drolosoft/go-docs-mcp/internal/pdf"
	"github.com/mark3labs/mcp-go/server"
)

// toolResult is the part of a tools/call response these tests look at,
// decoded from the JSON a client receives.
type toolResult struct {
	Content []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Data     string `json:"data"`
		MIMEType string `json:"mimeType"`
	} `json:"content"`
	StructuredContent *struct {
		Text       string `json:"text"`
		NextOffset *int   `json:"next_offset"`
		TotalChars int    `json:"total_chars"`
	} `json:"structuredContent"`
	IsError bool `json:"isError"`
}

// callToolMayFail sends a tools/call for name with arguments and decodes
// the result. Unlike callTool it returns error results too, since these
// tests check that bad arguments give one.
func callToolMayFail(t *testing.T, mcpServer *server.MCPServer, name string, arguments map[string]any) toolResult {
	t.Helper()

	params, err := json.Marshal(map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}

	data := handle(t, mcpServer, fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":%s}`, params))

	var envelope struct {
		Result toolResult `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode tools/call: %v\n%.500s", err, data)
	}

	return envelope.Result
}

func TestReadDocument_PagesALongTextByDefault(t *testing.T) {
	mcpServer, docsDir := newTestServer(t)
	text := strings.Repeat("é", pdf.DefaultMaxChars+1234)
	writeTestFile(t, docsDir, "long.txt", text)

	first := callToolMayFail(t, mcpServer, "read_document", map[string]any{"filename": "long.txt"})

	if first.IsError {
		t.Fatalf("read_document failed: %+v", first.Content)
	}
	wantTrailer := fmt.Sprintf("\n\n[truncated: showing chars 0-%d of %d; call again with offset=%d]", pdf.DefaultMaxChars, pdf.DefaultMaxChars+1234, pdf.DefaultMaxChars)
	if got := first.Content[0].Text; got != strings.Repeat("é", pdf.DefaultMaxChars)+wantTrailer {
		t.Errorf("first page text ends with %q, want the trailer %q", got[len(got)-80:], wantTrailer)
	}

	structured := first.StructuredContent
	if structured == nil || structured.NextOffset == nil || *structured.NextOffset != pdf.DefaultMaxChars || structured.TotalChars != pdf.DefaultMaxChars+1234 {
		t.Fatalf("structured content = %+v, want next_offset %d and total_chars %d", structured, pdf.DefaultMaxChars, pdf.DefaultMaxChars+1234)
	}

	rest := callToolMayFail(t, mcpServer, "read_document", map[string]any{"filename": "long.txt", "offset": *structured.NextOffset})

	if rest.Content[0].Text != strings.Repeat("é", 1234) {
		t.Errorf("second page has %d bytes, want the last 1234 characters and no trailer", len(rest.Content[0].Text))
	}
	if rest.StructuredContent == nil || rest.StructuredContent.NextOffset != nil {
		t.Errorf("the last page must not carry a next_offset, got %+v", rest.StructuredContent)
	}
}

func TestReadDocument_MaxCharsAndBadOffsets(t *testing.T) {
	mcpServer, docsDir := newTestServer(t)
	writeTestFile(t, docsDir, "notes.md", "0123456789")

	page := callToolMayFail(t, mcpServer, "read_document", map[string]any{"filename": "notes.md", "offset": 2, "max_chars": 3})

	if !strings.HasPrefix(page.Content[0].Text, "234\n\n[truncated: showing chars 2-5 of 10; call again with offset=5]") {
		t.Errorf("offset 2, max_chars 3 gave %q", page.Content[0].Text)
	}

	for _, arguments := range []map[string]any{
		{"filename": "notes.md", "offset": 11},
		{"filename": "notes.md", "offset": -1},
		{"filename": "notes.md", "max_chars": 0},
	} {
		if result := callToolMayFail(t, mcpServer, "read_document", arguments); !result.IsError {
			t.Errorf("read_document %v: want an error result, got %+v", arguments, result.Content)
		}
	}
}

func TestConvertToMarkdown_IsPaged(t *testing.T) {
	mcpServer, docsDir := newTestServer(t)
	writeTestFile(t, docsDir, "notes.md", "# Title\n\n"+strings.Repeat("word ", 100))

	result := callToolMayFail(t, mcpServer, "convert_to_markdown", map[string]any{"filename": "notes.md", "max_chars": 7})

	if !strings.HasPrefix(result.Content[0].Text, "# Title\n\n[truncated: showing chars 0-7 of 509; call again with offset=7]") {
		t.Errorf("convert_to_markdown max_chars 7 gave %q", result.Content[0].Text)
	}
	if result.StructuredContent == nil || result.StructuredContent.Text != "# Title" {
		t.Errorf("structured text = %+v, want the slice without the trailer", result.StructuredContent)
	}
}

func TestPagedTools_AdvertiseOffsetAndMaxChars(t *testing.T) {
	mcpServer, _ := newTestServer(t)

	data := handle(t, mcpServer, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	var envelope struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"inputSchema"`
				OutputSchema *struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"outputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}

	wanted := map[string][]string{
		"read_document":       {"offset", "max_chars"},
		"convert_to_markdown": {"offset", "max_chars"},
		"ocr_document":        {"offset", "max_chars"},
		"search_document":     {"max_results"},
	}

	for _, tool := range envelope.Result.Tools {
		arguments, ok := wanted[tool.Name]
		if !ok {
			continue
		}
		delete(wanted, tool.Name)

		for _, argument := range arguments {
			if _, ok := tool.InputSchema.Properties[argument]; !ok {
				t.Errorf("%s does not advertise %s", tool.Name, argument)
			}
		}

		if arguments[0] == "offset" {
			if tool.OutputSchema == nil || tool.OutputSchema.Properties["next_offset"] == nil || tool.OutputSchema.Properties["total_chars"] == nil {
				t.Errorf("%s does not advertise the text page output schema", tool.Name)
			}
		}
	}

	for name := range wanted {
		t.Errorf("tool %s was not listed", name)
	}
}

func TestSearchDocument_HonoursMaxResults(t *testing.T) {
	mcpServer, docsDir := newTestServer(t)
	writeTestFile(t, docsDir, "log.txt", strings.Repeat("needle\n\n\n\n\n\n", 80))

	byDefault := callToolMayFail(t, mcpServer, "search_document", map[string]any{"filename": "log.txt", "query": "needle"})

	if count := strings.Count(byDefault.Content[0].Text, "--- Match "); count != pdf.DefaultSearchResults {
		t.Errorf("default search rendered %d matches, want %d", count, pdf.DefaultSearchResults)
	}

	narrowed := callToolMayFail(t, mcpServer, "search_document", map[string]any{"filename": "log.txt", "query": "needle", "max_results": 3})

	if count := strings.Count(narrowed.Content[0].Text, "--- Match "); count != 3 {
		t.Errorf("max_results 3 rendered %d matches", count)
	}
}

func TestPagedTools_StructuredContentMatchesTheOutputSchema(t *testing.T) {
	docsDir := t.TempDir()
	writeTestFile(t, docsDir, "notes.md", "# Notes\n\n"+strings.Repeat("text ", 40))
	mcpServer := newServer(pdf.NewReader(docsDir), docsDir, server.WithOutputSchemaValidation())

	// A truncated page and a last page differ in next_offset, so both must
	// pass the validator.
	for _, arguments := range []map[string]any{
		{"filename": "notes.md", "max_chars": 10},
		{"filename": "notes.md", "offset": 10},
	} {
		for _, tool := range []string{"read_document", "convert_to_markdown"} {
			arguments["filename"] = "notes.md"

			if result := callToolMayFail(t, mcpServer, tool, arguments); result.IsError || result.StructuredContent == nil {
				t.Errorf("%s %v failed the output schema: %+v", tool, arguments, result.Content)
			}
		}
	}
}

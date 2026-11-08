package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drolosoft/go-docs-mcp/internal/pdf"
	"github.com/mark3labs/mcp-go/server"
)

// listedTool is the part of a tools/list entry these tests look at, decoded
// from the JSON a client receives so the wire names are checked too.
type listedTool struct {
	Name         string          `json:"name"`
	OutputSchema json.RawMessage `json:"outputSchema"`
	Annotations  struct {
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

func TestReadURLCanBeDisabled(t *testing.T) {
	t.Setenv("DOCS_MCP_DISABLE_URL", "1")

	mcpServer, _ := newTestServer(t)
	tools := listTools(t, mcpServer)

	if len(tools) != len(expectedTitles)-1 {
		t.Fatalf("tools/list returned %d tools, want %d", len(tools), len(expectedTitles)-1)
	}
	for _, tool := range tools {
		if tool.Name == "read_url" {
			t.Fatal("read_url is registered although DOCS_MCP_DISABLE_URL=1")
		}
	}
}

func TestRejectPrivateHost(t *testing.T) {
	cases := []struct {
		host    string
		private bool
	}{
		{"localhost", true},
		{"LocalHost", true},
		{"api.localhost", true},
		{"127.0.0.1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.1.10", true},
		{"169.254.169.254", true},
		{"100.100.100.100", true},
		{"0.0.0.0", true},
		{"::1", true},
		{"::", true},
		{"fd00::1", true},
		{"fe80::1", true},
		{"::ffff:127.0.0.1", true},
		{"93.184.216.34", false},
		{"2606:2800:220:1:248:1893:25c8:1946", false},
	}

	for _, c := range cases {
		err := rejectPrivateHost(context.Background(), c.host)

		if got := errors.Is(err, errPrivateAddress); got != c.private {
			t.Errorf("%s: rejected = %v (err %v), want %v", c.host, got, err, c.private)
		}
	}
}

func TestRejectPrivateDial(t *testing.T) {
	if err := rejectPrivateDial("tcp", "127.0.0.1:80", nil); !errors.Is(err, errPrivateAddress) {
		t.Errorf("loopback dial: err = %v, want errPrivateAddress", err)
	}
	if err := rejectPrivateDial("tcp6", "[fe80::1%en0]:443", nil); !errors.Is(err, errPrivateAddress) {
		t.Errorf("link-local dial: err = %v, want errPrivateAddress", err)
	}
	if err := rejectPrivateDial("tcp", "93.184.216.34:443", nil); err != nil {
		t.Errorf("public dial: err = %v, want nil", err)
	}
}

// allowAnyHost lets a test fetcher reach httptest servers on loopback.
func allowAnyHost(context.Context, string) error {
	return nil
}

func TestDownloadRejectsNonHTTPScheme(t *testing.T) {
	fetcher := newURLFetcher(time.Second)

	_, err := fetcher.download(context.Background(), "file:///etc/passwd")
	if err == nil {
		t.Fatal("file:// URL was accepted")
	}
}

func TestDownloadRejectsLoopbackTarget(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		writer.Write([]byte("internal"))
	}))
	defer target.Close()

	fetcher := newURLFetcher(time.Second)

	_, err := fetcher.download(context.Background(), target.URL)
	if !errors.Is(err, errPrivateAddress) {
		t.Fatalf("err = %v, want errPrivateAddress", err)
	}
}

func TestDownloadRejectsRedirectToLoopback(t *testing.T) {
	var internalHits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		internalHits.Add(1)
		writer.Header().Set("Content-Type", "text/plain")
		writer.Write([]byte("metadata secrets"))
	}))
	defer internal.Close()

	public := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, internal.URL+"/latest/meta-data", http.StatusFound)
	}))
	defer public.Close()

	// The first check stands in for a public host; every later one is the
	// production guard, so the redirect hop meets the real rule.
	var checks atomic.Int32
	checkHost := func(ctx context.Context, host string) error {
		if checks.Add(1) == 1 {
			return nil
		}
		return rejectPrivateHost(ctx, host)
	}
	fetcher := newGuardedFetcher(time.Second, checkHost, nil)

	_, err := fetcher.download(context.Background(), public.URL)

	if !errors.Is(err, errPrivateAddress) {
		t.Fatalf("err = %v, want errPrivateAddress", err)
	}
	if internalHits.Load() != 0 {
		t.Fatalf("the redirect target was contacted %d times", internalHits.Load())
	}
}

func TestDownloadSavesPlainTextAsTxt(t *testing.T) {
	const body = "plain text document\nsecond line\n"
	site := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.Write([]byte(body))
	}))
	defer site.Close()

	fetcher := newGuardedFetcher(time.Second, allowAnyHost, nil)

	path, err := fetcher.download(context.Background(), site.URL+"/notes")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer os.Remove(path)

	if filepath.Ext(path) != ".txt" {
		t.Errorf("saved as %s, want a .txt file", filepath.Base(path))
	}

	text, err := readDownloaded(context.Background(), pdf.NewReader(t.TempDir()), path, "")
	if err != nil {
		t.Fatalf("readDownloaded: %v", err)
	}
	if text != body {
		t.Errorf("text = %q, want %q", text, body)
	}

	if _, err := readDownloaded(context.Background(), pdf.NewReader(t.TempDir()), path, "1-2"); err == nil {
		t.Error("page ranges on a text download were accepted")
	}
}

func TestDownloadTimesOut(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer slow.Close()

	fetcher := newGuardedFetcher(100*time.Millisecond, allowAnyHost, nil)

	started := time.Now()
	_, err := fetcher.download(context.Background(), slow.URL)
	elapsed := time.Since(started)

	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("download took %v, the timeout did not apply", elapsed)
	}
}

func TestExtensionForContentType(t *testing.T) {
	cases := []struct {
		contentType string
		extension   string
		accepted    bool
	}{
		{"", ".pdf", true},
		{"application/pdf", ".pdf", true},
		{"application/octet-stream", ".pdf", true},
		{"text/plain; charset=utf-8", ".txt", true},
		{"text/markdown", ".md", true},
		{"text/csv", ".csv", true},
		{"text/html", ".txt", true},
		{"image/png", "", false},
		{"application/json", "", false},
	}

	for _, c := range cases {
		extension, err := extensionForContentType(c.contentType)

		if (err == nil) != c.accepted {
			t.Errorf("%q: err = %v, want accepted %v", c.contentType, err, c.accepted)
		}
		if extension != c.extension {
			t.Errorf("%q: extension = %q, want %q", c.contentType, extension, c.extension)
		}
	}
}

func TestDownloadRejectsPrivateAddressAtDial(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer target.Close()

	// The host check lets everything through, as a rebinding name would pass
	// it; only the dial hook stands between the request and loopback.
	fetcher := newGuardedFetcher(time.Second, allowAnyHost, rejectPrivateDial)

	_, err := fetcher.download(context.Background(), target.URL)

	if !errors.Is(err, errPrivateAddress) {
		t.Fatalf("err = %v, want errPrivateAddress", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("the server was contacted %d times", hits.Load())
	}
}

func TestToolErrorsHideAbsolutePaths(t *testing.T) {
	// A documents directory that does not exist makes list_documents fail
	// with the reader's message, which names the directory in full.
	docsDir := filepath.Join(t.TempDir(), "missing-documents")
	mcpServer := newServer(pdf.NewReader(docsDir), docsDir)

	data := handle(t, mcpServer, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_documents","arguments":{}}}`)

	if !strings.Contains(string(data), `"isError":true`) {
		t.Fatalf("list_documents did not fail: %s", data)
	}
	if strings.Contains(string(data), docsDir) {
		t.Fatalf("the error reveals the documents directory: %s", data)
	}
	if !strings.Contains(string(data), "missing-documents") {
		t.Fatalf("the error lost the directory's name: %s", data)
	}
}

func TestRedactPaths(t *testing.T) {
	docsDir := "/Users/someone/.docs-mcp/documents"
	tempDir := "/var/folders/xy/T/"

	cases := []struct {
		message string
		want    string
	}{
		{
			"cannot read documents directory /Users/someone/.docs-mcp/documents: open /Users/someone/.docs-mcp/documents: no such file or directory",
			"cannot read documents directory documents: open documents: no such file or directory",
		},
		{
			"failed to read file: open /Users/someone/.docs-mcp/documents/report.pdf: permission denied",
			"failed to read file: open report.pdf: permission denied",
		},
		{
			"file not found: /var/folders/xy/T/go-docs-mcp-123.pdf",
			"file not found: go-docs-mcp-123.pdf",
		},
		{
			`failed to download document: Get "https://example.com/a.pdf": timeout`,
			`failed to download document: Get "https://example.com/a.pdf": timeout`,
		},
	}

	for _, c := range cases {
		if got := redactPaths(c.message, docsDir, tempDir); got != c.want {
			t.Errorf("redactPaths(%q)\n got %q\nwant %q", c.message, got, c.want)
		}
	}
}

// structuredTools are the tools whose answer is JSON, so they also return it
// as structuredContent and describe its shape in an outputSchema.
var structuredTools = []string{
	"list_documents",
	"list_formats",
	"get_document_metadata",
	"get_document_outline",
	"extract_tables",
}

func TestStructuredToolsAdvertiseOutputSchema(t *testing.T) {
	mcpServer, _ := newTestServer(t)

	tools := listTools(t, mcpServer)

	for _, tool := range tools {
		wantSchema := slices.Contains(structuredTools, tool.Name)

		if !wantSchema {
			if len(tool.OutputSchema) > 0 {
				t.Errorf("%s: unexpected outputSchema %s", tool.Name, tool.OutputSchema)
			}
			continue
		}

		var schema struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(tool.OutputSchema, &schema); err != nil {
			t.Errorf("%s: outputSchema does not decode: %v (%s)", tool.Name, err, tool.OutputSchema)
			continue
		}

		// MCP requires structuredContent to be a JSON object, so the schema
		// must describe one even for the tools whose text answer is a list.
		if schema.Type != "object" || len(schema.Properties) == 0 {
			t.Errorf("%s: outputSchema is not an object with properties: %s", tool.Name, tool.OutputSchema)
		}
	}
}

// callResult is the part of a tools/call result the structured output tests
// look at.
type callResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

// callTool sends one tools/call with the given arguments, as JSON, and
// decodes its result.
func callTool(t *testing.T, mcpServer *server.MCPServer, name, arguments string) callResult {
	t.Helper()

	message := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"` + name + `","arguments":` + arguments + `}}`
	data := handle(t, mcpServer, message)

	var envelope struct {
		Result callResult `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode tools/call %s: %v\n%s", name, err, data)
	}

	if envelope.Result.IsError || len(envelope.Result.Content) == 0 {
		t.Fatalf("%s failed: %s", name, data)
	}

	return envelope.Result
}

// newStructuredServer builds a server that checks every structuredContent
// against its tool's outputSchema, so a result that drifts from the schema
// fails the call, over a directory with one document of each text kind.
func newStructuredServer(t *testing.T) (*server.MCPServer, string) {
	t.Helper()

	docsDir := t.TempDir()
	writeTestFile(t, docsDir, "notes.md", "# Notes\n\n## First part\n\nSome text.\n")
	writeTestFile(t, docsDir, "data.csv", "name,size\nalpha,1\nbeta,2\n")

	mcpServer := newServer(pdf.NewReader(docsDir), docsDir, server.WithOutputSchemaValidation())

	return mcpServer, docsDir
}

// writeTestFile writes content to name inside dir and fails the test when it
// cannot.
func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// indentedJSON returns value the way the tools have always printed it, the
// text an old client keeps reading.
func indentedJSON(t *testing.T, value any) string {
	t.Helper()

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return string(data)
}

func TestListDocumentsReturnsStructuredContent(t *testing.T) {
	mcpServer, docsDir := newStructuredServer(t)

	result := callTool(t, mcpServer, "list_documents", `{}`)

	var structured struct {
		Documents []pdf.DocumentInfo `json:"documents"`
	}
	if err := json.Unmarshal(result.StructuredContent, &structured); err != nil {
		t.Fatalf("structuredContent: %v (%s)", err, result.StructuredContent)
	}
	if len(structured.Documents) != 2 {
		t.Fatalf("structuredContent lists %d documents, want 2: %s", len(structured.Documents), result.StructuredContent)
	}

	// The text block stays the bare array the tool printed before, so a
	// client that ignores structuredContent sees no change.
	documents, err := pdf.NewReader(docsDir).ListDocuments(context.Background())
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if want := indentedJSON(t, documents); result.Content[0].Text != want {
		t.Errorf("text = %s, want %s", result.Content[0].Text, want)
	}
}

func TestListFormatsReturnsStructuredContent(t *testing.T) {
	mcpServer, docsDir := newStructuredServer(t)

	result := callTool(t, mcpServer, "list_formats", `{}`)

	formats := pdf.NewReader(docsDir).ListFormats()
	want := indentedJSON(t, map[string]any{"formats": formats})

	if result.Content[0].Text != want {
		t.Errorf("text = %s, want %s", result.Content[0].Text, want)
	}

	var structured struct {
		Formats []pdf.FormatInfo `json:"formats"`
	}
	if err := json.Unmarshal(result.StructuredContent, &structured); err != nil {
		t.Fatalf("structuredContent: %v (%s)", err, result.StructuredContent)
	}
	if !slices.Equal(structured.Formats, formats) {
		t.Errorf("structured formats = %+v, want %+v", structured.Formats, formats)
	}
}

func TestGetDocumentOutlineReturnsStructuredContent(t *testing.T) {
	mcpServer, _ := newStructuredServer(t)

	result := callTool(t, mcpServer, "get_document_outline", `{"filename":"notes.md"}`)

	var structured struct {
		Outline []pdf.OutlineEntry `json:"outline"`
	}
	if err := json.Unmarshal(result.StructuredContent, &structured); err != nil {
		t.Fatalf("structuredContent: %v (%s)", err, result.StructuredContent)
	}
	if len(structured.Outline) != 2 || structured.Outline[1].Title != "First part" {
		t.Fatalf("structured outline = %+v", structured.Outline)
	}

	if want := indentedJSON(t, structured.Outline); result.Content[0].Text != want {
		t.Errorf("text = %s, want %s", result.Content[0].Text, want)
	}
}

func TestExtractTablesReturnsStructuredContent(t *testing.T) {
	mcpServer, _ := newStructuredServer(t)

	result := callTool(t, mcpServer, "extract_tables", `{"filename":"data.csv"}`)

	var structured struct {
		Tables []pdf.TableData `json:"tables"`
	}
	if err := json.Unmarshal(result.StructuredContent, &structured); err != nil {
		t.Fatalf("structuredContent: %v (%s)", err, result.StructuredContent)
	}
	if len(structured.Tables) != 1 || len(structured.Tables[0].Rows) != 3 {
		t.Fatalf("structured tables = %+v", structured.Tables)
	}

	if want := indentedJSON(t, structured.Tables); result.Content[0].Text != want {
		t.Errorf("text = %s, want %s", result.Content[0].Text, want)
	}
}

func TestEmptyResultsKeepTheirText(t *testing.T) {
	mcpServer, docsDir := newStructuredServer(t)
	writeTestFile(t, docsDir, "plain.txt", "no headings and no columns here\n")

	outline := callTool(t, mcpServer, "get_document_outline", `{"filename":"plain.txt"}`)
	tables := callTool(t, mcpServer, "extract_tables", `{"filename":"plain.txt"}`)

	// An empty answer is still "[]" in the text and an empty list, never a
	// null, in the structured content.
	if outline.Content[0].Text != "[]" || string(outline.StructuredContent) != `{"outline":[]}` {
		t.Errorf("empty outline: text %q, structured %s", outline.Content[0].Text, outline.StructuredContent)
	}
	if tables.Content[0].Text != "[]" || string(tables.StructuredContent) != `{"tables":[]}` {
		t.Errorf("empty tables: text %q, structured %s", tables.Content[0].Text, tables.StructuredContent)
	}
}

func TestGetDocumentMetadataReturnsStructuredContent(t *testing.T) {
	mcpServer, docsDir := newStructuredServer(t)
	if err := pdf.NewReader(docsDir).CheckDependencies(); err != nil {
		t.Skipf("poppler is not installed: %v", err)
	}
	writeTestFile(t, docsDir, "fixture.pdf", string(onePagePDF("Contract fixture", "HELLO WORLD")))

	result := callTool(t, mcpServer, "get_document_metadata", `{"filename":"fixture.pdf"}`)

	var structured pdf.DocumentMetadata
	if err := json.Unmarshal(result.StructuredContent, &structured); err != nil {
		t.Fatalf("structuredContent: %v (%s)", err, result.StructuredContent)
	}
	if structured.Title != "Contract fixture" || structured.Pages != 1 {
		t.Fatalf("structured metadata = %+v", structured)
	}

	if want := indentedJSON(t, structured); result.Content[0].Text != want {
		t.Errorf("text = %s, want %s", result.Content[0].Text, want)
	}
}

// onePagePDF builds the smallest valid PDF that poppler and tesseract can
// read: one Letter page with text in 48pt Helvetica and title in its
// document information. The cross-reference table needs the byte offset of
// every object, so they are measured while the file is written.
func onePagePDF(title, text string) []byte {
	content := fmt.Sprintf("BT /F1 48 Tf 72 600 Td (%s) Tj ET", text)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		fmt.Sprintf("<< /Title (%s) >>", title),
	}

	var document bytes.Buffer
	document.WriteString("%PDF-1.4\n")

	offsets := make([]int, len(objects))
	for index, object := range objects {
		offsets[index] = document.Len()
		fmt.Fprintf(&document, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}

	crossReference := document.Len()
	fmt.Fprintf(&document, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&document, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&document, "trailer\n<< /Size %d /Root 1 0 R /Info 6 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, crossReference)

	return document.Bytes()
}

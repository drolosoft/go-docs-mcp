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
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drolosoft/go-docs-mcp/internal/pdf"
	"github.com/mark3labs/mcp-go/mcp"
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
// as structuredContent and describe its shape in an outputSchema, and the
// tools that page a document's text, whose page goes out the same way.
var structuredTools = []string{
	"list_documents",
	"list_formats",
	"get_document_metadata",
	"get_document_outline",
	"extract_tables",
	"read_document",
	"convert_to_markdown",
	"ocr_document",
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

// listedResource is the part of a resources/list entry these tests look at.
type listedResource struct {
	URI      string `json:"uri"`
	Name     string `json:"name"`
	MIMEType string `json:"mimeType"`
}

// listResources calls resources/list and decodes the resources it returns.
func listResources(t *testing.T, mcpServer *server.MCPServer) []listedResource {
	t.Helper()

	data := handle(t, mcpServer, `{"jsonrpc":"2.0","id":4,"method":"resources/list"}`)

	var envelope struct {
		Result struct {
			Resources []listedResource `json:"resources"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode resources/list: %v\n%s", err, data)
	}

	return envelope.Result.Resources
}

// readResource calls resources/read on uri and returns the raw response, so
// a test can look at the contents or at the error.
func readResource(t *testing.T, mcpServer *server.MCPServer, uri string) []byte {
	t.Helper()

	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "resources/read",
		"params":  map[string]any{"uri": uri},
	})
	if err != nil {
		t.Fatalf("marshal resources/read: %v", err)
	}

	return handle(t, mcpServer, string(request))
}

// readResourceText reads uri and returns the text and MIME type of its only
// content, failing the test when the read does not succeed.
func readResourceText(t *testing.T, mcpServer *server.MCPServer, uri string) (string, string) {
	t.Helper()

	data := readResource(t, mcpServer, uri)

	var envelope struct {
		Result struct {
			Contents []struct {
				URI      string `json:"uri"`
				MIMEType string `json:"mimeType"`
				Text     string `json:"text"`
			} `json:"contents"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode resources/read: %v\n%s", err, data)
	}
	if len(envelope.Result.Contents) != 1 {
		t.Fatalf("resources/read %s: want one content, got %s", uri, data)
	}

	content := envelope.Result.Contents[0]
	if content.URI != uri {
		t.Errorf("content uri = %q, want %q", content.URI, uri)
	}

	return content.Text, content.MIMEType
}

func TestResourcesListEnumeratesDocuments(t *testing.T) {
	mcpServer, docsDir := newStructuredServer(t)

	resources := listResources(t, mcpServer)

	want := []listedResource{
		{URI: "docs://data.csv", Name: "data.csv", MIMEType: "text/csv"},
		{URI: "docs://notes.md", Name: "notes.md", MIMEType: "text/markdown"},
	}
	if !slices.Equal(resources, want) {
		t.Fatalf("resources/list =\n  %+v\nwant\n  %+v", resources, want)
	}

	// The list is read from the directory on every call, so a document
	// dropped in after the server started can be mentioned right away.
	writeTestFile(t, docsDir, "my report.txt", "added later\n")

	resources = listResources(t, mcpServer)

	added := listedResource{URI: "docs://my%20report.txt", Name: "my report.txt", MIMEType: "text/plain"}
	if !slices.Contains(resources, added) {
		t.Fatalf("resources/list after adding a file = %+v, want it to contain %+v", resources, added)
	}
}

func TestResourceTemplateIsAdvertised(t *testing.T) {
	mcpServer, _ := newTestServer(t)

	data := handle(t, mcpServer, `{"jsonrpc":"2.0","id":6,"method":"resources/templates/list"}`)

	if !strings.Contains(string(data), `"uriTemplate":"docs://{filename}"`) {
		t.Fatalf("resources/templates/list has no docs://{filename}: %s", data)
	}
}

func TestReadResourceReturnsDocumentText(t *testing.T) {
	mcpServer, docsDir := newStructuredServer(t)
	writeTestFile(t, docsDir, "my report.txt", "spaces in the name\n")

	text, mimeType := readResourceText(t, mcpServer, "docs://notes.md")
	if text != "# Notes\n\n## First part\n\nSome text.\n" || mimeType != "text/markdown" {
		t.Errorf("docs://notes.md = %q (%s)", text, mimeType)
	}

	text, mimeType = readResourceText(t, mcpServer, "docs://my%20report.txt")
	if text != "spaces in the name\n" || mimeType != "text/plain" {
		t.Errorf("docs://my%%20report.txt = %q (%s)", text, mimeType)
	}
}

func TestReadResourceOutsideTheDirectoryFails(t *testing.T) {
	parentDir := t.TempDir()
	docsDir := filepath.Join(parentDir, "documents")
	if err := os.Mkdir(docsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeTestFile(t, parentDir, "secret.txt", "outside the documents directory\n")

	mcpServer := newServer(pdf.NewReader(docsDir), docsDir)

	for _, uri := range []string{
		"docs://../secret.txt",
		"docs://..%2Fsecret.txt",
		"docs://" + url.PathEscape(filepath.Join(parentDir, "secret.txt")),
		"docs://missing.txt",
	} {
		data := readResource(t, mcpServer, uri)

		if !strings.Contains(string(data), `"error"`) {
			t.Errorf("%s was read: %s", uri, data)
		}
		if strings.Contains(string(data), "outside the documents directory") {
			t.Errorf("%s leaked the file: %s", uri, data)
		}
		if strings.Contains(string(data), parentDir) {
			t.Errorf("%s error reveals the disk path: %s", uri, data)
		}
	}
}

func TestInitializeCarriesInstructions(t *testing.T) {
	mcpServer, _ := newTestServer(t)

	data := handle(t, mcpServer, `{"jsonrpc":"2.0","id":7,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)

	var envelope struct {
		Result struct {
			Instructions string `json:"instructions"`
			Capabilities struct {
				Resources *struct{} `json:"resources"`
			} `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode initialize: %v\n%s", err, data)
	}

	if envelope.Result.Capabilities.Resources == nil {
		t.Errorf("initialize does not declare resources: %s", data)
	}

	// The instructions name the tools in the order a model should reach for
	// them, so each one must appear after the one before.
	instructions := envelope.Result.Instructions
	previous := -1
	for _, name := range []string{"list_documents", "get_document_outline", "search_document", "read_document"} {
		position := strings.Index(instructions, name)
		if position <= previous {
			t.Fatalf("instructions do not name %s in order: %q", name, instructions)
		}
		previous = position
	}
}

// recordingSession is a client session that keeps the notifications the
// server sends it, so a test can read the progress of a tool call.
type recordingSession struct {
	notifications chan mcp.JSONRPCNotification
}

// newRecordingSession returns a session with room for more notifications
// than one call sends, so the server never finds the channel full.
func newRecordingSession() *recordingSession {
	return &recordingSession{notifications: make(chan mcp.JSONRPCNotification, 16)}
}

// Initialize does nothing: the session is ready from the start.
func (session *recordingSession) Initialize() {}

// Initialized reports true, because the server only notifies initialized
// sessions.
func (session *recordingSession) Initialized() bool {
	return true
}

// NotificationChannel is where the server writes the notifications.
func (session *recordingSession) NotificationChannel() chan<- mcp.JSONRPCNotification {
	return session.notifications
}

// SessionID names the session.
func (session *recordingSession) SessionID() string {
	return "recording-session"
}

// progressUpdate is the part of a notifications/progress these tests check.
type progressUpdate struct {
	Token    any
	Progress float64
	Total    float64
	Message  string
}

// receivedProgress drains the progress notifications the session got.
func (session *recordingSession) receivedProgress(t *testing.T) []progressUpdate {
	t.Helper()

	var updates []progressUpdate
	for {
		select {
		case notification := <-session.notifications:
			if notification.Method != string(mcp.MethodNotificationProgress) {
				t.Errorf("unexpected notification %s", notification.Method)
				continue
			}

			fields := notification.Params.AdditionalFields
			update := progressUpdate{Token: fields["progressToken"]}
			update.Progress, _ = fields["progress"].(float64)
			update.Total, _ = fields["total"].(float64)
			update.Message, _ = fields["message"].(string)
			updates = append(updates, update)
		default:
			return updates
		}
	}
}

func TestProgressNotifierSendsToTheCaller(t *testing.T) {
	mcpServer, _ := newTestServer(t)
	session := newRecordingSession()
	ctx := mcpServer.WithContext(context.Background(), session)

	notifier := progressNotifier{server: mcpServer, token: "ocr-1"}
	notifier.notify(ctx, 0, 3, "starting OCR of 3 pages")
	notifier.notify(ctx, 3, 0, "OCR done")

	// A total of 0 means unknown, and the notification leaves it out.
	want := []progressUpdate{
		{Token: "ocr-1", Progress: 0, Total: 3, Message: "starting OCR of 3 pages"},
		{Token: "ocr-1", Progress: 3, Total: 0, Message: "OCR done"},
	}
	if got := session.receivedProgress(t); !slices.Equal(got, want) {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}
}

func TestProgressNotifierNeedsATokenAndAServer(t *testing.T) {
	mcpServer, _ := newTestServer(t)
	session := newRecordingSession()
	ctx := mcpServer.WithContext(context.Background(), session)

	// A request without _meta.progressToken asked for no progress, and a
	// context without a server has nobody to send it through.
	withoutToken := newProgressNotifier(ctx, mcp.CallToolRequest{})
	withoutToken.notify(ctx, 0, 1, "starting OCR of 1 page")

	request := mcp.CallToolRequest{}
	request.Params.Meta = &mcp.Meta{ProgressToken: "ocr-2"}
	withoutServer := newProgressNotifier(context.Background(), request)
	withoutServer.notify(ctx, 0, 1, "starting OCR of 1 page")

	if got := session.receivedProgress(t); len(got) != 0 {
		t.Fatalf("progress = %+v, want none", got)
	}
}

// callOCRWithSession runs ocr_document on filename through HandleMessage,
// as the stdio transport does, with session as the caller. A non-nil token
// is sent as _meta.progressToken.
func callOCRWithSession(t *testing.T, mcpServer *server.MCPServer, session *recordingSession, filename string, token any) string {
	t.Helper()

	params := map[string]any{
		"name":      "ocr_document",
		"arguments": map[string]any{"filename": filename},
	}
	if token != nil {
		params["_meta"] = map[string]any{"progressToken": token}
	}

	message, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 8, "method": "tools/call", "params": params})
	if err != nil {
		t.Fatalf("marshal tools/call: %v", err)
	}

	ctx := mcpServer.WithContext(context.Background(), session)

	response, err := json.Marshal(mcpServer.HandleMessage(ctx, message))
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	return string(response)
}

func TestOCRDocumentReportsProgress(t *testing.T) {
	docsDir := t.TempDir()
	reader := pdf.NewReader(docsDir)
	if !reader.HasOCR() || reader.CheckDependencies() != nil {
		t.Skip("OCR needs tesseract and poppler")
	}
	writeTestFile(t, docsDir, "scan.pdf", string(onePagePDF("Scan", "HELLO WORLD")))

	mcpServer := newServer(reader, docsDir)
	session := newRecordingSession()

	response := callOCRWithSession(t, mcpServer, session, "scan.pdf", "ocr-3")
	if !strings.Contains(response, "HELLO") {
		t.Fatalf("ocr_document did not read the page: %s", response)
	}

	want := []progressUpdate{
		{Token: "ocr-3", Progress: 0, Total: 1, Message: "starting OCR of 1 page"},
		{Token: "ocr-3", Progress: 1, Total: 1, Message: "OCR done"},
	}
	if got := session.receivedProgress(t); !slices.Equal(got, want) {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}

	// The same call without a token asks for no progress and gets none.
	response = callOCRWithSession(t, mcpServer, session, "scan.pdf", nil)
	if !strings.Contains(response, "HELLO") {
		t.Fatalf("ocr_document without a token did not read the page: %s", response)
	}
	if got := session.receivedProgress(t); len(got) != 0 {
		t.Fatalf("progress without a token = %+v, want none", got)
	}
}

func TestDownloadThroughProxyWhenAllowed(t *testing.T) {
	// A forward proxy for plain http: it receives the absolute URL the
	// client wants and answers in its place, recording what was asked.
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "docs.example.invalid" {
			t.Errorf("proxy asked for %s, want docs.example.invalid", r.URL.Host)
		}
		proxied.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "through the proxy")
	}))
	defer proxy.Close()

	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")

	// The proxy listens on loopback, which the dial check would refuse: the
	// opt-in has to drop that check or no proxy could ever work.
	fetcher := newFetcherWithProxy(time.Second, allowAnyHost, rejectPrivateDial, true)

	path, err := fetcher.download(context.Background(), "http://docs.example.invalid/notes.txt")
	if err != nil {
		t.Fatalf("download through the proxy failed: %v", err)
	}
	defer os.Remove(path)

	if proxied.Load() != 1 {
		t.Fatalf("the proxy was used %d times, want 1", proxied.Load())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "through the proxy" {
		t.Fatalf("downloaded %q, %v", data, err)
	}
}

func TestProxyIsIgnoredByDefault(t *testing.T) {
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
	}))
	defer proxy.Close()

	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("DOCS_MCP_URL_PROXY", "")

	// Without the opt-in the request must not go to the proxy. The target
	// does not resolve, so the download fails, but the proxy stays untouched.
	fetcher := newGuardedFetcher(time.Second, allowAnyHost, rejectPrivateDial)
	if _, err := fetcher.download(context.Background(), "http://docs.example.invalid/notes.txt"); err == nil {
		t.Fatal("download of an unresolvable host succeeded")
	}
	if proxied.Load() != 0 {
		t.Fatalf("the proxy was contacted %d times without DOCS_MCP_URL_PROXY", proxied.Load())
	}
}

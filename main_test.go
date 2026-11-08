package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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

	text, err := readDownloaded(pdf.NewReader(t.TempDir()), path, "")
	if err != nil {
		t.Fatalf("readDownloaded: %v", err)
	}
	if text != body {
		t.Errorf("text = %q, want %q", text, body)
	}

	if _, err := readDownloaded(pdf.NewReader(t.TempDir()), path, "1-2"); err == nil {
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

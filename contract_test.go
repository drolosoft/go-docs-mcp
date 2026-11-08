package main

// Contract tests: the real binary, over stdio, on both MCP protocol eras.
//
// MCP 2026-07-28 removed the initialize handshake; earlier clients (Claude
// Desktop, Cowork, Claude Code as of mid-2026) still open with `initialize`.
// A server must serve both from one process ("dual-era"). These tests build
// the actual binary, spawn it exactly as an MCP client would, and drive it
// three ways: forcing the legacy handshake, letting the client negotiate
// (auto), and pinning the modern revision. Every mode must see the same 13
// tools, by name, and complete real tool calls.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// expectedToolNames is the public tool surface, sorted. Comparing names and
// not just a count catches a tool that was renamed or swapped for another,
// which a count of 13 would let through.
var expectedToolNames = []string{
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

// The binary is built with -buildvcs=false, so its module version is
// "(devel)" and serverVersion() reports "dev". Any other value means the
// serverInfo no longer comes from version.go.
const expectedServerVersion = "dev"

var (
	// binPath is the server binary built once in TestMain for every test.
	binPath string

	// docsDir is the fixture directory the spawned server serves.
	docsDir string
)

// TestMain builds the server binary and the fixture directory once, runs
// the tests and removes both afterwards.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "go-docs-mcp-contract-*")
	if err != nil {
		panic(err)
	}

	binPath = filepath.Join(tmp, "go-docs-mcp")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binPath, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("go build failed: " + err.Error())
	}

	docsDir = filepath.Join(tmp, "docs")
	if err := writeFixtures(docsDir); err != nil {
		panic(err)
	}

	code := m.Run()

	// os.Exit skips deferred calls, so the cleanup has to run before it.
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

// writeFixtures creates dir with the two text documents the tests list and
// convert. Text formats need no poppler, so the contract runs on any machine.
func writeFixtures(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create fixture dir: %w", err)
	}

	fixtures := map[string]string{
		"prueba.md": "# Hola\n\nDual-era contract fixture.\n",
		"notas.txt": "plain text fixture\n",
	}
	for name, content := range fixtures {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return fmt.Errorf("write fixture %s: %w", name, err)
		}
	}

	return nil
}

// connect spawns the binary, initializes a client with the given options and
// returns it with the initialize result. In the modern era the client builds
// that result from server/discover, so both eras expose the same serverInfo.
func connect(t *testing.T, opts ...client.ClientOption) (*client.Client, *mcp.InitializeResult) {
	t.Helper()

	stdio := transport.NewStdio(binPath, []string{"DOCS_MCP_DIR=" + docsDir})
	mcpClient := client.NewClient(stdio, opts...)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = mcpClient.Close() })

	request := mcp.InitializeRequest{}
	request.Params.ClientInfo = mcp.Implementation{Name: "contract-test", Version: "1.0"}
	initResult, err := mcpClient.Initialize(ctx, request)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	return mcpClient, initResult
}

// exerciseServer checks the negotiated version and the server identity, the
// exact tool surface, and three real calls: a listing, a conversion and a
// path traversal that must fail as a tool error.
func exerciseServer(t *testing.T, mcpClient *client.Client, initResult *mcp.InitializeResult, wantVersion string) {
	t.Helper()

	ctx := context.Background()

	if got := mcpClient.ProtocolVersion(); got != wantVersion {
		t.Fatalf("negotiated protocol version = %q, want %q", got, wantVersion)
	}

	if initResult.ServerInfo.Name != "go-docs-mcp" {
		t.Errorf("serverInfo.name = %q, want %q", initResult.ServerInfo.Name, "go-docs-mcp")
	}
	if initResult.ServerInfo.Version != expectedServerVersion {
		t.Errorf("serverInfo.version = %q, want %q", initResult.ServerInfo.Version, expectedServerVersion)
	}

	tools, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}

	toolNames := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		toolNames = append(toolNames, tool.Name)
	}
	slices.Sort(toolNames)
	if !slices.Equal(toolNames, expectedToolNames) {
		t.Fatalf("tools/list names =\n  %v\nwant\n  %v", toolNames, expectedToolNames)
	}

	// list_documents must see both fixtures.
	listRequest := mcp.CallToolRequest{}
	listRequest.Params.Name = "list_documents"
	result, err := mcpClient.CallTool(ctx, listRequest)
	if err != nil {
		t.Fatalf("list_documents: %v", err)
	}
	listing := firstText(t, result)
	if !strings.Contains(listing, "prueba.md") || !strings.Contains(listing, "notas.txt") {
		t.Fatalf("list_documents did not list fixtures: %s", listing)
	}

	// convert_to_markdown reads a text-format file end to end.
	convertRequest := mcp.CallToolRequest{}
	convertRequest.Params.Name = "convert_to_markdown"
	convertRequest.Params.Arguments = map[string]any{"filename": "prueba.md"}
	result, err = mcpClient.CallTool(ctx, convertRequest)
	if err != nil {
		t.Fatalf("convert_to_markdown: %v", err)
	}
	if result.IsError || !strings.Contains(firstText(t, result), "Dual-era contract fixture") {
		t.Fatalf("convert_to_markdown unexpected result: %+v", result)
	}

	// A bad path must come back as a tool error the model can read, not as a
	// transport failure that drops the connection.
	traversalRequest := mcp.CallToolRequest{}
	traversalRequest.Params.Name = "read_document"
	traversalRequest.Params.Arguments = map[string]any{"filename": "../escape.txt"}
	result, err = mcpClient.CallTool(ctx, traversalRequest)
	if err != nil {
		t.Fatalf("read_document (bad path) transport error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("read_document with traversal path should be a tool error, got %+v", result)
	}
}

// firstText returns the first text block of a tool result and fails the test
// when there is none.
func firstText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()

	for _, content := range result.Content {
		if text, ok := content.(mcp.TextContent); ok {
			return text.Text
		}
	}

	t.Fatalf("no text content in result: %+v", result)
	return ""
}

// TestLegacyHandshakeClient drives the server the way today's desktop
// clients do: initialize first, newest revision that still has it.
func TestLegacyHandshakeClient(t *testing.T) {
	mcpClient, initResult := connect(t, client.WithLegacyProtocolOnly())
	exerciseServer(t, mcpClient, initResult, mcp.LATEST_LEGACY_PROTOCOL_VERSION)
}

// TestModernPinnedClient pins 2026-07-28, so the client never falls back to
// the legacy handshake.
func TestModernPinnedClient(t *testing.T) {
	mcpClient, initResult := connect(t, client.WithProtocolVersion(mcp.ProtocolVersion20260728))
	exerciseServer(t, mcpClient, initResult, mcp.ProtocolVersion20260728)
}

// TestAutoNegotiatingClient lets the client choose; a dual-era server must
// end up on the newest revision.
func TestAutoNegotiatingClient(t *testing.T) {
	mcpClient, initResult := connect(t)
	exerciseServer(t, mcpClient, initResult, mcp.LATEST_PROTOCOL_VERSION)
}

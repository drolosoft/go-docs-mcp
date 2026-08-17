package main

// Contract tests: the real binary, over stdio, on both MCP protocol eras.
//
// MCP 2026-07-28 removed the initialize handshake; earlier clients (Claude
// Desktop, Cowork, Claude Code as of mid-2026) still open with `initialize`.
// A server must serve both from one process ("dual-era"). These tests build
// the actual binary, spawn it exactly as an MCP client would, and drive it
// three ways: forcing the legacy handshake, letting the client negotiate
// (auto), and pinning the modern revision. Every mode must see the same 13
// tools and complete real tool calls.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

const expectedToolCount = 13

var (
	binPath string
	docsDir string
)

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "go-docs-mcp-contract-*")
	if err != nil {
		panic(err)
	}
	binPath = filepath.Join(tmp, "go-docs-mcp")
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("go build failed: " + err.Error())
	}
	docsDir = filepath.Join(tmp, "docs")
	_ = os.MkdirAll(docsDir, 0o755)
	_ = os.WriteFile(filepath.Join(docsDir, "prueba.md"), []byte("# Hola\n\nDual-era contract fixture.\n"), 0o644)
	_ = os.WriteFile(filepath.Join(docsDir, "notas.txt"), []byte("plain text fixture\n"), 0o644)

	code := m.Run()
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

// connect spawns the binary and initializes a client with the given options.
func connect(t *testing.T, opts ...client.ClientOption) *client.Client {
	t.Helper()
	tr := transport.NewStdio(binPath, []string{"DOCS_MCP_DIR=" + docsDir}, nil...)
	c := client.NewClient(tr, opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	req := mcp.InitializeRequest{}
	req.Params.ClientInfo = mcp.Implementation{Name: "contract-test", Version: "1.0"}
	if _, err := c.Initialize(ctx, req); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return c
}

func exerciseServer(t *testing.T, c *client.Client, wantVersion string) {
	t.Helper()
	ctx := context.Background()

	if got := c.ProtocolVersion(); got != wantVersion {
		t.Fatalf("negotiated protocol version = %q, want %q", got, wantVersion)
	}

	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(tools.Tools) != expectedToolCount {
		t.Fatalf("tools/list returned %d tools, want %d", len(tools.Tools), expectedToolCount)
	}

	// list_documents must see the fixtures
	list := mcp.CallToolRequest{}
	list.Params.Name = "list_documents"
	res, err := c.CallTool(ctx, list)
	if err != nil {
		t.Fatalf("list_documents: %v", err)
	}
	text := firstText(t, res)
	if !strings.Contains(text, "prueba.md") || !strings.Contains(text, "notas.txt") {
		t.Fatalf("list_documents did not list fixtures: %s", text)
	}

	// convert_to_markdown reads a text-format file end to end
	conv := mcp.CallToolRequest{}
	conv.Params.Name = "convert_to_markdown"
	conv.Params.Arguments = map[string]any{"filename": "prueba.md"}
	res, err = c.CallTool(ctx, conv)
	if err != nil {
		t.Fatalf("convert_to_markdown: %v", err)
	}
	if res.IsError || !strings.Contains(firstText(t, res), "Dual-era contract fixture") {
		t.Fatalf("convert_to_markdown unexpected result: %+v", res)
	}

	// an error path must come back as a tool error, not a transport failure
	bad := mcp.CallToolRequest{}
	bad.Params.Name = "read_document"
	bad.Params.Arguments = map[string]any{"filename": "../escape.txt"}
	res, err = c.CallTool(ctx, bad)
	if err != nil {
		t.Fatalf("read_document (bad path) transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("read_document with traversal path should be a tool error, got %+v", res)
	}
}

func firstText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatalf("no text content in result: %+v", res)
	return ""
}

func TestLegacyHandshakeClient(t *testing.T) {
	c := connect(t, client.WithLegacyProtocolOnly())
	exerciseServer(t, c, mcp.LATEST_LEGACY_PROTOCOL_VERSION)
}

func TestModernPinnedClient(t *testing.T) {
	c := connect(t, client.WithProtocolVersion(mcp.ProtocolVersion20260728))
	exerciseServer(t, c, mcp.ProtocolVersion20260728)
}

func TestAutoNegotiatingClient(t *testing.T) {
	c := connect(t)
	exerciseServer(t, c, mcp.LATEST_PROTOCOL_VERSION)
}

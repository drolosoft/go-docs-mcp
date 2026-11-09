package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// bundleTool is one entry of the "tools" array in mcpb/manifest.json.
type bundleTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// TestMCPBManifestMatchesToolsList compares the tools the bundle manifest
// announces with the tools the server lists, names and descriptions both.
// Claude Desktop shows the manifest's list before the server ever runs, so a
// tool added or reworded without go generate would show the wrong thing.
func TestMCPBManifestMatchesToolsList(t *testing.T) {
	// The switch would remove read_url from the server but not from the
	// manifest, which always announces the full set.
	t.Setenv("DOCS_MCP_DISABLE_URL", "")

	data, err := os.ReadFile(filepath.Join("mcpb", "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var bundle struct {
		Tools []bundleTool `json:"tools"`
	}
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	mcpServer, _ := newTestServer(t)
	served := serverToolDescriptions(t, handle(t, mcpServer, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))

	// The generator writes the tools sorted by name, so the comparison sorts
	// too and does not depend on the order tools/list uses.
	sortByName(served)
	if !slices.Equal(bundle.Tools, served) {
		t.Errorf("mcpb/manifest.json tools differ from tools/list; run go generate\nmanifest: %v\nserver:   %v", toolNamesOf(bundle.Tools), toolNamesOf(served))
		for _, tool := range served {
			index := slices.IndexFunc(bundle.Tools, func(candidate bundleTool) bool { return candidate.Name == tool.Name })
			if index >= 0 && bundle.Tools[index].Description != tool.Description {
				t.Errorf("%s: the manifest has an old description", tool.Name)
			}
		}
	}

	if len(served) != len(expectedToolNames) {
		t.Errorf("tools/list returned %d tools, want %d", len(served), len(expectedToolNames))
	}
}

// serverToolDescriptions decodes the name and description of every tool in
// a tools/list response.
func serverToolDescriptions(t *testing.T, response []byte) []bundleTool {
	t.Helper()

	var envelope struct {
		Result struct {
			Tools []bundleTool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		t.Fatalf("decode tools/list: %v\n%s", err, response)
	}

	return envelope.Result.Tools
}

// sortByName sorts tools by name in place.
func sortByName(tools []bundleTool) {
	slices.SortFunc(tools, func(first, second bundleTool) int {
		return strings.Compare(first.Name, second.Name)
	})
}

// toolNamesOf returns the names of tools, in their order.
func toolNamesOf(tools []bundleTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}

	return names
}

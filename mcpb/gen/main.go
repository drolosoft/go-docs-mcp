// Command gen writes the tools of mcpb/manifest.json from the server's own
// tools/list, so the list Claude Desktop shows before installing the bundle
// is the list the server serves. It builds the server, starts it over stdio
// the way an MCP client does and replaces the "tools" array of the manifest
// with the name and description of every tool. Everything else in the
// manifest is written by hand and kept as it is.
//
// Run it from the repository root with go generate (the directive is in
// mcpb.go), after adding a tool or changing a description. TestMCPBManifest
// fails until the manifest and the server agree again.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// startTimeout is the time the server has to start and answer initialize
// and tools/list. The build that comes before it has no limit.
const startTimeout = 20 * time.Second

// manifestTool is one entry of the manifest's "tools" array. The MCPB spec
// allows only a name and a description there.
type manifestTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// manifest holds every field of mcpb/manifest.json in the order it is
// written. The fields written by hand stay raw JSON, so the generator does
// not reorder or reformat what is inside them. A field missing here makes
// the generator fail instead of dropping it, because decoding refuses
// unknown fields.
type manifest struct {
	ManifestVersion json.RawMessage `json:"manifest_version"`
	Name            json.RawMessage `json:"name"`
	DisplayName     json.RawMessage `json:"display_name,omitempty"`
	Version         json.RawMessage `json:"version"`
	Description     json.RawMessage `json:"description"`
	LongDescription json.RawMessage `json:"long_description,omitempty"`
	Author          json.RawMessage `json:"author"`
	Repository      json.RawMessage `json:"repository,omitempty"`
	Homepage        json.RawMessage `json:"homepage,omitempty"`
	Documentation   json.RawMessage `json:"documentation,omitempty"`
	Support         json.RawMessage `json:"support,omitempty"`
	Icon            json.RawMessage `json:"icon,omitempty"`
	Server          json.RawMessage `json:"server"`
	Tools           []manifestTool  `json:"tools"`
	Keywords        json.RawMessage `json:"keywords,omitempty"`
	License         json.RawMessage `json:"license,omitempty"`
	Compatibility   json.RawMessage `json:"compatibility,omitempty"`
	UserConfig      json.RawMessage `json:"user_config,omitempty"`
}

func main() {
	manifestPath := flag.String("manifest", filepath.Join("mcpb", "manifest.json"), "manifest to update")
	flag.Parse()

	if err := run(*manifestPath); err != nil {
		log.Fatalf("gen: %v", err)
	}
}

// run builds the server, asks it for its tools and writes them into the
// manifest at manifestPath.
func run(manifestPath string) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}

	current, err := decodeManifest(data)
	if err != nil {
		return fmt.Errorf("%s: %w", manifestPath, err)
	}

	workDir, err := os.MkdirTemp("", "go-docs-mcp-gen-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	binaryPath, err := buildServer(workDir)
	if err != nil {
		return err
	}

	tools, err := serverTools(binaryPath, workDir)
	if err != nil {
		return err
	}
	current.Tools = tools

	output, err := encodeManifest(current)
	if err != nil {
		return err
	}

	return os.WriteFile(manifestPath, output, 0o644)
}

// decodeManifest reads the manifest and refuses a field the manifest type
// does not name, so that a field added by hand is never lost on the next run.
func decodeManifest(data []byte) (manifest, error) {
	var decoded manifest

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return manifest{}, fmt.Errorf("decode manifest (add a new field to the manifest type in mcpb/gen): %w", err)
	}

	return decoded, nil
}

// encodeManifest writes the manifest with two spaces of indent. HTML
// escaping is off because tool descriptions quote characters such as < and >
// that would otherwise turn into < in a file people read.
func encodeManifest(value manifest) ([]byte, error) {
	var output bytes.Buffer

	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}

	return output.Bytes(), nil
}

// buildServer compiles the server in the current directory, which go
// generate sets to the repository root, into workDir.
func buildServer(workDir string) (string, error) {
	binaryPath := filepath.Join(workDir, "go-docs-mcp")

	build := exec.Command("go", "build", "-buildvcs=false", "-o", binaryPath, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return "", fmt.Errorf("go build: %w", err)
	}

	return binaryPath, nil
}

// serverTools starts the binary over stdio and returns its tools sorted by
// name. DOCS_MCP_DISABLE_URL is set to false on purpose: a developer with the
// switch on in their shell would otherwise write a manifest without read_url.
func serverTools(binaryPath, workDir string) ([]manifestTool, error) {
	docsDir := filepath.Join(workDir, "documents")
	if err := os.Mkdir(docsDir, 0o755); err != nil {
		return nil, err
	}

	environment := []string{"DOCS_MCP_DIR=" + docsDir, "DOCS_MCP_DISABLE_URL=false"}
	stdio := transport.NewStdio(binaryPath, environment)
	mcpClient := client.NewClient(stdio)

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	if err := mcpClient.Start(ctx); err != nil {
		return nil, fmt.Errorf("start server: %w", err)
	}
	defer mcpClient.Close()

	request := mcp.InitializeRequest{}
	request.Params.ClientInfo = mcp.Implementation{Name: "go-docs-mcp-gen", Version: "1.0"}
	if _, err := mcpClient.Initialize(ctx, request); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}

	listed, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}

	tools := make([]manifestTool, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		tools = append(tools, manifestTool{Name: tool.Name, Description: tool.Description})
	}

	// The order of tools/list is the SDK's business; sorting by name keeps
	// the manifest stable when that order changes.
	slices.SortFunc(tools, func(first, second manifestTool) int {
		return strings.Compare(first.Name, second.Name)
	})

	return tools, nil
}

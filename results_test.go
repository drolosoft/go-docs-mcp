package main

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
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
		"extract_images":      {"max_images"},
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

func TestServer_RefusesAResultOverTheSizeLimit(t *testing.T) {
	mcpServer, docsDir := newTestServer(t)

	// Every line matches and is long, so the search result is several times
	// the limit even with the match cap: only the server's size check can
	// stop it.
	line := strings.Repeat("needle ", 4000)
	writeTestFile(t, docsDir, "huge.txt", strings.Repeat(line+"\n", 80))

	result := callToolMayFail(t, mcpServer, "search_document", map[string]any{"filename": "huge.txt", "query": "needle", "max_results": pdf.MaxSearchResultsLimit})

	if !result.IsError || !strings.Contains(result.Content[0].Text, "tool result exceeds") {
		t.Errorf("an oversized result must be replaced by an error, got isError=%v and %d content items", result.IsError, len(result.Content))
	}
}

// writeMaskedImagePDF writes a one-page PDF with a width by height RGB
// gradient that carries a soft mask, which pdfimages extracts as a second,
// grey image.
func writeMaskedImagePDF(t *testing.T, path string, width, height int) {
	t.Helper()

	pixels := make([]byte, width*height*3)
	alpha := make([]byte, width*height)
	for index := range alpha {
		x, y := index%width, index/width
		pixels[index*3] = byte(x)
		pixels[index*3+1] = byte(y)
		alpha[index] = byte(x + y)
	}

	content := "q 400 0 0 200 100 100 cm /Im1 Do Q"
	imageStream := deflate(t, pixels)
	maskStream := deflate(t, alpha)

	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /SMask 6 0 R /Length %d >>\nstream\n%s\nendstream", width, height, len(imageStream), imageStream),
		fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream", width, height, len(maskStream), maskStream),
	}

	// The cross-reference table needs the byte offset of every object.
	var document strings.Builder
	document.WriteString("%PDF-1.4\n")
	var offsets []int
	for index, object := range objects {
		offsets = append(offsets, document.Len())
		fmt.Fprintf(&document, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}

	xrefOffset := document.Len()
	fmt.Fprintf(&document, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&document, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&document, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefOffset)

	if err := os.WriteFile(path, []byte(document.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// deflate compresses data with zlib, the PDF FlateDecode filter.
func deflate(t *testing.T, data []byte) []byte {
	t.Helper()

	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("deflate: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("deflate: %v", err)
	}

	return compressed.Bytes()
}

func TestExtractImages_ReturnsPNGImageContentWithoutTheMask(t *testing.T) {
	mcpServer, docsDir := newTestServer(t)
	if err := pdf.NewReader(docsDir).CheckDependencies(); err != nil {
		t.Skipf("poppler is not installed: %v", err)
	}
	writeMaskedImagePDF(t, filepath.Join(docsDir, "figure.pdf"), 2048, 1024)

	result := callToolMayFail(t, mcpServer, "extract_images", map[string]any{"filename": "figure.pdf"})

	if result.IsError {
		t.Fatalf("extract_images failed: %+v", result.Content)
	}
	if len(result.Content) != 2 {
		t.Fatalf("got %d content items, want the description and one image: the soft mask must not come back as a second image", len(result.Content))
	}

	description, picture := result.Content[0], result.Content[1]
	if description.Type != "text" || !strings.Contains(description.Text, `"found": 1`) {
		t.Errorf("first item should describe the extraction, got %s: %.300s", description.Type, description.Text)
	}
	if picture.Type != "image" || picture.MIMEType != "image/png" {
		t.Fatalf("second item is %s %s, want image image/png", picture.Type, picture.MIMEType)
	}
	if len(picture.Data) > pdf.ImageBudgetBytes {
		t.Errorf("image takes %d bytes, over the %d budget", len(picture.Data), pdf.ImageBudgetBytes)
	}

	data, err := base64.StdEncoding.DecodeString(picture.Data)
	if err != nil {
		t.Fatalf("image data is not base64: %v", err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("image data is not a PNG: %v", err)
	}
	if config.Width != pdf.MaxImageSide || config.Height != pdf.MaxImageSide/2 {
		t.Errorf("PNG is %dx%d, want %dx%d", config.Width, config.Height, pdf.MaxImageSide, pdf.MaxImageSide/2)
	}
}

func TestExtractImages_PagePastTheEndIsAnError(t *testing.T) {
	mcpServer, docsDir := newTestServer(t)
	writeMaskedImagePDF(t, filepath.Join(docsDir, "figure.pdf"), 64, 32)

	result := callToolMayFail(t, mcpServer, "extract_images", map[string]any{"filename": "figure.pdf", "page": 2})

	if !result.IsError {
		t.Errorf("page 2 of a one-page PDF must be an error result, got %+v", result.Content)
	}
}

// TestListDocuments_EmptyDirectoryPrintsAnEmptyList checks the text block of
// list_documents when there is nothing to list. A client that reads only the
// text parses it as JSON and iterates it, so it must be "[]" like the
// structured list, never "null".
func TestListDocuments_EmptyDirectoryPrintsAnEmptyList(t *testing.T) {
	mcpServer, _ := newTestServer(t)

	result := callToolMayFail(t, mcpServer, "list_documents", map[string]any{})

	if result.IsError {
		t.Fatalf("list_documents failed: %+v", result.Content)
	}
	if got := result.Content[0].Text; got != "[]" {
		t.Errorf("text block of an empty directory = %q, want %q", got, "[]")
	}
}

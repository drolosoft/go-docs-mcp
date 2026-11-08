package pdf

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestReader creates a Reader pointing at a temp directory with test files.
func newTestReader(t *testing.T) (*Reader, string) {
	t.Helper()
	dir := t.TempDir()
	r := NewReader(dir)
	return r, dir
}

// writeFile is a helper to create a file in the temp directory.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", name, err)
	}
}

// ---------------------------------------------------------------------------
// 1. sanitizeFilename
// ---------------------------------------------------------------------------

func TestSanitizeFilename_ValidNames(t *testing.T) {
	r, _ := newTestReader(t)

	valid := []string{
		"test.pdf",
		"my doc.txt",
		"notes.md",
		"data.csv",
		"scan.docx",
		"photo.png",
		"image.jpg",
		"pic.jpeg",
		"scan.tiff",
		"scan.tif",
		"bitmap.bmp",
	}

	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			got, err := r.sanitizeFilename(name)
			if err != nil {
				t.Errorf("sanitizeFilename(%q) returned error: %v", name, err)
			}
			if got != name {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", name, got, name)
			}
		})
	}
}

func TestSanitizeFilename_Invalid(t *testing.T) {
	r, _ := newTestReader(t)

	invalid := []struct {
		name string
		desc string
	}{
		{"../etc/passwd", "directory traversal with .."},
		{"/absolute/path.pdf", "absolute path"},
		{"file.exe", "unsupported extension .exe"},
		{"file.sh", "unsupported extension .sh"},
		{"subdir/file.pdf", "subdirectory path"},
		{"file.zip", "unsupported extension .zip"},
		{"noextension", "no extension at all"},
	}

	for _, tc := range invalid {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := r.sanitizeFilename(tc.name)
			if err == nil {
				t.Errorf("sanitizeFilename(%q) expected error for %s, got nil", tc.name, tc.desc)
			}
		})
	}
}

func TestSanitizeFilename_EdgeCases(t *testing.T) {
	r, _ := newTestReader(t)

	// Filename with spaces is valid
	got, err := r.sanitizeFilename("my important document.pdf")
	if err != nil {
		t.Errorf("filename with spaces: unexpected error: %v", err)
	}
	if got != "my important document.pdf" {
		t.Errorf("filename with spaces: got %q, want %q", got, "my important document.pdf")
	}

	// Unicode filename with supported extension
	got, err = r.sanitizeFilename("documento-espanol.txt")
	if err != nil {
		t.Errorf("unicode filename: unexpected error: %v", err)
	}
	if got != "documento-espanol.txt" {
		t.Errorf("unicode filename: got %q, want %q", got, "documento-espanol.txt")
	}

	// Empty string — filepath.Base("") returns ".", so it won't match the filename
	_, err = r.sanitizeFilename("")
	if err == nil {
		t.Error("empty filename: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// 2. ListDocuments
// ---------------------------------------------------------------------------

func TestListDocuments_WithFiles(t *testing.T) {
	r, dir := newTestReader(t)

	writeFile(t, dir, "report.txt", "hello world")
	writeFile(t, dir, "notes.md", "# Notes")
	writeFile(t, dir, "data.csv", "a,b,c\n1,2,3")
	writeFile(t, dir, "readme.pdf", "fake pdf content") // not a real PDF but will be listed

	docs, err := r.ListDocuments(t.Context())
	if err != nil {
		t.Fatalf("ListDocuments() error: %v", err)
	}

	if len(docs) != 4 {
		t.Fatalf("ListDocuments() returned %d docs, want 4", len(docs))
	}

	// Build a map for easy lookup
	found := map[string]DocumentInfo{}
	for _, d := range docs {
		found[d.Filename] = d
	}

	for _, name := range []string{"report.txt", "notes.md", "data.csv", "readme.pdf"} {
		if _, ok := found[name]; !ok {
			t.Errorf("ListDocuments() missing expected file: %s", name)
		}
	}

	// Verify size is non-zero for text files we wrote
	if found["report.txt"].SizeBytes == 0 {
		t.Error("report.txt should have non-zero size")
	}

	// Verify title is derived from filename (without extension)
	if found["notes.md"].Title != "notes" {
		t.Errorf("notes.md title = %q, want %q", found["notes.md"].Title, "notes")
	}
}

func TestListDocuments_ExcludesUnsupported(t *testing.T) {
	r, dir := newTestReader(t)

	writeFile(t, dir, "good.txt", "ok")
	writeFile(t, dir, "bad.exe", "nope")
	writeFile(t, dir, "script.sh", "#!/bin/bash")
	writeFile(t, dir, "archive.zip", "pk")

	docs, err := r.ListDocuments(t.Context())
	if err != nil {
		t.Fatalf("ListDocuments() error: %v", err)
	}

	if len(docs) != 1 {
		t.Fatalf("ListDocuments() returned %d docs, want 1 (only good.txt)", len(docs))
	}
	if docs[0].Filename != "good.txt" {
		t.Errorf("expected good.txt, got %s", docs[0].Filename)
	}
}

func TestListDocuments_EmptyDirectory(t *testing.T) {
	r, _ := newTestReader(t)

	docs, err := r.ListDocuments(t.Context())
	if err != nil {
		t.Fatalf("ListDocuments() error: %v", err)
	}

	if docs != nil && len(docs) != 0 {
		t.Errorf("ListDocuments() on empty dir returned %d docs, want 0", len(docs))
	}
}

func TestListDocuments_SkipsSubdirectories(t *testing.T) {
	r, dir := newTestReader(t)

	writeFile(t, dir, "top.txt", "top level")
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, subdir, "nested.txt", "should not appear")

	docs, err := r.ListDocuments(t.Context())
	if err != nil {
		t.Fatalf("ListDocuments() error: %v", err)
	}

	if len(docs) != 1 {
		t.Fatalf("expected 1 doc (top.txt only), got %d", len(docs))
	}
}

// ---------------------------------------------------------------------------
// 3. ReadDocument (text and markdown — no poppler needed)
// ---------------------------------------------------------------------------

func TestReadDocument_TextFormats_ReturnContentVerbatim(t *testing.T) {
	// The tool description promises "raw text of a PDF, TXT, MD, CSV, or DOCX
	// file". Text formats must be read directly, never through pdftotext.
	r, dir := newTestReader(t)
	cases := map[string]string{
		"sample.txt": "Line one\nLine two\nLine three",
		"notes.md":   "# Title\n\nSome *markdown* body.",
		"data.csv":   "a,b,c\n1,2,3",
	}
	for name, content := range cases {
		writeFile(t, dir, name, content)
		got, err := r.ReadDocument(t.Context(), name, 0)
		if err != nil {
			t.Fatalf("ReadDocument(%s): unexpected error: %v", name, err)
		}
		if got != content {
			t.Errorf("ReadDocument(%s) = %q, want %q", name, got, content)
		}
	}
}

func TestReadDocument_TextFormats_ServeCache(t *testing.T) {
	r, dir := newTestReader(t)
	writeFile(t, dir, "notes.md", "v1")
	if got, _ := r.ReadDocument(t.Context(), "notes.md", 0); got != "v1" {
		t.Fatalf("first read = %q", got)
	}
	// Same mtime -> cached; a rewrite with a newer mtime must be picked up.
	time.Sleep(20 * time.Millisecond)
	writeFile(t, dir, "notes.md", "v2")
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(filepath.Join(dir, "notes.md"), future, future)
	if got, _ := r.ReadDocument(t.Context(), "notes.md", 0); got != "v2" {
		t.Errorf("after rewrite = %q, want v2 (cache must key on mtime)", got)
	}
}

func TestReadDocument_TextFormats_RejectPageSelection(t *testing.T) {
	// Pages are a PDF concept; asking for one on a text file must be a clear
	// error, not a pdftotext failure and not silently the whole file.
	r, dir := newTestReader(t)
	writeFile(t, dir, "notes.md", "# Title")
	if _, err := r.ReadDocument(t.Context(), "notes.md", 2); err == nil || !strings.Contains(err.Error(), "PDF") {
		t.Errorf("ReadDocument(page=2) on .md: want a 'only supported for PDF' error, got %v", err)
	}
	if _, err := r.ReadDocumentPages(t.Context(), "notes.md", "1-2"); err == nil || !strings.Contains(err.Error(), "PDF") {
		t.Errorf("ReadDocumentPages on .md: want a 'only supported for PDF' error, got %v", err)
	}
}

func TestSearchDocument_TextFile_FindsMatches(t *testing.T) {
	r, dir := newTestReader(t)
	writeFile(t, dir, "log.txt", "alpha\nbeta needle here\ngamma")
	out, err := r.SearchDocument(t.Context(), "log.txt", "needle")
	if err != nil {
		t.Fatalf("SearchDocument(.txt): %v", err)
	}
	if !strings.Contains(out, "needle") {
		t.Errorf("search output should contain the matching line, got %q", out)
	}
}

func TestGetDocumentSummary_TextFile_ReturnsHead(t *testing.T) {
	r, dir := newTestReader(t)
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	writeFile(t, dir, "big.md", b.String())
	out, err := r.GetDocumentSummary(t.Context(), "big.md")
	if err != nil {
		t.Fatalf("GetDocumentSummary(.md): %v", err)
	}
	if !strings.Contains(out, "line 1\n") || strings.Contains(out, "line 300") {
		t.Errorf("summary should be the head of the file, not all of it: %q", out[:min(len(out), 200)])
	}
}

func TestReadDocument_Docx_RequiresPandoc(t *testing.T) {
	r, dir := newTestReader(t)

	// A pandoc that is not there, whatever this machine has installed.
	r.pandocBin = filepath.Join(dir, "no-such-pandoc")
	writeFile(t, dir, "doc.docx", "not really a docx")
	_, err := r.ReadDocument(t.Context(), "doc.docx", 0)
	if err == nil || !strings.Contains(err.Error(), "pandoc") {
		t.Errorf("ReadDocument(.docx) without pandoc: want an error naming pandoc, got %v", err)
	}
}

func TestReadDocument_Docx_ViaPandoc(t *testing.T) {
	if _, err := exec.LookPath("pandoc"); err != nil {
		t.Skip("pandoc not installed")
	}
	r, dir := newTestReader(t)
	// build a real docx with pandoc itself
	src := filepath.Join(dir, "src.md")
	_ = os.WriteFile(src, []byte("# Heading\n\nBody text from docx."), 0o644)
	if out, err := exec.Command("pandoc", src, "-o", filepath.Join(dir, "doc.docx")).CombinedOutput(); err != nil {
		t.Skipf("pandoc could not build fixture: %v %s", err, out)
	}
	got, err := r.ReadDocument(t.Context(), "doc.docx", 0)
	if err != nil {
		t.Fatalf("ReadDocument(.docx): %v", err)
	}
	if !strings.Contains(got, "Body text from docx") {
		t.Errorf("docx text not extracted: %q", got)
	}
}

func TestReadDocument_SanitizesFilenameFirst(t *testing.T) {
	r, dir := newTestReader(t)

	// Verify that sanitization happens BEFORE file-not-found check
	writeFile(t, dir, "legit.txt", "content")

	_, err := r.ReadDocument(t.Context(), "../escape.txt", 0)
	if err == nil {
		t.Error("ReadDocument() should reject directory traversal")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected 'traversal' error, got: %v", err)
	}
}

func TestReadDocument_NotFound(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ReadDocument(t.Context(), "nonexistent.txt", 0)
	if err == nil {
		t.Error("ReadDocument() for nonexistent file: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestReadDocument_InvalidFilename(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ReadDocument(t.Context(), "../escape.txt", 0)
	if err == nil {
		t.Error("ReadDocument() with traversal: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected 'traversal' in error, got: %v", err)
	}
}

func TestReadDocument_UnsupportedExtension(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ReadDocument(t.Context(), "malware.exe", 0)
	if err == nil {
		t.Error("ReadDocument() with .exe: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported extension") {
		t.Errorf("expected 'unsupported extension' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 4. SearchDocument (using .txt files — reads via extractFull)
// ---------------------------------------------------------------------------

// Note: SearchDocument internally calls extractFull which uses pdftotext.
// For text files, pdftotext will fail. We test the validation/sanitization layer here.

func TestSearchDocument_InvalidFilename(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.SearchDocument(t.Context(), "../etc/passwd", "root")
	if err == nil {
		t.Error("SearchDocument() with traversal: expected error, got nil")
	}
}

func TestSearchDocument_NotFound(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.SearchDocument(t.Context(), "missing.txt", "hello")
	if err == nil {
		t.Error("SearchDocument() for missing file: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 5. GetDocumentSummary
// ---------------------------------------------------------------------------

func TestGetDocumentSummary_InvalidFilename(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.GetDocumentSummary(t.Context(), "../bad.txt")
	if err == nil {
		t.Error("GetDocumentSummary() with traversal: expected error, got nil")
	}
}

func TestGetDocumentSummary_NotFound(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.GetDocumentSummary(t.Context(), "missing.txt")
	if err == nil {
		t.Error("GetDocumentSummary() for missing file: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// 6. ConvertToMarkdown
// ---------------------------------------------------------------------------

func TestConvertToMarkdown_MdPassthrough(t *testing.T) {
	r, dir := newTestReader(t)

	content := "# Hello\n\nThis is **bold** markdown."
	writeFile(t, dir, "readme.md", content)

	got, err := r.ConvertToMarkdown(t.Context(), "readme.md")
	if err != nil {
		t.Fatalf("ConvertToMarkdown(.md) error: %v", err)
	}

	if got != content {
		t.Errorf("ConvertToMarkdown(.md) should return content unchanged\ngot:  %q\nwant: %q", got, content)
	}
}

func TestConvertToMarkdown_TxtWrappedInCodeBlock(t *testing.T) {
	r, dir := newTestReader(t)

	content := "some plain text\nwith multiple lines"
	writeFile(t, dir, "note.txt", content)

	got, err := r.ConvertToMarkdown(t.Context(), "note.txt")
	if err != nil {
		t.Fatalf("ConvertToMarkdown(.txt) error: %v", err)
	}

	expected := "```text\n" + content + "\n```"
	if got != expected {
		t.Errorf("ConvertToMarkdown(.txt) mismatch\ngot:  %q\nwant: %q", got, expected)
	}
}

func TestConvertToMarkdown_CsvWrappedInCodeBlock(t *testing.T) {
	r, dir := newTestReader(t)

	content := "name,age,city\nAlice,30,NYC\nBob,25,LA"
	writeFile(t, dir, "people.csv", content)

	got, err := r.ConvertToMarkdown(t.Context(), "people.csv")
	if err != nil {
		t.Fatalf("ConvertToMarkdown(.csv) error: %v", err)
	}

	expected := "```csv\n" + content + "\n```"
	if got != expected {
		t.Errorf("ConvertToMarkdown(.csv) mismatch\ngot:  %q\nwant: %q", got, expected)
	}
}

func TestConvertToMarkdown_NotFound(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ConvertToMarkdown(t.Context(), "ghost.md")
	if err == nil {
		t.Error("ConvertToMarkdown() for missing file: expected error, got nil")
	}
}

func TestConvertToMarkdown_InvalidFilename(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ConvertToMarkdown(t.Context(), "../escape.md")
	if err == nil {
		t.Error("ConvertToMarkdown() with traversal: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// 7. GetDocumentOutline
// ---------------------------------------------------------------------------

func TestGetDocumentOutline_MarkdownHeadings(t *testing.T) {
	r, dir := newTestReader(t)

	content := `# Title
Some intro text.

## Section One
Content here.

### Subsection
More content.

## Section Two
Final content.
`
	writeFile(t, dir, "outline.md", content)

	entries, err := r.GetDocumentOutline(t.Context(), "outline.md")
	if err != nil {
		t.Fatalf("GetDocumentOutline(.md) error: %v", err)
	}

	if len(entries) != 4 {
		t.Fatalf("expected 4 outline entries, got %d: %+v", len(entries), entries)
	}

	expected := []struct {
		level int
		title string
	}{
		{1, "Title"},
		{2, "Section One"},
		{3, "Subsection"},
		{2, "Section Two"},
	}

	for i, e := range expected {
		if entries[i].Level != e.level {
			t.Errorf("entry[%d].Level = %d, want %d", i, entries[i].Level, e.level)
		}
		if entries[i].Title != e.title {
			t.Errorf("entry[%d].Title = %q, want %q", i, entries[i].Title, e.title)
		}
	}
}

func TestGetDocumentOutline_TxtNoHeadings(t *testing.T) {
	r, dir := newTestReader(t)

	content := "Just some plain text.\nNo headings here.\nNothing special."
	writeFile(t, dir, "plain.txt", content)

	entries, err := r.GetDocumentOutline(t.Context(), "plain.txt")
	if err != nil {
		t.Fatalf("GetDocumentOutline(.txt) error: %v", err)
	}

	// Plain text without # headings should return empty outline
	if len(entries) != 0 {
		t.Errorf("expected 0 outline entries for plain text, got %d: %+v", len(entries), entries)
	}
}

func TestGetDocumentOutline_TxtWithMarkdownHeadings(t *testing.T) {
	r, dir := newTestReader(t)

	// txt files also go through outlineMarkdown
	content := "# A heading in a txt file\nSome text\n## Another heading"
	writeFile(t, dir, "mixed.txt", content)

	entries, err := r.GetDocumentOutline(t.Context(), "mixed.txt")
	if err != nil {
		t.Fatalf("GetDocumentOutline(.txt with headings) error: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Level != 1 || entries[0].Title != "A heading in a txt file" {
		t.Errorf("entry[0] = %+v, unexpected", entries[0])
	}
}

func TestGetDocumentOutline_NotFound(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.GetDocumentOutline(t.Context(), "missing.md")
	if err == nil {
		t.Error("GetDocumentOutline() for missing file: expected error, got nil")
	}
}

func TestGetDocumentOutline_UnsupportedFormat(t *testing.T) {
	r, dir := newTestReader(t)

	writeFile(t, dir, "data.csv", "a,b,c")

	_, err := r.GetDocumentOutline(t.Context(), "data.csv")
	if err == nil {
		t.Error("GetDocumentOutline(.csv) should return unsupported format error")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected 'not supported' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 8. ExtractTables
// ---------------------------------------------------------------------------

func TestExtractTables_CSV(t *testing.T) {
	r, dir := newTestReader(t)

	content := "name,age,city\nAlice,30,NYC\nBob,25,LA"
	writeFile(t, dir, "people.csv", content)

	tables, err := r.ExtractTables(t.Context(), "people.csv", 0)
	if err != nil {
		t.Fatalf("ExtractTables(.csv) error: %v", err)
	}

	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}

	table := tables[0]
	if len(table.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(table.Rows))
	}

	// Header row
	if len(table.Rows[0]) != 3 {
		t.Fatalf("expected 3 columns in header, got %d", len(table.Rows[0]))
	}
	if table.Rows[0][0] != "name" || table.Rows[0][1] != "age" || table.Rows[0][2] != "city" {
		t.Errorf("header row = %v, want [name age city]", table.Rows[0])
	}

	// Data rows
	if table.Rows[1][0] != "Alice" {
		t.Errorf("row[1][0] = %q, want 'Alice'", table.Rows[1][0])
	}
	if table.Rows[2][1] != "25" {
		t.Errorf("row[2][1] = %q, want '25'", table.Rows[2][1])
	}
}

func TestExtractTables_CSVWithQuotedFields(t *testing.T) {
	r, dir := newTestReader(t)

	content := `name,description,value
"Smith, John","Has a comma",100
"O""Brien",Normal,200`
	writeFile(t, dir, "quoted.csv", content)

	tables, err := r.ExtractTables(t.Context(), "quoted.csv", 0)
	if err != nil {
		t.Fatalf("ExtractTables(.csv quoted) error: %v", err)
	}

	if len(tables) != 1 || len(tables[0].Rows) != 3 {
		t.Fatalf("expected 1 table with 3 rows, got %d tables", len(tables))
	}

	// Quoted field with comma
	if tables[0].Rows[1][0] != "Smith, John" {
		t.Errorf("quoted field = %q, want 'Smith, John'", tables[0].Rows[1][0])
	}
	// Escaped quote
	if tables[0].Rows[2][0] != `O"Brien` {
		t.Errorf("escaped quote field = %q, want 'O\"Brien'", tables[0].Rows[2][0])
	}
}

func TestExtractTables_CSVWithQuotedNewline(t *testing.T) {
	r, dir := newTestReader(t)

	// A quoted field may hold a line break; it is one cell of one record,
	// not the end of the row.
	content := "name,address\n\"Alice\",\"12 Main St\nSpringfield\"\nBob,LA\n"
	writeFile(t, dir, "addresses.csv", content)

	tables, err := r.ExtractTables(t.Context(), "addresses.csv", 0)
	if err != nil {
		t.Fatalf("ExtractTables(.csv with a quoted newline) error: %v", err)
	}

	if len(tables) != 1 || len(tables[0].Rows) != 3 {
		t.Fatalf("want 1 table with 3 rows, got %+v", tables)
	}
	if got := tables[0].Rows[1][1]; got != "12 Main St\nSpringfield" {
		t.Errorf("quoted multi-line field = %q, want %q", got, "12 Main St\nSpringfield")
	}
	if got := tables[0].Rows[2]; len(got) != 2 || got[0] != "Bob" || got[1] != "LA" {
		t.Errorf("row after the multi-line field = %v, want [Bob LA]", got)
	}
}

func TestExtractTables_CSVTrimsCellsAndAllowsRaggedRows(t *testing.T) {
	r, dir := newTestReader(t)

	// The hand-rolled splitter trimmed every cell and took rows of any
	// length; encoding/csv must keep both.
	writeFile(t, dir, "ragged.csv", " a , b , c \nsingle\n")

	tables, err := r.ExtractTables(t.Context(), "ragged.csv", 0)
	if err != nil {
		t.Fatalf("ExtractTables(ragged .csv) error: %v", err)
	}

	rows := tables[0].Rows
	if len(rows) != 2 || strings.Join(rows[0], "|") != "a|b|c" || strings.Join(rows[1], "|") != "single" {
		t.Errorf("rows = %q, want [[a b c] [single]]", rows)
	}
}

func TestExtractTables_MarkdownPipeTable(t *testing.T) {
	r, dir := newTestReader(t)

	content := `# Some heading

| Name  | Age | City |
|-------|-----|------|
| Alice | 30  | NYC  |
| Bob   | 25  | LA   |

Some trailing text.
`
	writeFile(t, dir, "table.md", content)

	tables, err := r.ExtractTables(t.Context(), "table.md", 0)
	if err != nil {
		t.Fatalf("ExtractTables(.md) error: %v", err)
	}

	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}

	table := tables[0]
	// Separator row is skipped, so we expect header + 2 data rows = 3 rows
	if len(table.Rows) != 3 {
		t.Fatalf("expected 3 rows (header + 2 data), got %d: %+v", len(table.Rows), table.Rows)
	}

	if table.Rows[0][0] != "Name" {
		t.Errorf("header[0] = %q, want 'Name'", table.Rows[0][0])
	}
	if table.Rows[1][2] != "NYC" {
		t.Errorf("row[1][2] = %q, want 'NYC'", table.Rows[1][2])
	}
}

func TestExtractTables_EmptyCSV(t *testing.T) {
	r, dir := newTestReader(t)

	// tablesCSV: strings.TrimSpace + strings.Split always yields at least [""],
	// so even an empty/whitespace CSV produces 1 table with 1 row of 1 empty cell.
	// This documents the current behavior.
	writeFile(t, dir, "empty.csv", "   ")

	tables, err := r.ExtractTables(t.Context(), "empty.csv", 0)
	if err != nil {
		t.Fatalf("ExtractTables(empty.csv) error: %v", err)
	}

	// Current behavior: 1 table with a single empty-cell row
	if len(tables) != 1 {
		t.Errorf("expected 1 table for empty CSV (current behavior), got %d", len(tables))
	}
	if len(tables) == 1 && len(tables[0].Rows) != 1 {
		t.Errorf("expected 1 row in empty CSV table, got %d", len(tables[0].Rows))
	}
}

func TestExtractTables_SingleEmptyLineCSV(t *testing.T) {
	r, dir := newTestReader(t)

	// A truly empty string: strings.TrimSpace("") == "", strings.Split("", "\n") == [""]
	// tablesCSV parses that as 1 row with 1 empty cell, yielding 1 table.
	writeFile(t, dir, "blank.csv", "")

	tables, err := r.ExtractTables(t.Context(), "blank.csv", 0)
	if err != nil {
		t.Fatalf("ExtractTables(blank.csv) error: %v", err)
	}

	// This is the actual behavior — an empty file still produces 1 table with 1 row
	if len(tables) != 1 {
		t.Errorf("expected 1 table for empty CSV (current behavior), got %d", len(tables))
	}
}

func TestExtractTables_PlaintextWithTabs(t *testing.T) {
	r, dir := newTestReader(t)

	content := "Name\tAge\tCity\nAlice\t30\tNYC\nBob\t25\tLA"
	writeFile(t, dir, "tabbed.txt", content)

	tables, err := r.ExtractTables(t.Context(), "tabbed.txt", 0)
	if err != nil {
		t.Fatalf("ExtractTables(.txt tabs) error: %v", err)
	}

	if len(tables) < 1 {
		t.Fatal("expected at least 1 table from tab-delimited text")
	}

	if len(tables[0].Rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(tables[0].Rows))
	}
}

func TestExtractTables_NotFound(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ExtractTables(t.Context(), "missing.csv", 0)
	if err == nil {
		t.Error("ExtractTables() for missing file: expected error, got nil")
	}
}

func TestExtractTables_UnsupportedFormat(t *testing.T) {
	r, dir := newTestReader(t)

	writeFile(t, dir, "doc.docx", "fake docx")

	_, err := r.ExtractTables(t.Context(), "doc.docx", 0)
	if err == nil {
		t.Error("ExtractTables(.docx) should return unsupported format error")
	}
}

// ---------------------------------------------------------------------------
// 9. ReadImage (validation only — no actual OCR)
// ---------------------------------------------------------------------------

func TestReadImage_InvalidExtension(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ReadImage(t.Context(), "document.pdf", "eng")
	if err == nil {
		t.Error("ReadImage(.pdf) should reject non-image extension")
	}
	if !strings.Contains(err.Error(), "unsupported image extension") {
		t.Errorf("expected 'unsupported image extension' in error, got: %v", err)
	}
}

func TestReadImage_DirectoryTraversal(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ReadImage(t.Context(), "../secret.png", "eng")
	if err == nil {
		t.Error("ReadImage() with traversal should be rejected")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected 'traversal' in error, got: %v", err)
	}
}

func TestReadImage_NotFound(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ReadImage(t.Context(), "missing.png", "eng")
	if err == nil {
		t.Error("ReadImage() for missing file: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "not available") {
		t.Errorf("expected 'not found' or 'not available' in error, got: %v", err)
	}
}

func TestSanitizeImageFilename_ValidImages(t *testing.T) {
	r, _ := newTestReader(t)

	valid := []string{"photo.png", "pic.jpg", "img.jpeg", "scan.tiff", "scan.tif", "map.bmp"}
	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			got, err := r.sanitizeImageFilename(name)
			if err != nil {
				t.Errorf("sanitizeImageFilename(%q) error: %v", name, err)
			}
			if got != name {
				t.Errorf("sanitizeImageFilename(%q) = %q, want %q", name, got, name)
			}
		})
	}
}

func TestSanitizeImageFilename_RejectsNonImages(t *testing.T) {
	r, _ := newTestReader(t)

	invalid := []string{"doc.pdf", "notes.txt", "data.csv", "file.md", "script.sh"}
	for _, name := range invalid {
		t.Run(name, func(t *testing.T) {
			_, err := r.sanitizeImageFilename(name)
			if err == nil {
				t.Errorf("sanitizeImageFilename(%q) should reject non-image extension", name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 10. ListFormats
// ---------------------------------------------------------------------------

func TestListFormats_ReturnsExpectedFormats(t *testing.T) {
	r, _ := newTestReader(t)

	formats := r.ListFormats()

	if len(formats) == 0 {
		t.Fatal("ListFormats() returned empty list")
	}

	// Should have at least 6 entries
	if len(formats) < 6 {
		t.Errorf("ListFormats() returned %d entries, want at least 6", len(formats))
	}

	// Build a map by extension
	byExt := map[string]FormatInfo{}
	for _, f := range formats {
		byExt[f.Extension] = f
	}

	// .txt, .md, .csv should always be installed (no deps)
	for _, ext := range []string{".txt", ".md", ".csv"} {
		info, ok := byExt[ext]
		if !ok {
			t.Errorf("ListFormats() missing %s", ext)
			continue
		}
		if !info.Installed {
			t.Errorf("%s should always be installed (requires: none)", ext)
		}
		if info.Status != "supported" {
			t.Errorf("%s status = %q, want 'supported'", ext, info.Status)
		}
	}

	// .pdf entry should exist
	pdfInfo, ok := byExt[".pdf"]
	if !ok {
		t.Error("ListFormats() missing .pdf")
	} else {
		if pdfInfo.Requires != "poppler" {
			t.Errorf(".pdf requires = %q, want 'poppler'", pdfInfo.Requires)
		}
	}

	// .docx entry should exist with optional status
	docxInfo, ok := byExt[".docx"]
	if !ok {
		t.Error("ListFormats() missing .docx")
	} else {
		if docxInfo.Status != "optional" {
			t.Errorf(".docx status = %q, want 'optional'", docxInfo.Status)
		}
	}
}

// ---------------------------------------------------------------------------
// 11. parsePageRanges (internal function — unit test)
// ---------------------------------------------------------------------------

func TestParsePageRanges_Valid(t *testing.T) {
	tests := []struct {
		input    string
		expected [][2]int
	}{
		{"1", [][2]int{{1, 1}}},
		{"5", [][2]int{{5, 5}}},
		{"1-5", [][2]int{{1, 5}}},
		{"1-3,7,10-12", [][2]int{{1, 3}, {7, 7}, {10, 12}}},
		{"1, 2, 3", [][2]int{{1, 1}, {2, 2}, {3, 3}}},
		{"1-1", [][2]int{{1, 1}}},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parsePageRanges(tc.input)
			if err != nil {
				t.Fatalf("parsePageRanges(%q) error: %v", tc.input, err)
			}
			if len(got) != len(tc.expected) {
				t.Fatalf("parsePageRanges(%q) = %v, want %v", tc.input, got, tc.expected)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("parsePageRanges(%q)[%d] = %v, want %v", tc.input, i, got[i], tc.expected[i])
				}
			}
		})
	}
}

func TestParsePageRanges_Invalid(t *testing.T) {
	invalid := []struct {
		input string
		desc  string
	}{
		{"", "empty string"},
		{"abc", "non-numeric"},
		{"0", "zero page"},
		{"-1", "negative page"},
		{"5-3", "reversed range"},
		{"1-abc", "non-numeric end"},
	}

	for _, tc := range invalid {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := parsePageRanges(tc.input)
			if err == nil {
				t.Errorf("parsePageRanges(%q) expected error for %s, got nil", tc.input, tc.desc)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 12. isTextEmpty (internal function — unit test)
// ---------------------------------------------------------------------------

func TestIsTextEmpty(t *testing.T) {
	tests := []struct {
		text      string
		pageCount int
		expected  bool
	}{
		{"", 1, true},                        // empty
		{"   \n\t\r  ", 1, true},             // whitespace only
		{"hello world", 1, true},             // 10 chars < 50 threshold
		{strings.Repeat("x", 50), 1, false},  // exactly 50 non-ws chars
		{strings.Repeat("x", 49), 1, true},   // 49 < 50
		{strings.Repeat("x", 100), 2, false}, // 100 >= 100 (50*2)
		{strings.Repeat("x", 99), 2, true},   // 99 < 100
		{"abc", 0, true},                     // pageCount 0 treated as 1
	}

	for i, tc := range tests {
		got := isTextEmpty(tc.text, tc.pageCount)
		if got != tc.expected {
			t.Errorf("case %d: isTextEmpty(%q, %d) = %v, want %v", i, tc.text[:min(len(tc.text), 20)], tc.pageCount, got, tc.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// 13. matchNumberedHeading (internal function — unit test)
// ---------------------------------------------------------------------------

func TestMatchNumberedHeading(t *testing.T) {
	tests := []struct {
		line     string
		expected int
	}{
		{"Chapter 1", 1},
		{"chapter one", 1},
		{"Part I", 1},
		{"part two", 1},
		{"1. Introduction", 1},
		{"1.1 Background", 2},
		{"1.1.1 Details", 3},
		{"2.3.4.5 Deep heading", 4},
		{"Regular text line here", 0},
		{"Hello", 0}, // single word, no number prefix
		{"1.", 0},    // only number, no text after
	}

	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			got := matchNumberedHeading(tc.line)
			if got != tc.expected {
				t.Errorf("matchNumberedHeading(%q) = %d, want %d", tc.line, got, tc.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 15. splitMultiSpace (internal function — unit test)
// ---------------------------------------------------------------------------

func TestSplitMultiSpace(t *testing.T) {
	tests := []struct {
		line     string
		expected []string
	}{
		{"hello   world", []string{"hello", "world"}},
		{"col1     col2     col3", []string{"col1", "col2", "col3"}},
		{"no multi spaces here", nil}, // returns nil when < 2 parts
		{"one   two", []string{"one", "two"}},
	}

	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			got := splitMultiSpace(tc.line)
			if tc.expected == nil {
				// For single-word results, just check length < 2
				if len(got) >= 2 {
					t.Errorf("splitMultiSpace(%q) = %v, expected fewer than 2 parts", tc.line, got)
				}
				return
			}
			if len(got) != len(tc.expected) {
				t.Fatalf("splitMultiSpace(%q) = %v (len %d), want %v (len %d)", tc.line, got, len(got), tc.expected, len(tc.expected))
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("splitMultiSpace(%q)[%d] = %q, want %q", tc.line, i, got[i], tc.expected[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 16. parsePipeRow (internal function — unit test)
// ---------------------------------------------------------------------------

func TestParsePipeRow(t *testing.T) {
	tests := []struct {
		line     string
		expected []string
	}{
		{"| A | B | C |", []string{"A", "B", "C"}},
		{"| hello | world |", []string{"hello", "world"}},
		{"A | B | C", []string{"A", "B", "C"}},
		{"| single |", []string{"single"}},
	}

	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			got := parsePipeRow(tc.line)
			if len(got) != len(tc.expected) {
				t.Fatalf("parsePipeRow(%q) = %v (len %d), want %v (len %d)", tc.line, got, len(got), tc.expected, len(tc.expected))
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("parsePipeRow(%q)[%d] = %q, want %q", tc.line, i, got[i], tc.expected[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 17. detectTableRow (internal function — unit test)
// ---------------------------------------------------------------------------

func TestDetectTableRow(t *testing.T) {
	tests := []struct {
		line     string
		hasRow   bool
		minCells int
	}{
		{"| A | B | C |", true, 3},
		{"Name\tAge\tCity", true, 3},
		{"col1   col2   col3", true, 3},
		{"just plain text", false, 0},
		{"", false, 0},
		{"|---|---|", false, 0}, // separator line
	}

	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			got := detectTableRow(tc.line)
			if tc.hasRow {
				if got == nil {
					t.Errorf("detectTableRow(%q) = nil, expected row", tc.line)
				} else if len(got) < tc.minCells {
					t.Errorf("detectTableRow(%q) = %v (len %d), want at least %d cells", tc.line, got, len(got), tc.minCells)
				}
			} else {
				if got != nil {
					t.Errorf("detectTableRow(%q) = %v, expected nil", tc.line, got)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 18. NewReader / fullPath
// ---------------------------------------------------------------------------

func TestNewReader(t *testing.T) {
	dir := t.TempDir()
	r := NewReader(dir)

	if r.docsDir != dir {
		t.Errorf("NewReader().docsDir = %q, want %q", r.docsDir, dir)
	}
	if r.cache == nil {
		t.Error("NewReader().cache should be initialized, got nil")
	}
}

func TestFullPath(t *testing.T) {
	dir := t.TempDir()
	r := NewReader(dir)

	got := r.fullPath("test.pdf")
	want := filepath.Join(dir, "test.pdf")
	if got != want {
		t.Errorf("fullPath(%q) = %q, want %q", "test.pdf", got, want)
	}
}

// ---------------------------------------------------------------------------
// 19. pdfTextToMarkdown (internal function — unit test for heading detection)
// ---------------------------------------------------------------------------

func TestPdfTextToMarkdown(t *testing.T) {
	r, _ := newTestReader(t)

	input := "INTRODUCTION\n\nThis is body text that goes on for a while.\n\n1.1 Background\n\nMore text here."
	got := r.pdfTextToMarkdown(input)

	// INTRODUCTION should become ## INTRODUCTION
	if !strings.Contains(got, "## INTRODUCTION") {
		t.Errorf("expected '## INTRODUCTION' in output, got:\n%s", got)
	}

	// Body text should remain as-is
	if !strings.Contains(got, "This is body text") {
		t.Errorf("expected body text preserved, got:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// 20. HasOCR
// ---------------------------------------------------------------------------

func TestHasOCR(t *testing.T) {
	dir := t.TempDir()
	r := NewReader(dir)

	// HasOCR should return a boolean without panicking
	_ = r.HasOCR()
}

// min helper for Go < 1.21 compatibility
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// 21. Confinement to the documents directory
// ---------------------------------------------------------------------------

// secretText is what the file outside the documents directory holds, so a
// test can tell whether any tool leaked it.
const secretText = "TOP SECRET: this file lives outside DOCS_MCP_DIR"

// writeOutsideSecret creates a file in a directory that is not the documents
// directory and returns its path.
func writeOutsideSecret(t *testing.T, name string) string {
	t.Helper()
	outside := t.TempDir()
	writeFile(t, outside, name, "# Heading\n\n"+secretText+"\n")
	return filepath.Join(outside, name)
}

func TestDocuments_SymlinkOutsideDirIsRejected(t *testing.T) {
	r, dir := newTestReader(t)

	secret := writeOutsideSecret(t, "secret.md")
	if err := os.Symlink(secret, filepath.Join(dir, "innocent.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "innocent.png")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "innocent.pdf")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Every tool that opens a document by name must refuse the outward link.
	calls := map[string]func() (any, error){
		"ReadDocument":        func() (any, error) { return r.ReadDocument(t.Context(), "innocent.md", 0) },
		"SearchDocument":      func() (any, error) { return r.SearchDocument(t.Context(), "innocent.md", "SECRET") },
		"GetDocumentSummary":  func() (any, error) { return r.GetDocumentSummary(t.Context(), "innocent.md") },
		"GetDocumentOutline":  func() (any, error) { return r.GetDocumentOutline(t.Context(), "innocent.md") },
		"ExtractTables":       func() (any, error) { return r.ExtractTables(t.Context(), "innocent.md", 0) },
		"ConvertToMarkdown":   func() (any, error) { return r.ConvertToMarkdown(t.Context(), "innocent.md") },
		"ReadImage":           func() (any, error) { return r.ReadImage(t.Context(), "innocent.png", "eng") },
		"ReadDocumentPages":   func() (any, error) { return r.ReadDocumentPages(t.Context(), "innocent.pdf", "1") },
		"GetDocumentMetadata": func() (any, error) { return r.GetDocumentMetadata(t.Context(), "innocent.pdf") },
		"ExtractImages":       func() (any, error) { return r.ExtractImages(t.Context(), "innocent.pdf", 0) },
		"OCRDocument":         func() (any, error) { return r.OCRDocument(t.Context(), "innocent.pdf", 1, "eng") },
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			result, err := call()
			if err == nil {
				t.Fatalf("%s followed a symlink out of the documents directory: %v", name, result)
			}
			if strings.Contains(fmt.Sprint(result), "TOP SECRET") {
				t.Errorf("%s leaked the outside file: %v", name, result)
			}
			if !strings.Contains(err.Error(), "outside the documents directory") {
				t.Errorf("%s: want an 'outside the documents directory' error, got %v", name, err)
			}
		})
	}
}

func TestDocuments_SymlinkInsideDirStillWorks(t *testing.T) {
	r, dir := newTestReader(t)

	writeFile(t, dir, "real.txt", "inside content")
	if err := os.Symlink("real.txt", filepath.Join(dir, "alias.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := r.ReadDocument(t.Context(), "alias.txt", 0)
	if err != nil {
		t.Fatalf("ReadDocument(alias.txt): %v", err)
	}
	if got != "inside content" {
		t.Errorf("ReadDocument(alias.txt) = %q, want %q", got, "inside content")
	}
}

func TestListDocuments_OmitsSymlinksThatLeaveTheDir(t *testing.T) {
	r, dir := newTestReader(t)

	writeFile(t, dir, "real.txt", "inside content")
	if err := os.Symlink("real.txt", filepath.Join(dir, "alias.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink(writeOutsideSecret(t, "secret.txt"), filepath.Join(dir, "innocent.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "gone.txt"), filepath.Join(dir, "dangling.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	docs, err := r.ListDocuments(t.Context())
	if err != nil {
		t.Fatalf("ListDocuments() with a dangling symlink must not fail: %v", err)
	}

	var names []string
	for _, doc := range docs {
		names = append(names, doc.Filename)
	}
	listed := strings.Join(names, ",")

	if listed != "alias.txt,real.txt" {
		t.Errorf("ListDocuments() = %s, want alias.txt,real.txt (no outward or dangling links)", listed)
	}
}

// ---------------------------------------------------------------------------
// 22. Deadlines on the external binaries
// ---------------------------------------------------------------------------

// writeFakeBinary writes an executable shell script called name into dir and
// returns its path, so a test can point the reader at it instead of the real
// poppler or tesseract binary.
func writeFakeBinary(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("failed to write fake %s: %v", name, err)
	}
	return path
}

// slowBinaryBody sleeps far longer than any test waits. It runs sleep as a
// child instead of exec'ing it, so the child keeps stdout open after the
// shell is killed and only WaitDelay lets the call return.
const slowBinaryBody = "sleep 30\necho too late\n"

// assertStoppedByDeadline checks that a call stopped by a short context
// deadline reports it through errors.Is and came back in a few seconds.
func assertStoppedByDeadline(t *testing.T, err error, elapsed time.Duration) {
	t.Helper()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want an error wrapping context.DeadlineExceeded, got %v", err)
	}

	// The deadline is 300 ms and WaitDelay adds up to two seconds for the
	// orphaned sleep; anything near the 30 s of the script means no deadline.
	if elapsed > 5*time.Second {
		t.Errorf("call took %s, want it stopped shortly after the deadline", elapsed)
	}
}

func TestReadDocument_SlowPdftotextStopsAtDeadline(t *testing.T) {
	t.Parallel()

	r, dir := newTestReader(t)
	writeFile(t, dir, "slow.pdf", "%PDF-1.4 never parsed by the fake")
	r.pdftotextBin = writeFakeBinary(t, t.TempDir(), "pdftotext", slowBinaryBody)

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := r.ReadDocument(ctx, "slow.pdf", 0)
	assertStoppedByDeadline(t, err, time.Since(started))
}

func TestReadImage_SlowTesseractStopsAtDeadline(t *testing.T) {
	t.Parallel()

	r, dir := newTestReader(t)
	writeFile(t, dir, "slow.png", "not really a png")
	r.tesseractBin = writeFakeBinary(t, t.TempDir(), "tesseract", slowBinaryBody)

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := r.ReadImage(ctx, "slow.png", "eng")
	assertStoppedByDeadline(t, err, time.Since(started))
}

func TestReadDocument_ToolStderrIsBoundedAndNamesNoPath(t *testing.T) {
	r, dir := newTestReader(t)
	writeFile(t, dir, "broken.pdf", "%PDF-1.4 never parsed by the fake")

	// The fake complains about the path it was given, then floods stderr.
	r.pdftotextBin = writeFakeBinary(t, t.TempDir(), "pdftotext",
		"for arg; do case $arg in *.pdf) document=$arg;; esac; done\n"+
			"echo \"Syntax Error: Couldn't open file '$document'\" >&2\n"+
			"head -c 5000 /dev/zero | tr '\\0' 'x' >&2\n"+
			"exit 1\n")

	_, err := r.ReadDocument(t.Context(), "broken.pdf", 0)
	if err == nil {
		t.Fatal("ReadDocument with a failing pdftotext: want an error, got nil")
	}

	message := err.Error()
	if !strings.Contains(message, "Couldn't open file 'broken.pdf'") {
		t.Errorf("error should carry the stderr with the file name: %q", message)
	}
	if strings.Contains(message, dir) {
		t.Errorf("error leaks the documents directory %s: %q", dir, message)
	}
	if len(message) > stderrLimit+200 {
		t.Errorf("error is %d bytes long, want the stderr cut to %d", len(message), stderrLimit)
	}
}

// ---------------------------------------------------------------------------
// 23. Page ranges against the real page count
// ---------------------------------------------------------------------------

// writeTextPDF writes a minimal PDF with one page per entry of pageTexts,
// each drawn in Helvetica, so tests have real text PDFs without binary
// fixtures in the repository.
func writeTextPDF(t *testing.T, path string, pageTexts []string) {
	t.Helper()

	// Objects 1 and 2 are the catalog and the page tree, 3 is the font, and
	// every page takes two more: the page and its content stream.
	var objects []string
	var kids []string
	for index := range pageTexts {
		kids = append(kids, fmt.Sprintf("%d 0 R", 4+index*2))
	}
	objects = append(objects, "<< /Type /Catalog /Pages 2 0 R >>")
	objects = append(objects, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pageTexts)))
	objects = append(objects, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for index, text := range pageTexts {
		stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+index*2))
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
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
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

// longPageText has more than the 50 visible characters isTextEmpty asks of
// a page, so pdftotext's output counts as real text.
const longPageText = "This page of the test document carries enough real text to never look scanned."

// invocationLog records which fake binaries ran, one line per run.
type invocationLog struct {
	path string
}

// newInvocationLog creates an empty log file in a temporary directory.
func newInvocationLog(t *testing.T) *invocationLog {
	t.Helper()
	return &invocationLog{path: filepath.Join(t.TempDir(), "invocations.log")}
}

// record returns the shell line that appends name and its arguments.
func (log *invocationLog) record(name string) string {
	return fmt.Sprintf("echo \"%s $*\" >> '%s'\n", name, log.path)
}

// count returns how many times the binary called name ran.
func (log *invocationLog) count(t *testing.T, name string) int {
	t.Helper()
	data, err := os.ReadFile(log.path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("failed to read invocation log: %v", err)
	}

	runs := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, name+" ") {
			runs++
		}
	}
	return runs
}

// useFakeOCR points the reader at a pdftoppm that writes an empty TIFF and a
// tesseract that prints recognisedText, both logged, and turns OCR on.
func useFakeOCR(t *testing.T, r *Reader, log *invocationLog, recognisedText string) {
	t.Helper()
	bin := t.TempDir()

	// pdftoppm's last argument is the output prefix; -singlefile adds .tif.
	r.pdftoppmBin = writeFakeBinary(t, bin, "pdftoppm",
		log.record("pdftoppm")+"for arg; do prefix=$arg; done\n: > \"$prefix.tif\"\n")
	r.tesseractBin = writeFakeBinary(t, bin, "tesseract",
		log.record("tesseract")+"cat > /dev/null\necho '"+recognisedText+"'\n")
	r.hasOCR = true
}

func TestReadDocumentPages_RangePastTheEndOfATextPDFRunsNoOCR(t *testing.T) {
	r, dir := newTestReader(t)
	realPdftotext, err := exec.LookPath(r.pdftotextBin)
	if err != nil {
		t.Skip("poppler not installed")
	}
	if _, err := exec.LookPath(r.pdfinfoBin); err != nil {
		t.Skip("poppler not installed")
	}

	writeTextPDF(t, filepath.Join(dir, "one.pdf"), []string{longPageText})
	log := newInvocationLog(t)
	useFakeOCR(t, r, log, "should never be used")
	r.pdftotextBin = writeFakeBinary(t, t.TempDir(), "pdftotext",
		log.record("pdftotext")+"exec '"+realPdftotext+"' \"$@\"\n")

	got, err := r.ReadDocumentPages(t.Context(), "one.pdf", "1-60")
	if err != nil {
		t.Fatalf("ReadDocumentPages(1-60) on a one-page PDF: %v", err)
	}

	if !strings.Contains(got, "enough real text") {
		t.Errorf("want the page text, got %q", got)
	}
	if runs := log.count(t, "pdftoppm"); runs != 0 {
		t.Errorf("pdftoppm ran %d times on a text PDF, want 0", runs)
	}
	if runs := log.count(t, "pdftotext"); runs != 1 {
		t.Errorf("pdftotext ran %d times, want 1", runs)
	}
}

func TestReadDocumentPages_ScannedPDFOnlyOCRsPagesThatExist(t *testing.T) {
	r, dir := newTestReader(t)
	writeFile(t, dir, "scan.pdf", "%PDF-1.4 never parsed by the fakes")

	log := newInvocationLog(t)
	useFakeOCR(t, r, log, "recognised by the fake tesseract")
	bin := t.TempDir()
	r.pdfinfoBin = writeFakeBinary(t, bin, "pdfinfo", log.record("pdfinfo")+"echo 'Pages:          1'\n")
	r.pdftotextBin = writeFakeBinary(t, bin, "pdftotext", log.record("pdftotext"))

	got, err := r.ReadDocumentPages(t.Context(), "scan.pdf", "1-60")
	if err != nil {
		t.Fatalf("ReadDocumentPages(1-60) on a one-page scan: %v", err)
	}

	if !strings.Contains(got, "recognised by the fake tesseract") {
		t.Errorf("want the OCR text, got %q", got)
	}
	if runs := log.count(t, "pdftoppm"); runs != 1 {
		t.Errorf("pdftoppm ran %d times for a one-page document, want 1", runs)
	}
	if runs := log.count(t, "tesseract"); runs != 1 {
		t.Errorf("tesseract ran %d times for a one-page document, want 1", runs)
	}
	if runs := log.count(t, "pdfinfo"); runs != 1 {
		t.Errorf("pdfinfo ran %d times, want 1", runs)
	}
}

func TestReadDocumentPages_RejectsPagesPastTheEnd(t *testing.T) {
	r, dir := newTestReader(t)
	writeFile(t, dir, "scan.pdf", "%PDF-1.4 never parsed by the fakes")

	log := newInvocationLog(t)
	bin := t.TempDir()
	r.pdfinfoBin = writeFakeBinary(t, bin, "pdfinfo", "echo 'Pages:          1'\n")
	r.pdftotextBin = writeFakeBinary(t, bin, "pdftotext", log.record("pdftotext"))

	_, err := r.ReadDocumentPages(t.Context(), "scan.pdf", "5-9")
	if err == nil || !strings.Contains(err.Error(), "has 1 page") {
		t.Errorf("ReadDocumentPages(5-9) on a one-page PDF: want an error naming the page count, got %v", err)
	}

	_, err = r.ReadDocument(t.Context(), "scan.pdf", 3)
	if err == nil || !strings.Contains(err.Error(), "has 1 page") {
		t.Errorf("ReadDocument(page 3) on a one-page PDF: want an error naming the page count, got %v", err)
	}

	if runs := log.count(t, "pdftotext"); runs != 0 {
		t.Errorf("pdftotext ran %d times for pages that do not exist, want 0", runs)
	}
}

func TestReadDocumentPages_CapsPagesPerCall(t *testing.T) {
	r, dir := newTestReader(t)
	writeFile(t, dir, "long.pdf", "%PDF-1.4 never parsed by the fakes")

	log := newInvocationLog(t)
	bin := t.TempDir()
	r.pdfinfoBin = writeFakeBinary(t, bin, "pdfinfo", "echo 'Pages:          500'\n")
	r.pdftotextBin = writeFakeBinary(t, bin, "pdftotext", log.record("pdftotext"))

	_, err := r.ReadDocumentPages(t.Context(), "long.pdf", "1-150,300-360")
	if err == nil || !strings.Contains(err.Error(), "at most 200 pages") {
		t.Errorf("211 pages in one call: want an 'at most 200 pages' error, got %v", err)
	}
	if runs := log.count(t, "pdftotext"); runs != 0 {
		t.Errorf("pdftotext ran %d times for a refused request, want 0", runs)
	}
}

func TestClampPageRanges(t *testing.T) {
	tests := []struct {
		name      string
		ranges    [][2]int
		pageCount int
		want      [][2]int
		wantErr   string
	}{
		{"inside", [][2]int{{1, 3}}, 10, [][2]int{{1, 3}}, ""},
		{"cut at the last page", [][2]int{{1, 60}}, 1, [][2]int{{1, 1}}, ""},
		{"several ranges", [][2]int{{2, 4}, {8, 20}}, 10, [][2]int{{2, 4}, {8, 10}}, ""},
		{"starts past the end", [][2]int{{1, 1}, {12, 14}}, 10, nil, "has 10 page"},
		{"exactly the cap", [][2]int{{1, 200}}, 500, [][2]int{{1, 200}}, ""},
		{"over the cap", [][2]int{{1, 201}}, 500, nil, "at most 200 pages"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := clampPageRanges(tc.ranges, tc.pageCount)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("clampPageRanges: want an error with %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("clampPageRanges: %v", err)
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("clampPageRanges = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 24. OCR language
// ---------------------------------------------------------------------------

func TestOCRLanguage(t *testing.T) {
	tests := []struct {
		name        string
		requested   string
		environment string
		want        string
		wantErr     bool
	}{
		{"default", "", "", "eng", false},
		{"environment", "", "spa", "spa", false},
		{"call wins over environment", "fra", "spa", "fra", false},
		{"several languages", "eng+spa", "", "eng+spa", false},
		{"script variant", "chi_sim", "", "chi_sim", false},
		{"uppercase", "ENG", "", "", true},
		{"too short", "en", "", "", true},
		{"too long", "eng+spa+fra+deu+ita+por", "", "", true},
		{"option injection", "-l eng", "", "", true},
		{"path", "../eng", "", "", true},
		{"shell", "eng;rm", "", "", true},
		{"invalid environment", "", "Spanish!", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DOCS_MCP_OCR_LANG", tc.environment)

			got, err := ocrLanguage(tc.requested)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "invalid OCR language") {
					t.Fatalf("ocrLanguage(%q): want an 'invalid OCR language' error, got %q, %v", tc.requested, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ocrLanguage(%q): %v", tc.requested, err)
			}
			if got != tc.want {
				t.Errorf("ocrLanguage(%q) = %q, want %q", tc.requested, got, tc.want)
			}
		})
	}
}

func TestOCR_InvalidLanguageNeverReachesTesseract(t *testing.T) {
	r, dir := newTestReader(t)
	writeFile(t, dir, "scan.pdf", "%PDF-1.4 never parsed by the fakes")
	writeFile(t, dir, "photo.png", "not really a png")

	log := newInvocationLog(t)
	useFakeOCR(t, r, log, "should never be used")

	_, err := r.OCRDocument(t.Context(), "scan.pdf", 1, "eng -c tessedit_write_images=1")
	if err == nil || !strings.Contains(err.Error(), "invalid OCR language") {
		t.Errorf("OCRDocument with a bad language: want 'invalid OCR language', got %v", err)
	}

	_, err = r.ReadImage(t.Context(), "photo.png", "../../eng")
	if err == nil || !strings.Contains(err.Error(), "invalid OCR language") {
		t.Errorf("ReadImage with a bad language: want 'invalid OCR language', got %v", err)
	}

	if runs := log.count(t, "tesseract") + log.count(t, "pdftoppm"); runs != 0 {
		t.Errorf("OCR binaries ran %d times for a refused language, want 0", runs)
	}
}

func TestOCR_FallbackUsesEnvironmentLanguage(t *testing.T) {
	t.Setenv("DOCS_MCP_OCR_LANG", "spa")

	r, dir := newTestReader(t)
	writeFile(t, dir, "scan.pdf", "%PDF-1.4 never parsed by the fakes")

	log := newInvocationLog(t)
	useFakeOCR(t, r, log, "texto reconocido")
	bin := t.TempDir()
	r.pdfinfoBin = writeFakeBinary(t, bin, "pdfinfo", "echo 'Pages:          1'\n")
	r.pdftotextBin = writeFakeBinary(t, bin, "pdftotext", "")

	if _, err := r.ReadDocument(t.Context(), "scan.pdf", 0); err != nil {
		t.Fatalf("ReadDocument on a scan: %v", err)
	}

	data, err := os.ReadFile(log.path)
	if err != nil {
		t.Fatalf("failed to read invocation log: %v", err)
	}
	if !strings.Contains(string(data), "tesseract stdin stdout -l spa") {
		t.Errorf("the OCR fallback should use DOCS_MCP_OCR_LANG=spa, tesseract ran as:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// 25. Where the binaries are looked for
// ---------------------------------------------------------------------------

func TestFindBinary_TriesTheHomebrewDirsBeforePATH(t *testing.T) {
	empty := t.TempDir()
	withPandoc := t.TempDir()
	fake := writeFakeBinary(t, withPandoc, "pandoc", "")

	if got := findBinary("pandoc", []string{empty, withPandoc}); got != fake {
		t.Errorf("findBinary = %q, want %q", got, fake)
	}
	if got := findBinary("pandoc", []string{empty}); got != "pandoc" {
		t.Errorf("findBinary with no candidate = %q, want the bare name for PATH", got)
	}
}

func TestReadDocument_Docx_FindsHomebrewPandocWithoutPATH(t *testing.T) {
	pandoc := findBinary("pandoc", binaryDirs)
	if pandoc == "pandoc" {
		t.Skip("no pandoc in /opt/homebrew/bin or /usr/local/bin")
	}

	r, dir := newTestReader(t)
	source := filepath.Join(dir, "source.md")
	writeFile(t, dir, "source.md", "# Heading\n\nBody text from docx.")
	if out, err := exec.Command(pandoc, source, "-o", filepath.Join(dir, "doc.docx")).CombinedOutput(); err != nil {
		t.Skipf("pandoc could not build the fixture: %v %s", err, out)
	}

	// The bare PATH an MCP client may give the server.
	t.Setenv("PATH", t.TempDir())
	r = NewReader(dir)

	got, err := r.ReadDocument(t.Context(), "doc.docx", 0)
	if err != nil {
		t.Fatalf("ReadDocument(.docx) with an empty PATH: %v", err)
	}
	if !strings.Contains(got, "Body text from docx") {
		t.Errorf("docx text not extracted: %q", got)
	}

	formats := r.ListFormats()
	for _, format := range formats {
		if format.Extension == ".docx" && !format.Installed {
			t.Errorf("ListFormats reports pandoc missing although %s exists", pandoc)
		}
	}
}

// ---------------------------------------------------------------------------
// 26. Errors name files, not where they live
// ---------------------------------------------------------------------------

// assertNoAbsolutePath fails when an error message shows the directory.
func assertNoAbsolutePath(t *testing.T, what string, err error, dir string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: want an error, got nil", what)
	}
	if strings.Contains(err.Error(), dir) {
		t.Errorf("%s leaks the path %s: %q", what, dir, err)
	}
}

func TestErrors_DoNotLeakAbsolutePaths(t *testing.T) {
	r, dir := newTestReader(t)

	// A file the server cannot open fails on read, after it was found.
	writeFile(t, dir, "locked.md", "# Locked")
	if err := os.Chmod(filepath.Join(dir, "locked.md"), 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(filepath.Join(dir, "locked.md"), 0o644)
	})

	_, err := r.ReadDocument(t.Context(), "locked.md", 0)
	assertNoAbsolutePath(t, "ReadDocument(unreadable)", err, dir)
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("the cause should stay in the message: %q", err)
	}

	_, err = r.GetDocumentOutline(t.Context(), "locked.md")
	assertNoAbsolutePath(t, "GetDocumentOutline(unreadable)", err, dir)

	_, err = r.ExtractTables(t.Context(), "locked.md", 0)
	assertNoAbsolutePath(t, "ExtractTables(unreadable)", err, dir)

	_, err = r.ConvertToMarkdown(t.Context(), "locked.md")
	assertNoAbsolutePath(t, "ConvertToMarkdown(unreadable)", err, dir)

	// A documents directory that is gone.
	missing := filepath.Join(dir, "missing-dir")
	gone := NewReader(missing)

	_, err = gone.ListDocuments(t.Context())
	assertNoAbsolutePath(t, "ListDocuments(missing dir)", err, missing)

	_, err = gone.ReadDocument(t.Context(), "any.txt", 0)
	assertNoAbsolutePath(t, "ReadDocument(missing dir)", err, missing)

	// A downloaded file that is not there.
	_, err = r.ReadFile(t.Context(), filepath.Join(dir, "download.pdf"))
	assertNoAbsolutePath(t, "ReadFile(missing)", err, dir)

	_, err = r.ReadFilePages(t.Context(), filepath.Join(dir, "download.pdf"), "1")
	assertNoAbsolutePath(t, "ReadFilePages(missing)", err, dir)
}

// ---------------------------------------------------------------------------
// 27. ocr_document is for PDFs
// ---------------------------------------------------------------------------

func TestOCRDocument_RejectsNonPDFFiles(t *testing.T) {
	r, dir := newTestReader(t)
	log := newInvocationLog(t)
	useFakeOCR(t, r, log, "should never be used")

	for _, name := range []string{"photo.png", "notes.md", "letter.docx"} {
		writeFile(t, dir, name, "not a PDF")

		_, err := r.OCRDocument(t.Context(), name, 0, "")
		if err == nil || !strings.Contains(err.Error(), "ocr_document only works on PDF files; use read_image for images") {
			t.Errorf("OCRDocument(%s): want the 'only works on PDF files' error, got %v", name, err)
		}
	}

	if runs := log.count(t, "tesseract") + log.count(t, "pdftoppm"); runs != 0 {
		t.Errorf("OCR binaries ran %d times for files that are not PDFs, want 0", runs)
	}
}

// ---------------------------------------------------------------------------
// 28. Page markers in OCR text
// ---------------------------------------------------------------------------

func TestJoinOCRPages_EndsEveryPageWithAFormFeed(t *testing.T) {
	// pdftotext ends every page with "\n\f"; OCR text must look the same so
	// that search, outlines and tables count pages the same way. A page
	// whose text has no final newline still gets one before the marker.
	got := joinOCRPages([]string{"first page\n", "second page", "[OCR failed on page 3: boom]"})

	want := "first page\n\fsecond page\n\f[OCR failed on page 3: boom]\n\f"
	if got != want {
		t.Errorf("joinOCRPages = %q, want %q", got, want)
	}
}

// useFakePagedOCR points the reader at a pdftoppm that writes the page
// number into its TIFF and a tesseract that prints what it reads, so every
// page of a fake scan "recognises" as its own text.
func useFakePagedOCR(t *testing.T, r *Reader, pageCount int) {
	t.Helper()
	bin := t.TempDir()

	r.pdfinfoBin = writeFakeBinary(t, bin, "pdfinfo", fmt.Sprintf("echo 'Pages:          %d'\n", pageCount))
	r.pdftotextBin = writeFakeBinary(t, bin, "pdftotext", "")
	r.pdftoppmBin = writeFakeBinary(t, bin, "pdftoppm",
		"for arg; do prefix=$arg; done\n"+
			"while [ $# -gt 0 ]; do if [ \"$1\" = -f ]; then page=$2; fi; shift; done\n"+
			"if [ \"$page\" = 2 ]; then word=needle; else word=hay; fi\n"+
			"printf 'Scanned page %s line one\\nscanned page %s with %s\\n' \"$page\" \"$page\" \"$word\" > \"$prefix.tif\"\n")
	r.tesseractBin = writeFakeBinary(t, bin, "tesseract", "cat\n")
	r.hasOCR = true
}

func TestSearchDocument_OCRTextReportsTheRightPage(t *testing.T) {
	r, dir := newTestReader(t)
	writeFile(t, dir, "scan.pdf", "%PDF-1.4 never parsed by the fakes")
	useFakePagedOCR(t, r, 3)

	got, err := r.SearchDocument(t.Context(), "scan.pdf", "needle")
	if err != nil {
		t.Fatalf("SearchDocument on a scanned PDF: %v", err)
	}

	if !strings.Contains(got, "Found 1 matches") || !strings.Contains(got, "page ~2") {
		t.Errorf("want one match reported on page 2, got:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// 29. Formats read through pandoc
// ---------------------------------------------------------------------------

// fixtureMarkdown is the source pandoc turns into the office and e-book
// fixtures: a heading and a paragraph with bold text, so a test can tell
// Markdown output (# and **) from plain text.
const fixtureMarkdown = "# Heading\n\nBody text with **bold** words.\n"

// requirePandoc returns the pandoc the reader would use, or skips the test
// with a message that says pandoc is missing, so the log never shows a
// pandoc test as passed on a machine that could not run it.
func requirePandoc(t *testing.T) string {
	t.Helper()
	pandoc := findBinary("pandoc", binaryDirs)
	if _, err := exec.LookPath(pandoc); err != nil {
		t.Skip("pandoc not installed: skipping the test of the pandoc-backed formats")
	}
	return pandoc
}

// writePandocOutput has pandoc write fixtureMarkdown as the format of name's
// extension into dir.
func writePandocOutput(t *testing.T, pandoc, dir, name string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source.md")
	if err := os.WriteFile(source, []byte(fixtureMarkdown), 0o644); err != nil {
		t.Fatalf("failed to write the pandoc source: %v", err)
	}

	output, err := exec.Command(pandoc, source, "-o", filepath.Join(dir, name)).CombinedOutput()
	if err != nil {
		t.Fatalf("pandoc could not write %s: %v %s", name, err, output)
	}
}

// writeMinimalXLSX writes the smallest workbook pandoc reads: one sheet with
// a header row of two shared strings and one numeric cell. pandoc can read
// xlsx but not write it, and it ignores inline strings, hence the shared
// strings part.
func writeMinimalXLSX(t *testing.T, path string) {
	t.Helper()

	parts := []struct {
		name    string
		content string
	}{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/></Relationships>`},
		{"xl/sharedStrings.xml", `<?xml version="1.0" encoding="UTF-8"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="2" uniqueCount="2"><si><t>Name</t></si><si><t>Body text with bold words</t></si></sst>`},
		{"xl/worksheets/sheet1.xml", `<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row><row r="2"><c r="A2"><v>42</v></c></row></sheetData></worksheet>`},
	}

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create %s: %v", path, err)
	}
	defer file.Close()

	archive := zip.NewWriter(file)
	for _, part := range parts {
		writer, err := archive.Create(part.name)
		if err != nil {
			t.Fatalf("failed to add %s to the workbook: %v", part.name, err)
		}
		if _, err := writer.Write([]byte(part.content)); err != nil {
			t.Fatalf("failed to write %s: %v", part.name, err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("failed to finish the workbook: %v", err)
	}
}

// pandocFixtureHTML is a page with the same heading and paragraph as
// fixtureMarkdown.
const pandocFixtureHTML = "<!doctype html><html><head><title>Fixture</title></head><body><h1>Heading</h1><p>Body text with <b>bold</b> words.</p></body></html>"

// pandocFixtureRTF is a paragraph with a bold run; pandoc keeps the bold.
const pandocFixtureRTF = `{\rtf1\ansi Body text with {\b bold} words.\par}`

// pandocFixtureNotebook is a notebook with one Markdown cell.
const pandocFixtureNotebook = `{"cells":[{"cell_type":"markdown","metadata":{},"source":["# Heading\n","\n","Body text with **bold** words."]}],"metadata":{},"nbformat":4,"nbformat_minor":5}`

// pandocFixtureCase is one pandoc-backed format with the Markdown that
// convert_to_markdown must produce from its fixture.
type pandocFixtureCase struct {
	name         string
	write        func(t *testing.T, pandoc, dir, name string)
	wantMarkdown string
}

// writeTextFixture returns a fixture writer for a format written by hand.
func writeTextFixture(content string) func(t *testing.T, pandoc, dir, name string) {
	return func(t *testing.T, pandoc, dir, name string) {
		writeFile(t, dir, name, content)
	}
}

// pandocFixtureCases covers every extension that goes through pandoc.
var pandocFixtureCases = []pandocFixtureCase{
	{"letter.docx", writePandocOutput, "# Heading"},
	{"slides.pptx", writePandocOutput, "## Heading"},
	{"essay.odt", writePandocOutput, "**bold**"},
	{"book.epub", writePandocOutput, "# Heading"},
	{"sheet.xlsx", func(t *testing.T, pandoc, dir, name string) { writeMinimalXLSX(t, filepath.Join(dir, name)) }, "| Name |"},
	{"page.html", writeTextFixture(pandocFixtureHTML), "# Heading"},
	{"old-page.htm", writeTextFixture(pandocFixtureHTML), "**bold**"},
	{"memo.rtf", writeTextFixture(pandocFixtureRTF), "**bold**"},
	{"analysis.ipynb", writeTextFixture(pandocFixtureNotebook), "# Heading"},
}

func TestReadDocument_PandocFormats(t *testing.T) {
	pandoc := requirePandoc(t)

	for _, testCase := range pandocFixtureCases {
		t.Run(testCase.name, func(t *testing.T) {
			r, dir := newTestReader(t)
			testCase.write(t, pandoc, dir, testCase.name)

			got, err := r.ReadDocument(t.Context(), testCase.name, 0)
			if err != nil {
				t.Fatalf("ReadDocument(%s): %v", testCase.name, err)
			}
			if !strings.Contains(got, "Body text with") {
				t.Errorf("ReadDocument(%s) misses the body text: %q", testCase.name, got)
			}

			// read_document returns plain text, without Markdown marks.
			if strings.Contains(got, "**") {
				t.Errorf("ReadDocument(%s) should be plain text, got %q", testCase.name, got)
			}
		})
	}
}

func TestReadDocument_PandocFormatsRequirePandoc(t *testing.T) {
	r, dir := newTestReader(t)

	// A pandoc that is not there, whatever this machine has installed.
	r.pandocBin = filepath.Join(dir, "no-such-pandoc")

	for _, testCase := range pandocFixtureCases {
		writeFile(t, dir, testCase.name, "not really this format")

		_, err := r.ReadDocument(t.Context(), testCase.name, 0)
		wantExtension := filepath.Ext(testCase.name)
		if err == nil || !strings.Contains(err.Error(), "pandoc") || !strings.Contains(err.Error(), wantExtension) {
			t.Errorf("ReadDocument(%s) without pandoc: want an error naming pandoc and %s, got %v", testCase.name, wantExtension, err)
		}
	}
}

func TestPandocFormats_AreListedEverywhere(t *testing.T) {
	r, dir := newTestReader(t)

	formats := map[string]FormatInfo{}
	for _, format := range r.ListFormats() {
		formats[format.Extension] = format
	}

	for _, testCase := range pandocFixtureCases {
		writeFile(t, dir, testCase.name, "listed whatever it holds")
		extension := filepath.Ext(testCase.name)

		if _, err := r.sanitizeFilename(testCase.name); err != nil {
			t.Errorf("sanitizeFilename(%s): %v", testCase.name, err)
		}

		format, ok := formats[extension]
		if !ok {
			t.Errorf("ListFormats misses %s", extension)
			continue
		}
		if format.Requires != "pandoc" || format.Status != "optional" {
			t.Errorf("ListFormats %s = %+v, want optional and requiring pandoc", extension, format)
		}
	}

	docs, err := r.ListDocuments(t.Context())
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != len(pandocFixtureCases) {
		t.Errorf("ListDocuments listed %d files, want the %d pandoc fixtures: %+v", len(docs), len(pandocFixtureCases), docs)
	}
}

func TestConvertToMarkdown_PandocFormatsGiveGFM(t *testing.T) {
	pandoc := requirePandoc(t)

	for _, testCase := range pandocFixtureCases {
		t.Run(testCase.name, func(t *testing.T) {
			r, dir := newTestReader(t)
			testCase.write(t, pandoc, dir, testCase.name)

			got, err := r.ConvertToMarkdown(t.Context(), testCase.name)
			if err != nil {
				t.Fatalf("ConvertToMarkdown(%s): %v", testCase.name, err)
			}
			if !strings.Contains(got, testCase.wantMarkdown) {
				t.Errorf("ConvertToMarkdown(%s) = %q, want GitHub Markdown with %q", testCase.name, got, testCase.wantMarkdown)
			}

			// The plain-text header of the old path is gone: pandoc's output
			// is already the document as Markdown.
			if strings.Contains(got, "> Converted from") {
				t.Errorf("ConvertToMarkdown(%s) still wraps plain text: %q", testCase.name, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 30. Document types in the listing
// ---------------------------------------------------------------------------

func TestListDocuments_ListsImagesAndNamesEveryType(t *testing.T) {
	r, dir := newTestReader(t)

	// The PDF is not parsed by the fake pdfinfo, which reports one page.
	r.pdfinfoBin = writeFakeBinary(t, t.TempDir(), "pdfinfo", "echo 'Pages:          1'\n")

	wantTypes := map[string]string{
		"report.pdf":   "pdf",
		"report.txt":   "text",
		"notes.md":     "markdown",
		"data.csv":     "csv",
		"letter.docx":  "docx",
		"sheet.xlsx":   "xlsx",
		"slides.pptx":  "pptx",
		"essay.odt":    "odt",
		"book.epub":    "epub",
		"page.html":    "html",
		"old-page.htm": "html",
		"memo.rtf":     "rtf",
		"lab.ipynb":    "ipynb",
		"photo.png":    "image",
		"scan.JPG":     "image",
		"shot.jpeg":    "image",
		"web.webp":     "image",
		"fax.tiff":     "image",
		"fax2.tif":     "image",
		"icon.bmp":     "image",
	}
	for name := range wantTypes {
		writeFile(t, dir, name, "listed whatever it holds")
	}

	docs, err := r.ListDocuments(t.Context())
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}

	gotTypes := map[string]string{}
	for _, doc := range docs {
		gotTypes[doc.Filename] = doc.Type
	}
	for name, wantType := range wantTypes {
		gotType, listed := gotTypes[name]
		if !listed {
			t.Errorf("ListDocuments misses %s", name)
			continue
		}
		if gotType != wantType {
			t.Errorf("%s type = %q, want %q", name, gotType, wantType)
		}
	}
}

func TestDocumentInfo_TypeIsInTheJSON(t *testing.T) {
	data, err := json.Marshal(DocumentInfo{Filename: "photo.png", Type: "image"})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"type":"image"`) {
		t.Errorf("DocumentInfo JSON = %s, want a \"type\" field", data)
	}
}

func TestWebP_IsAnImageFormat(t *testing.T) {
	r, _ := newTestReader(t)

	if _, err := r.sanitizeImageFilename("diagram.webp"); err != nil {
		t.Errorf("sanitizeImageFilename(.webp): %v", err)
	}

	listed := false
	for _, format := range r.ListFormats() {
		if format.Requires == "tesseract" && strings.Contains(format.Extension, ".webp") {
			listed = true
		}
	}
	if !listed {
		t.Errorf("ListFormats should name .webp among the tesseract formats: %+v", r.ListFormats())
	}
}

package pdf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	docs, err := r.ListDocuments()
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

	docs, err := r.ListDocuments()
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

	docs, err := r.ListDocuments()
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

	docs, err := r.ListDocuments()
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

func TestReadDocument_TextFile_FailsWithPdftotext(t *testing.T) {
	r, dir := newTestReader(t)

	// ReadDocument routes ALL files through pdftotext (extractFull -> readWithOCRFallback -> runPdftotext).
	// For non-PDF files (.txt, .md, .csv), pdftotext will fail because it only handles PDFs.
	// This is expected behavior — the MCP wrappers in main.go validate format appropriateness.
	content := "Line one\nLine two\nLine three"
	writeFile(t, dir, "sample.txt", content)

	_, err := r.ReadDocument("sample.txt", 0)
	// pdftotext cannot read .txt files — expect an error
	if err == nil {
		t.Fatal("ReadDocument(.txt) expected error since pdftotext cannot read text files, got nil")
	}
	if !strings.Contains(err.Error(), "pdftotext") {
		t.Errorf("expected pdftotext-related error, got: %v", err)
	}
}

func TestReadDocument_SanitizesFilenameFirst(t *testing.T) {
	r, dir := newTestReader(t)

	// Verify that sanitization happens BEFORE file-not-found check
	writeFile(t, dir, "legit.txt", "content")

	_, err := r.ReadDocument("../escape.txt", 0)
	if err == nil {
		t.Error("ReadDocument() should reject directory traversal")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected 'traversal' error, got: %v", err)
	}
}

func TestReadDocument_NotFound(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ReadDocument("nonexistent.txt", 0)
	if err == nil {
		t.Error("ReadDocument() for nonexistent file: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestReadDocument_InvalidFilename(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ReadDocument("../escape.txt", 0)
	if err == nil {
		t.Error("ReadDocument() with traversal: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected 'traversal' in error, got: %v", err)
	}
}

func TestReadDocument_UnsupportedExtension(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ReadDocument("malware.exe", 0)
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

	_, err := r.SearchDocument("../etc/passwd", "root")
	if err == nil {
		t.Error("SearchDocument() with traversal: expected error, got nil")
	}
}

func TestSearchDocument_NotFound(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.SearchDocument("missing.txt", "hello")
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

	_, err := r.GetDocumentSummary("../bad.txt")
	if err == nil {
		t.Error("GetDocumentSummary() with traversal: expected error, got nil")
	}
}

func TestGetDocumentSummary_NotFound(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.GetDocumentSummary("missing.txt")
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

	got, err := r.ConvertToMarkdown("readme.md")
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

	got, err := r.ConvertToMarkdown("note.txt")
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

	got, err := r.ConvertToMarkdown("people.csv")
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

	_, err := r.ConvertToMarkdown("ghost.md")
	if err == nil {
		t.Error("ConvertToMarkdown() for missing file: expected error, got nil")
	}
}

func TestConvertToMarkdown_InvalidFilename(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ConvertToMarkdown("../escape.md")
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

	entries, err := r.GetDocumentOutline("outline.md")
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

	entries, err := r.GetDocumentOutline("plain.txt")
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

	entries, err := r.GetDocumentOutline("mixed.txt")
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

	_, err := r.GetDocumentOutline("missing.md")
	if err == nil {
		t.Error("GetDocumentOutline() for missing file: expected error, got nil")
	}
}

func TestGetDocumentOutline_UnsupportedFormat(t *testing.T) {
	r, dir := newTestReader(t)

	writeFile(t, dir, "data.csv", "a,b,c")

	_, err := r.GetDocumentOutline("data.csv")
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

	tables, err := r.ExtractTables("people.csv", 0)
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

	tables, err := r.ExtractTables("quoted.csv", 0)
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

	tables, err := r.ExtractTables("table.md", 0)
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

	tables, err := r.ExtractTables("empty.csv", 0)
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

	tables, err := r.ExtractTables("blank.csv", 0)
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

	tables, err := r.ExtractTables("tabbed.txt", 0)
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

	_, err := r.ExtractTables("missing.csv", 0)
	if err == nil {
		t.Error("ExtractTables() for missing file: expected error, got nil")
	}
}

func TestExtractTables_UnsupportedFormat(t *testing.T) {
	r, dir := newTestReader(t)

	writeFile(t, dir, "doc.docx", "fake docx")

	_, err := r.ExtractTables("doc.docx", 0)
	if err == nil {
		t.Error("ExtractTables(.docx) should return unsupported format error")
	}
}

// ---------------------------------------------------------------------------
// 9. ReadImage (validation only — no actual OCR)
// ---------------------------------------------------------------------------

func TestReadImage_InvalidExtension(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ReadImage("document.pdf", "eng")
	if err == nil {
		t.Error("ReadImage(.pdf) should reject non-image extension")
	}
	if !strings.Contains(err.Error(), "unsupported image extension") {
		t.Errorf("expected 'unsupported image extension' in error, got: %v", err)
	}
}

func TestReadImage_DirectoryTraversal(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ReadImage("../secret.png", "eng")
	if err == nil {
		t.Error("ReadImage() with traversal should be rejected")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected 'traversal' in error, got: %v", err)
	}
}

func TestReadImage_NotFound(t *testing.T) {
	r, _ := newTestReader(t)

	_, err := r.ReadImage("missing.png", "eng")
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
		{"", 1, true},                       // empty
		{"   \n\t\r  ", 1, true},            // whitespace only
		{"hello world", 1, true},            // 10 chars < 50 threshold
		{strings.Repeat("x", 50), 1, false}, // exactly 50 non-ws chars
		{strings.Repeat("x", 49), 1, true},  // 49 < 50
		{strings.Repeat("x", 100), 2, false}, // 100 >= 100 (50*2)
		{strings.Repeat("x", 99), 2, true},   // 99 < 100
		{"abc", 0, true},                      // pageCount 0 treated as 1
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
		{"Hello", 0},   // single word, no number prefix
		{"1.", 0},       // only number, no text after
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
// 14. splitCSVLine (internal function — unit test)
// ---------------------------------------------------------------------------

func TestSplitCSVLine(t *testing.T) {
	tests := []struct {
		line     string
		expected []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{`"hello, world",foo,bar`, []string{"hello, world", "foo", "bar"}},
		{`"a""b",c`, []string{`a"b`, "c"}},
		{" a , b , c ", []string{"a", "b", "c"}},
		{"single", []string{"single"}},
		{"", []string{""}},
	}

	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			got := splitCSVLine(tc.line)
			if len(got) != len(tc.expected) {
				t.Fatalf("splitCSVLine(%q) = %v (len %d), want %v (len %d)", tc.line, got, len(got), tc.expected, len(tc.expected))
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("splitCSVLine(%q)[%d] = %q, want %q", tc.line, i, got[i], tc.expected[i])
				}
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

package pdf

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// DocumentInfo holds metadata about a file in the documents directory. Type
// is the kind of document, see documentType, and Pages is 0 for anything
// that is not a PDF.
type DocumentInfo struct {
	Filename  string `json:"filename"`
	Title     string `json:"title"`
	Type      string `json:"type"`
	Pages     int    `json:"pages"`
	SizeBytes int64  `json:"size_bytes"`
}

// DocumentMetadata holds full PDF metadata from pdfinfo.
type DocumentMetadata struct {
	Title        string `json:"title"`
	Author       string `json:"author"`
	Subject      string `json:"subject"`
	Creator      string `json:"creator"`
	Producer     string `json:"producer"`
	CreationDate string `json:"creation_date"`
	ModDate      string `json:"modification_date"`
	Pages        int    `json:"pages"`
	FileSize     int64  `json:"file_size_bytes"`
	PDFVersion   string `json:"pdf_version"`
}

// cacheEntry stores extracted text with its modification time for invalidation.
// It keeps its own filename so an evicted list element can be removed from the
// map without searching it.
type cacheEntry struct {
	filename string
	text     string
	mtime    time.Time
}

// defaultCacheMegabytes is the text cache cap when DOCS_MCP_CACHE_MB is not
// set. Extracted text is a few hundred kilobytes for a long report, so 256 MB
// keeps hundreds of documents warm without letting a library of scanned books
// take the machine's memory.
const defaultCacheMegabytes = 256

// cacheLimit returns the text cache cap in bytes from DOCS_MCP_CACHE_MB. Zero
// turns the cache off; an empty, negative or unreadable value falls back to
// defaultCacheMegabytes, because a typo must not disable the cache silently or
// make it unbounded.
func cacheLimit() int64 {
	megabytes, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("DOCS_MCP_CACHE_MB")), 10, 64)
	if err != nil || megabytes < 0 {
		megabytes = defaultCacheMegabytes
	}

	return megabytes << 20
}

// textCache keeps extracted text in memory up to limit bytes and evicts the
// least recently used document first. The size counts the text alone: the
// key and the bookkeeping are small next to a document.
type textCache struct {
	mu      sync.Mutex
	limit   int64
	size    int64
	order   *list.List
	entries map[string]*list.Element
}

// newTextCache returns an empty cache that holds at most limit bytes of text.
func newTextCache(limit int64) *textCache {
	return &textCache{
		limit:   limit,
		order:   list.New(),
		entries: make(map[string]*list.Element),
	}
}

// get returns the cached text of filename when it was extracted from the
// version with mtime, and marks it as the most recently used.
func (c *textCache) get(filename string, mtime time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	element, ok := c.entries[filename]
	if !ok {
		return "", false
	}

	entry := element.Value.(*cacheEntry)
	if !entry.mtime.Equal(mtime) {
		return "", false
	}

	c.order.MoveToFront(element)

	return entry.text, true
}

// put stores text for filename at mtime, replacing an older version, and
// evicts the least recently used documents until the cache fits its limit.
// A text larger than the whole limit is not stored, so one huge document
// cannot empty the cache for nothing.
func (c *textCache) put(filename string, mtime time.Time, text string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	textSize := int64(len(text))
	if textSize > c.limit {
		return
	}

	if element, ok := c.entries[filename]; ok {
		c.remove(element)
	}

	entry := &cacheEntry{filename: filename, text: text, mtime: mtime}
	c.entries[filename] = c.order.PushFront(entry)
	c.size += textSize

	for c.size > c.limit {
		c.remove(c.order.Back())
	}
}

// remove drops element from the list, the map and the size. The caller holds
// the lock.
func (c *textCache) remove(element *list.Element) {
	entry := c.order.Remove(element).(*cacheEntry)
	delete(c.entries, entry.filename)
	c.size -= int64(len(entry.text))
}

// Reader handles PDF text extraction via pdftotext with in-memory caching.
// Supports automatic OCR fallback for image-based PDFs using tesseract.
type Reader struct {
	docsDir      string
	pdftotextBin string
	pdfinfoBin   string
	pdfimagesBin string
	pdftoppmBin  string
	tesseractBin string
	pandocBin    string
	hasOCR       bool // true if both pdftoppm and tesseract are available

	// cache holds the full text of documents already read, keyed by file
	// name, within the DOCS_MCP_CACHE_MB cap.
	cache *textCache
}

// toolTimeout bounds one run of pdftotext, pdfinfo, pdfimages or pandoc. A
// healthy run takes seconds even on a long document; a minute leaves room for
// very large files and still stops a binary stuck on a malformed one.
const toolTimeout = 60 * time.Second

// ocrPageTimeout bounds the OCR of one page, pdftoppm and tesseract together.
// Rendering a page at 200 dpi and recognising it takes a few seconds; two
// minutes covers slow machines and oversized pages.
const ocrPageTimeout = 120 * time.Second

// ocrCallTimeout bounds all the OCR a single call may do. Pages are
// recognised one after another, so a long scan would otherwise keep the tool
// busy for an hour; ten minutes is about what an MCP client will wait.
const ocrCallTimeout = 10 * time.Minute

// commandWaitDelay is how long Wait keeps waiting for the output pipes of a
// killed command. A wrapper script can leave a child holding stdout after the
// kill; two seconds lets real output drain without hanging on that child.
const commandWaitDelay = 2 * time.Second

// stderrLimit is how much of a tool's stderr goes into an error. The first
// kilobyte holds the actual complaint; poppler can repeat a warning for every
// broken object and would flood the tool result otherwise.
const stderrLimit = 1024

// runTool runs an external binary bound to ctx and to its own timeout, and
// returns what it wrote to stdout, also when it fails. stdin may be nil. The
// error names the tool, wraps the context error when a deadline or a
// cancellation stopped it, and carries the start of its stderr with
// documentPath reduced to the file name.
func runTool(ctx context.Context, timeout time.Duration, stdin io.Reader, documentPath, binary string, args ...string) (string, error) {
	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(toolCtx, binary, args...)
	cmd.WaitDelay = commandWaitDelay
	cmd.Stdin = stdin

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}

	tool := filepath.Base(binary)

	// A killed process only says "signal: killed"; the context says why.
	if contextErr := toolCtx.Err(); contextErr != nil {
		return stdout.String(), fmt.Errorf("%s did not finish: %w", tool, contextErr)
	}

	detail := boundedStderr(stderr.String(), documentPath)
	if detail == "" {
		return stdout.String(), fmt.Errorf("%s failed: %w", tool, err)
	}
	return stdout.String(), fmt.Errorf("%s failed: %w (stderr: %s)", tool, err, detail)
}

// boundedStderr prepares a tool's stderr for an error message: the document
// path becomes its file name, so the message never shows where the documents
// live, and the text is cut to stderrLimit bytes without splitting a rune.
func boundedStderr(stderr, documentPath string) string {
	if documentPath != "" {
		stderr = strings.ReplaceAll(stderr, documentPath, filepath.Base(documentPath))
	}

	stderr = strings.TrimSpace(stderr)
	if len(stderr) <= stderrLimit {
		return stderr
	}

	return strings.ToValidUTF8(stderr[:stderrLimit], "") + " [truncated]"
}

// isContextError reports whether err comes from a deadline or a cancellation.
// Those stop the work outright, where a failed OCR page is noted and skipped.
func isContextError(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// binaryDirs are the directories searched for a binary before PATH. An MCP
// client often starts the server with a bare PATH that misses Homebrew,
// which installs into /opt/homebrew/bin on Apple silicon and /usr/local/bin
// on Intel Macs.
var binaryDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// findBinary returns the path of name in the first of dirs that holds it, or
// the bare name so that exec looks it up in PATH.
func findBinary(name string, dirs []string) string {
	for _, dir := range dirs {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return name
}

// NewReader creates a new PDF reader for the given documents directory.
func NewReader(docsDir string) *Reader {
	pdftotextBin := findBinary("pdftotext", binaryDirs)
	pdfinfoBin := findBinary("pdfinfo", binaryDirs)
	pdfimagesBin := findBinary("pdfimages", binaryDirs)
	pdftoppmBin := findBinary("pdftoppm", binaryDirs)
	tesseractBin := findBinary("tesseract", binaryDirs)
	pandocBin := findBinary("pandoc", binaryDirs)

	// Determine OCR availability
	_, errPdftoppm := exec.LookPath(pdftoppmBin)
	_, errTesseract := exec.LookPath(tesseractBin)
	hasOCR := errPdftoppm == nil && errTesseract == nil

	return &Reader{
		docsDir:      docsDir,
		pdftotextBin: pdftotextBin,
		pdfinfoBin:   pdfinfoBin,
		pdfimagesBin: pdfimagesBin,
		pdftoppmBin:  pdftoppmBin,
		tesseractBin: tesseractBin,
		pandocBin:    pandocBin,
		hasOCR:       hasOCR,
		cache:        newTextCache(cacheLimit()),
	}
}

// CheckDependencies verifies that pdftotext, pdfinfo, and pdfimages are available.
// Returns errors for required tools, logs warnings for optional OCR tools.
func (r *Reader) CheckDependencies() error {
	if _, err := exec.LookPath(r.pdftotextBin); err != nil {
		return fmt.Errorf("pdftotext not found at %s: install poppler (brew install poppler)", r.pdftotextBin)
	}
	if _, err := exec.LookPath(r.pdfinfoBin); err != nil {
		return fmt.Errorf("pdfinfo not found at %s: install poppler (brew install poppler)", r.pdfinfoBin)
	}
	if _, err := exec.LookPath(r.pdfimagesBin); err != nil {
		return fmt.Errorf("pdfimages not found at %s: install poppler (brew install poppler)", r.pdfimagesBin)
	}
	return nil
}

// CheckOCRDependencies returns warnings for missing OCR tools. Not fatal.
func (r *Reader) CheckOCRDependencies() []string {
	var warnings []string
	if _, err := exec.LookPath(r.pdftoppmBin); err != nil {
		warnings = append(warnings, fmt.Sprintf("pdftoppm not found at %s: OCR will not be available (brew install poppler)", r.pdftoppmBin))
	}
	if _, err := exec.LookPath(r.tesseractBin); err != nil {
		warnings = append(warnings, fmt.Sprintf("tesseract not found at %s: OCR will not be available (brew install tesseract)", r.tesseractBin))
	}
	return warnings
}

// HasOCR returns whether OCR capabilities are available.
func (r *Reader) HasOCR() bool {
	return r.hasOCR
}

// pandocFormat ties a file extension to the pandoc reader that converts it.
type pandocFormat struct {
	extension string
	reader    string
}

// pandocFormats are the formats read through pandoc, in the order
// list_formats shows them. The reader goes to pandoc with -f, so the format
// never depends on pandoc's own guess from the file name; .htm and .html
// share the html reader.
var pandocFormats = []pandocFormat{
	{extension: ".docx", reader: "docx"},
	{extension: ".xlsx", reader: "xlsx"},
	{extension: ".pptx", reader: "pptx"},
	{extension: ".odt", reader: "odt"},
	{extension: ".epub", reader: "epub"},
	{extension: ".html", reader: "html"},
	{extension: ".htm", reader: "html"},
	{extension: ".rtf", reader: "rtf"},
	{extension: ".ipynb", reader: "ipynb"},
}

// pandocFormatFor returns the pandoc format of filename's extension and
// whether pandoc is the way to read that file at all.
func pandocFormatFor(filename string) (pandocFormat, bool) {
	extension := strings.ToLower(filepath.Ext(filename))
	for _, format := range pandocFormats {
		if format.extension == extension {
			return format, true
		}
	}
	return pandocFormat{}, false
}

// pandocExtensions returns the extensions of pandocFormats.
func pandocExtensions() []string {
	var extensions []string
	for _, format := range pandocFormats {
		extensions = append(extensions, format.extension)
	}
	return extensions
}

// supportedExtensions lists all document extensions this tool can work with:
// the ones read directly or with poppler, then every pandoc format.
var supportedExtensions = append([]string{".pdf", ".txt", ".md", ".csv"}, pandocExtensions()...)

// imageExtensions lists supported image file extensions for OCR. tesseract
// reads WebP through leptonica like the others.
var imageExtensions = []string{".png", ".jpg", ".jpeg", ".webp", ".tiff", ".tif", ".bmp"}

// textExtensions are read directly from disk; they never go through pdftotext.
var textExtensions = []string{".txt", ".md", ".csv"}

// isTextFormat reports whether the (sanitized) filename is a plain-text format.
func isTextFormat(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	for _, t := range textExtensions {
		if ext == t {
			return true
		}
	}
	return false
}

// isImage reports whether the (sanitized) filename is an image for read_image.
func isImage(filename string) bool {
	return slices.Contains(imageExtensions, strings.ToLower(filepath.Ext(filename)))
}

// documentType names the kind of a listed file for the "type" field of
// list_documents: "pdf", "text", "markdown", "csv", "image", or the pandoc
// reader of a pandoc format ("docx", "xlsx", "html" for .htm too, ...). It
// tells a client which tool fits before it opens the file.
func documentType(filename string) string {
	if format, ok := pandocFormatFor(filename); ok {
		return format.reader
	}
	if isImage(filename) {
		return "image"
	}

	switch strings.ToLower(filepath.Ext(filename)) {
	case ".pdf":
		return "pdf"
	case ".txt":
		return "text"
	case ".md":
		return "markdown"
	case ".csv":
		return "csv"
	default:
		return "unknown"
	}
}

// isPDF reports whether the (sanitized) filename is a PDF.
func isPDF(filename string) bool {
	return strings.ToLower(filepath.Ext(filename)) == ".pdf"
}

// summaryLines is how much of a text-format document GetDocumentSummary returns.
const summaryLines = 100

// readTextFile returns a text-format document verbatim.
func readTextFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", withoutPath(err))
	}
	return string(data), nil
}

// pandocPlain and pandocMarkdown are the pandoc writers this reader uses:
// plain text for read_document and everything built on it, GitHub-flavoured
// Markdown for convert_to_markdown, which keeps headings, emphasis and
// tables that plain text flattens.
const (
	pandocPlain    = "plain"
	pandocMarkdown = "gfm"
)

// convertWithPandoc converts the document at path from format to the pandoc
// writer target, without line wrapping so a paragraph stays on one line.
func (r *Reader) convertWithPandoc(ctx context.Context, path string, format pandocFormat, target string) (string, error) {
	if _, err := exec.LookPath(r.pandocBin); err != nil {
		return "", fmt.Errorf("reading %s requires pandoc (install pandoc and retry)", format.extension)
	}

	out, err := runTool(ctx, toolTimeout, nil, path, r.pandocBin, "-f", format.reader, "-t", target, "--wrap=none", path)
	if err != nil {
		return "", err
	}
	return out, nil
}

// extractText dispatches full-text extraction by format: text files verbatim,
// the pandoc formats as plain text, PDFs via pdftotext with OCR fallback.
func (r *Reader) extractText(ctx context.Context, filename, path string) (string, error) {
	if isTextFormat(filename) {
		return readTextFile(path)
	}

	if format, ok := pandocFormatFor(filename); ok {
		return r.convertWithPandoc(ctx, path, format, pandocPlain)
	}

	text, _, err := r.readWithOCRFallback(ctx, path, 0, 0)
	return text, err
}

// sanitizeFilename ensures the filename is safe (no directory traversal) and has a supported extension.
func (r *Reader) sanitizeFilename(filename string) (string, error) {
	base := filepath.Base(filename)
	if base != filename {
		return "", fmt.Errorf("invalid filename: directory traversal not allowed")
	}
	ext := strings.ToLower(filepath.Ext(base))
	for _, supported := range supportedExtensions {
		if ext == supported {
			return base, nil
		}
	}
	for _, supported := range imageExtensions {
		if ext == supported {
			return base, nil
		}
	}
	return "", fmt.Errorf("invalid filename: unsupported extension %q (supported: %v)", ext, append(supportedExtensions, imageExtensions...))
}

// sanitizeImageFilename ensures the filename is safe and has an image extension.
func (r *Reader) sanitizeImageFilename(filename string) (string, error) {
	base := filepath.Base(filename)
	if base != filename {
		return "", fmt.Errorf("invalid filename: directory traversal not allowed")
	}
	ext := strings.ToLower(filepath.Ext(base))
	for _, supported := range imageExtensions {
		if ext == supported {
			return base, nil
		}
	}
	return "", fmt.Errorf("invalid filename: unsupported image extension %q (supported: %v)", ext, imageExtensions)
}

// fullPath returns the full filesystem path for a sanitized filename.
func (r *Reader) fullPath(filename string) string {
	return filepath.Join(r.docsDir, filename)
}

// withoutPath drops the absolute path that an os error carries and keeps the
// operation and the cause, "open: permission denied" for instance, so a tool
// result names the file but never shows where the documents live on disk.
// errors.Is still sees the cause.
func withoutPath(err error) error {
	var pathError *fs.PathError
	if errors.As(err, &pathError) {
		return fmt.Errorf("%s: %w", pathError.Op, pathError.Err)
	}
	return err
}

// errOutsideDocsDir is returned when a name inside the documents directory
// resolves, through a symlink, to a file that lives somewhere else.
var errOutsideDocsDir = errors.New("resolves outside the documents directory")

// resolveDocument returns the real path of a sanitized filename. It follows
// symlinks, because pdftotext, tesseract and pandoc open the path themselves,
// and refuses any result that leaves the documents directory: a link called
// innocent.txt must not hand out a file from elsewhere on the disk. A name
// that does not exist, a dangling link included, is reported as not found.
func (r *Reader) resolveDocument(safe string) (string, error) {
	realDir, err := filepath.EvalSymlinks(r.docsDir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve the documents directory: %w", withoutPath(err))
	}

	realPath, err := filepath.EvalSymlinks(r.fullPath(safe))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("document not found: %s", safe)
	}
	if err != nil {
		return "", fmt.Errorf("cannot resolve %s: %w", safe, withoutPath(err))
	}

	// Rel answers ".." or a "../" prefix exactly when the real path is not
	// below the real directory; comparing string prefixes would accept a
	// sibling such as /docs-other for /docs.
	relative, err := filepath.Rel(realDir, realPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s %w", safe, errOutsideDocsDir)
	}

	return realPath, nil
}

// ListDocuments returns metadata for all supported documents and images in
// the documents directory.
func (r *Reader) ListDocuments(ctx context.Context) ([]DocumentInfo, error) {
	entries, err := os.ReadDir(r.docsDir)
	if err != nil {
		// The folder's own name helps the user spot a typo in DOCS_MCP_DIR;
		// the rest of the path stays out of the message.
		return nil, fmt.Errorf("cannot read the documents directory %q: %w", filepath.Base(r.docsDir), withoutPath(err))
	}

	var docs []DocumentInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))

		// Images are listed too: read_image and the other tools accept them,
		// and a client cannot ask for a file it was never shown.
		if !slices.Contains(supportedExtensions, ext) && !isImage(name) {
			continue
		}

		// A symlink that leaves the directory or points nowhere is left out
		// of the listing instead of failing it, and the size is the one of
		// the file the link reaches, not of the link itself.
		path, err := r.resolveDocument(name)
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}

		pages := 0
		if ext == ".pdf" {
			pages = r.getPageCount(ctx, path)
		}
		title := strings.TrimSuffix(name, filepath.Ext(name))

		docs = append(docs, DocumentInfo{
			Filename:  name,
			Title:     title,
			Type:      documentType(name),
			Pages:     pages,
			SizeBytes: info.Size(),
		})
	}

	return docs, nil
}

// getPageCount uses pdfinfo to get the page count for a PDF file, or 0 when
// pdfinfo cannot tell.
func (r *Reader) getPageCount(ctx context.Context, path string) int {
	pages, err := r.pageCount(ctx, path)
	if err != nil {
		return 0
	}
	return pages
}

// pageCount asks pdfinfo how many pages a PDF has and says why when it
// cannot, so a page range is never checked against a guess.
func (r *Reader) pageCount(ctx context.Context, path string) (int, error) {
	out, err := runTool(ctx, toolTimeout, nil, path, r.pdfinfoBin, path)
	if err != nil {
		return 0, err
	}

	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Pages:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				n, err := strconv.Atoi(parts[1])
				if err == nil {
					return n, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("pdfinfo reported no page count for %s", filepath.Base(path))
}

// ReadDocument extracts text from a PDF file. If page > 0, only that page is returned.
// Automatically falls back to OCR if pdftotext returns empty text.
func (r *Reader) ReadDocument(ctx context.Context, filename string, page int) (string, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return "", err
	}

	path, err := r.resolveDocument(safe)
	if err != nil {
		return "", err
	}

	if page > 0 {
		if !isPDF(safe) {
			return "", fmt.Errorf("page selection is only supported for PDF files; %s is %s — call without page/pages to read the whole document", safe, filepath.Ext(safe))
		}
		return r.readPageRanges(ctx, path, [][2]int{{page, page}})
	}

	return r.extractFull(ctx, safe, path)
}

// ReadDocumentPages extracts text from specific page ranges of a PDF file.
// Supports ranges like "1-5", "10", "1-3,7,10-12".
func (r *Reader) ReadDocumentPages(ctx context.Context, filename, pagesStr string) (string, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return "", err
	}

	path, err := r.resolveDocument(safe)
	if err != nil {
		return "", err
	}

	if !isPDF(safe) {
		return "", fmt.Errorf("page ranges are only supported for PDF files; %s is %s — call without page/pages to read the whole document", safe, filepath.Ext(safe))
	}
	return r.extractPageRanges(ctx, path, pagesStr)
}

// ReadFile extracts full text from an arbitrary PDF file path (used for URL downloads).
// Automatically falls back to OCR if pdftotext returns empty text.
func (r *Reader) ReadFile(ctx context.Context, path string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("file not found: %s", filepath.Base(path))
	}
	text, _, err := r.readWithOCRFallback(ctx, path, 0, 0)
	return text, err
}

// ReadFilePages extracts text from specific page ranges of an arbitrary PDF file path.
func (r *Reader) ReadFilePages(ctx context.Context, path, pagesStr string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("file not found: %s", filepath.Base(path))
	}
	return r.extractPageRanges(ctx, path, pagesStr)
}

// parsePageRanges parses a page range string like "1-5,7,10-12" into a list of [first, last] pairs.
func parsePageRanges(pagesStr string) ([][2]int, error) {
	var ranges [][2]int
	parts := strings.Split(pagesStr, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "-") {
			bounds := strings.SplitN(part, "-", 2)
			first, err := strconv.Atoi(strings.TrimSpace(bounds[0]))
			if err != nil {
				return nil, fmt.Errorf("invalid page range %q: %w", part, err)
			}
			last, err := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid page range %q: %w", part, err)
			}
			if first < 1 || last < first {
				return nil, fmt.Errorf("invalid page range %q: first must be >= 1 and <= last", part)
			}
			ranges = append(ranges, [2]int{first, last})
		} else {
			page, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid page number %q: %w", part, err)
			}
			if page < 1 {
				return nil, fmt.Errorf("invalid page number %q: must be >= 1", part)
			}
			ranges = append(ranges, [2]int{page, page})
		}
	}
	if len(ranges) == 0 {
		return nil, fmt.Errorf("no valid page ranges found in %q", pagesStr)
	}
	return ranges, nil
}

// maxPagesPerCall caps the pages one read may cover. A scanned page costs two
// processes and up to ocrPageTimeout, so a request for a thousand pages
// would run for hours; two hundred is a long chapter, and a client that
// wants more can ask in several calls.
const maxPagesPerCall = 200

// errTooManyPages marks a request over maxPagesPerCall, so the OCR fallback
// can report it instead of quietly returning the empty pdftotext text.
var errTooManyPages = errors.New("too many pages for one call")

// clampPageRanges fits the requested ranges to the pages the document really
// has: a range that runs past the end stops at the last page and one that
// starts after it is an error. The pages left are capped at maxPagesPerCall.
// Without this, "1-60" on a one-page PDF looked like sixty empty pages and
// started sixty OCR runs.
func clampPageRanges(ranges [][2]int, pageCount int) ([][2]int, error) {
	var clamped [][2]int
	total := 0

	for _, pageRange := range ranges {
		first := pageRange[0]
		last := pageRange[1]
		if first > pageCount {
			return nil, fmt.Errorf("page %d does not exist: the document has %d page(s)", first, pageCount)
		}
		if last > pageCount {
			last = pageCount
		}

		total += last - first + 1
		clamped = append(clamped, [2]int{first, last})
	}

	if total > maxPagesPerCall {
		return nil, fmt.Errorf("%w: a call can read at most %d pages and this one asks for %d, split it into several calls", errTooManyPages, maxPagesPerCall, total)
	}

	return clamped, nil
}

// extractPageRanges extracts text from multiple page ranges and combines the results.
// Falls back to OCR if pdftotext returns empty text.
func (r *Reader) extractPageRanges(ctx context.Context, path, pagesStr string) (string, error) {
	ranges, err := parsePageRanges(pagesStr)
	if err != nil {
		return "", err
	}

	return r.readPageRanges(ctx, path, ranges)
}

// readPageRanges checks parsed ranges against the page count pdfinfo gives,
// then extracts each one with the OCR fallback. The real count, not the
// request, decides how many pages are read and how much text a range needs
// before it counts as scanned.
func (r *Reader) readPageRanges(ctx context.Context, path string, ranges [][2]int) (string, error) {
	pageCount, err := r.pageCount(ctx, path)
	if err != nil {
		return "", err
	}

	ranges, err = clampPageRanges(ranges, pageCount)
	if err != nil {
		return "", err
	}

	var parts []string
	for _, rng := range ranges {
		text, _, err := r.readWithOCRFallback(ctx, path, rng[0], rng[1])
		if err != nil {
			return "", fmt.Errorf("error extracting pages %d-%d: %w", rng[0], rng[1], err)
		}
		parts = append(parts, text)
	}

	return strings.Join(parts, "\n"), nil
}

// extractFull extracts the full text of a PDF, using cache when possible.
// Falls back to OCR if pdftotext returns empty text.
func (r *Reader) extractFull(ctx context.Context, filename, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("cannot stat file: %w", withoutPath(err))
	}
	mtime := info.ModTime()

	// Check cache
	if text, ok := r.cache.get(filename, mtime); ok {
		return text, nil
	}

	// Extract by format (text verbatim, docx via pandoc, PDF via pdftotext + OCR fallback)
	text, err := r.extractText(ctx, filename, path)
	if err != nil {
		return "", err
	}

	// Update cache
	r.cache.put(filename, mtime, text)

	return text, nil
}

// runPdftotext invokes pdftotext with the given options.
func (r *Reader) runPdftotext(ctx context.Context, path string, firstPage, lastPage int) (string, error) {
	args := []string{"-layout"}
	if firstPage > 0 {
		args = append(args, "-f", strconv.Itoa(firstPage))
	}
	if lastPage > 0 {
		args = append(args, "-l", strconv.Itoa(lastPage))
	}
	args = append(args, path, "-")

	out, err := runTool(ctx, toolTimeout, nil, path, r.pdftotextBin, args...)
	if err != nil {
		return "", err
	}
	return out, nil
}

// isTextEmpty returns true if text has fewer than 50 non-whitespace characters
// per page, indicating an image-based PDF that pdftotext cannot extract.
func isTextEmpty(text string, pageCount int) bool {
	if pageCount < 1 {
		pageCount = 1
	}
	nonWS := 0
	for _, c := range text {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' && c != '\f' {
			nonWS++
		}
	}
	threshold := 50 * pageCount
	return nonWS < threshold
}

// defaultOCRLanguage is the tesseract language when neither the call nor
// DOCS_MCP_OCR_LANG names one; English ships with every tesseract install.
const defaultOCRLanguage = "eng"

// ocrLanguagePattern is what a tesseract language may look like: lowercase
// codes such as spa or chi_sim, joined with + to combine several. It keeps an
// option, a path or anything else out of tesseract's -l argument.
var ocrLanguagePattern = regexp.MustCompile(`^[a-z_+]{3,20}$`)

// ocrLanguage picks the OCR language for a call: the call's own language,
// then DOCS_MCP_OCR_LANG, then English, and refuses one that does not match
// ocrLanguagePattern with an error that says what is accepted.
func ocrLanguage(requested string) (string, error) {
	language := strings.TrimSpace(requested)
	if language == "" {
		language = strings.TrimSpace(os.Getenv("DOCS_MCP_OCR_LANG"))
	}
	if language == "" {
		language = defaultOCRLanguage
	}

	if !ocrLanguagePattern.MatchString(language) {
		return "", fmt.Errorf("invalid OCR language %q: use tesseract codes such as eng, spa or eng+spa (lowercase letters, _ and +, 3 to 20 characters)", language)
	}

	return language, nil
}

// ocrPage runs OCR on a single page of a PDF using pdftoppm + tesseract.
// Uses TIFF format with temp files, piping the TIFF data via stdin to tesseract
// to work around leptonica file-open bugs on macOS.
func (r *Reader) ocrPage(ctx context.Context, path string, page int, language string) (string, error) {
	// One deadline covers rendering and recognising the page.
	pageCtx, cancel := context.WithTimeout(ctx, ocrPageTimeout)
	defer cancel()

	// Create temp dir for the TIFF file
	tmpDir, err := os.MkdirTemp("", "go-docs-mcp-ocr-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", withoutPath(err))
	}
	defer os.RemoveAll(tmpDir)

	prefix := filepath.Join(tmpDir, "page")

	// pdftoppm -tiff -r 200 -f N -l N -singlefile <pdf> <prefix>
	// This creates <prefix>.tif
	pdftoppmArgs := []string{"-tiff", "-r", "200", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-singlefile", path, prefix}
	if _, err := runTool(pageCtx, ocrPageTimeout, nil, path, r.pdftoppmBin, pdftoppmArgs...); err != nil {
		return "", fmt.Errorf("page %d: %w", page, err)
	}

	tiffPath := prefix + ".tif"
	if _, err := os.Stat(tiffPath); os.IsNotExist(err) {
		return "", fmt.Errorf("pdftoppm did not produce output for page %d", page)
	}

	// Pipe TIFF data via stdin to tesseract to work around leptonica bug
	// where tesseract cannot open files directly on some macOS installations
	tiffData, err := os.Open(tiffPath)
	if err != nil {
		return "", fmt.Errorf("failed to open TIFF file: %w", withoutPath(err))
	}
	defer tiffData.Close()

	tesseractArgs := []string{"stdin", "stdout", "-l", language}
	text, err := runTool(pageCtx, ocrPageTimeout, tiffData, path, r.tesseractBin, tesseractArgs...)

	// tesseract can exit non-zero after printing usable text, for instance
	// after a warning about the resolution; that text is kept unless the
	// deadline cut it short.
	if err != nil && (text == "" || isContextError(err)) {
		return "", fmt.Errorf("page %d: %w", page, err)
	}

	return text, nil
}

// ocrDocument runs OCR on all pages (or a specific page) of a PDF.
// Returns the extracted text and the method used.
func (r *Reader) ocrDocument(ctx context.Context, path string, page int, language string) (string, error) {
	if !r.hasOCR {
		return "", fmt.Errorf("OCR not available: tesseract and/or pdftoppm not installed")
	}

	if page > 0 {
		return r.ocrPages(ctx, path, page, page, language)
	}

	// Get total page count
	totalPages := r.getPageCount(ctx, path)
	if totalPages == 0 {
		// Fallback: try just page 1
		totalPages = 1
	}

	return r.ocrPages(ctx, path, 1, totalPages, language)
}

// ocrPages runs OCR on pages firstPage to lastPage under one ocrCallTimeout.
// A page that fails is noted in the text and the next one is tried; a
// deadline or a cancellation stops the loop, because every later page would
// fail the same way. A single page returns its error as it is. Several pages
// come back joined by joinOCRPages, one form feed after each.
func (r *Reader) ocrPages(ctx context.Context, path string, firstPage, lastPage int, language string) (string, error) {
	pages := lastPage - firstPage + 1
	if pages > maxPagesPerCall {
		return "", fmt.Errorf("%w: OCR covers at most %d pages per call and this document has %d, ask for a range with pages", errTooManyPages, maxPagesPerCall, pages)
	}

	ocrCtx, cancel := context.WithTimeout(ctx, ocrCallTimeout)
	defer cancel()

	if firstPage == lastPage {
		return r.ocrPage(ocrCtx, path, firstPage, language)
	}

	var pageTexts []string
	for page := firstPage; page <= lastPage; page++ {
		text, err := r.ocrPage(ocrCtx, path, page, language)
		if isContextError(err) {
			return "", err
		}

		// A failed page keeps its place, so the pages after it are still
		// counted right.
		if err != nil {
			pageTexts = append(pageTexts, fmt.Sprintf("[OCR failed on page %d: %v]", page, err))
			continue
		}
		pageTexts = append(pageTexts, text)
	}

	return joinOCRPages(pageTexts), nil
}

// ocrPageBreak ends the text of every recognised page. It is the form feed
// pdftotext writes after each page, so SearchDocument, the outline and the
// table detector count the pages of a scan as they count those of a text PDF.
const ocrPageBreak = "\f"

// joinOCRPages joins the text recognised on consecutive pages the way
// pdftotext lays out a document: each page ends with a newline and then
// ocrPageBreak. The newline matters because a page is counted on the line
// that carries the form feed; without it the last line of one page would
// be reported on the next.
func joinOCRPages(pages []string) string {
	var joined strings.Builder

	for _, text := range pages {
		joined.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			joined.WriteString("\n")
		}
		joined.WriteString(ocrPageBreak)
	}

	return joined.String()
}

// errOCRNeedsPDF is returned when ocr_document is asked for a file that is
// not a PDF. pdftoppm cannot render anything else, and the error used to be
// a confusing poppler complaint; images have read_image of their own.
var errOCRNeedsPDF = errors.New("ocr_document only works on PDF files; use read_image for images")

// OCRDocument performs OCR on a PDF in the configured directory.
// Forces OCR regardless of whether pdftotext works. Useful for garbled text.
func (r *Reader) OCRDocument(ctx context.Context, filename string, page int, language string) (string, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return "", err
	}

	if !isPDF(safe) {
		return "", fmt.Errorf("%w (%s is %s)", errOCRNeedsPDF, safe, filepath.Ext(safe))
	}

	language, err = ocrLanguage(language)
	if err != nil {
		return "", err
	}

	path, err := r.resolveDocument(safe)
	if err != nil {
		return "", err
	}

	return r.ocrDocument(ctx, path, page, language)
}

// readWithOCRFallback extracts text from a PDF, falling back to OCR if pdftotext
// returns empty/whitespace-only text. Returns the text and extraction method used.
func (r *Reader) readWithOCRFallback(ctx context.Context, path string, firstPage, lastPage int) (text string, method string, err error) {
	text, err = r.runPdftotext(ctx, path, firstPage, lastPage)
	if err != nil {
		return "", "", err
	}

	// A whole-document read asks pdfinfo once, and that count sets both the
	// threshold and the pages to recognise. Ranges arrive already clamped to
	// the real page count by readPageRanges.
	if firstPage == 0 {
		firstPage = 1
		lastPage = r.getPageCount(ctx, path)
		if lastPage == 0 {
			lastPage = 1
		}
	}

	if !isTextEmpty(text, lastPage-firstPage+1) {
		return text, "pdftotext", nil
	}

	// Text is empty or near-empty; try OCR fallback
	if !r.hasOCR {
		return text, "pdftotext (empty, no OCR available)", nil
	}

	// The fallback has no language parameter of its own, so it follows
	// DOCS_MCP_OCR_LANG and then English.
	language, err := ocrLanguage("")
	if err != nil {
		return "", "", err
	}

	ocrText, ocrErr := r.ocrPages(ctx, path, firstPage, lastPage, language)

	// A deadline, a cancellation or a scan too long for one call is the
	// caller's answer, not a reason to fall back to the empty pdftotext text.
	if isContextError(ocrErr) || errors.Is(ocrErr, errTooManyPages) {
		return "", "", ocrErr
	}
	if ocrErr != nil {
		// Return the original (possibly empty) pdftotext result
		return text, "pdftotext (OCR fallback failed)", nil
	}
	return ocrText, "ocr", nil
}

// DefaultMaxChars is how much text read_document, convert_to_markdown and
// ocr_document return per call when the client does not say. 50,000
// characters are about 12,000 tokens: a long chapter, and well under what an
// MCP client accepts from one tool call.
const DefaultMaxChars = 50000

// MaxCharsLimit caps max_chars whatever the client asks for. 200,000
// characters are about 50,000 tokens, the most a single result should take
// of a model's context; a longer read is several calls with offset.
const MaxCharsLimit = 200000

// TextPage is one slice of a document's text. Offsets count characters
// (Unicode code points), not bytes, so a client can page through any
// language without cutting a character in half.
type TextPage struct {
	Text       string `json:"text" jsonschema:"The text of this page of the document"`
	NextOffset int    `json:"next_offset,omitempty" jsonschema:"Offset to pass on the next call; absent when this page reaches the end"`
	TotalChars int    `json:"total_chars" jsonschema:"Length of the whole text in characters"`

	// offset is where Text starts, kept for the trailer.
	offset int
}

// PageText returns at most maxChars characters of text starting at the
// character offset. maxChars above MaxCharsLimit is lowered to it. An offset
// equal to the length gives an empty last page; a negative offset, one past
// the end or a maxChars below 1 is an error the client can correct.
func PageText(text string, offset, maxChars int) (TextPage, error) {
	if offset < 0 {
		return TextPage{}, fmt.Errorf("offset must be 0 or more, got %d", offset)
	}
	if maxChars < 1 {
		return TextPage{}, fmt.Errorf("max_chars must be 1 or more, got %d", maxChars)
	}
	maxChars = min(maxChars, MaxCharsLimit)

	totalChars := utf8.RuneCountInString(text)
	if offset > totalChars {
		return TextPage{}, fmt.Errorf("offset %d is past the end of the text, which has %d characters", offset, totalChars)
	}

	start := byteIndexOfChar(text, 0, offset)
	end := byteIndexOfChar(text, start, maxChars)

	page := TextPage{Text: text[start:end], TotalChars: totalChars, offset: offset}

	// The slice is maxChars long unless it reached the end, so the next
	// offset is only set when something is left.
	if offset+maxChars < totalChars {
		page.NextOffset = offset + maxChars
	}

	return page, nil
}

// byteIndexOfChar returns the byte index in text that lies count characters
// after the byte index from, or len(text) when the text ends first.
func byteIndexOfChar(text string, from, count int) int {
	index := from

	for range count {
		if index >= len(text) {
			break
		}

		_, size := utf8.DecodeRuneInString(text[index:])
		index += size
	}

	return index
}

// TextWithTrailer returns the page text and, when the document goes on, a
// last line that tells the model where this page ends and how to ask for
// the next one, so a truncated read is never mistaken for the whole document.
func (page TextPage) TextWithTrailer() string {
	if page.NextOffset == 0 {
		return page.Text
	}

	return fmt.Sprintf("%s\n\n[truncated: showing chars %d-%d of %d; call again with offset=%d]", page.Text, page.offset, page.NextOffset, page.TotalChars, page.NextOffset)
}

// DefaultSearchResults is how many matches search_document shows when the
// client does not say. Fifty matches with their context fit comfortably in
// one result and are more than a model reads before refining the query.
const DefaultSearchResults = 50

// MaxSearchResultsLimit caps max_results. Each match carries five lines of
// context, and pdftotext -layout lines can be long, so 500 matches is about
// as much as a result can hold before the server's size limit refuses it.
const MaxSearchResultsLimit = 500

// SearchDocument searches a document for lines matching the query (case-insensitive).
// Returns matching lines with 2 lines of context before and after, with page number hints.
// Only the first maxResults matches are shown, capped at MaxSearchResultsLimit,
// but the header counts all of them so the client knows when to narrow the query.
func (r *Reader) SearchDocument(ctx context.Context, filename, query string, maxResults int) (string, error) {
	if maxResults < 1 {
		return "", fmt.Errorf("max_results must be 1 or more, got %d", maxResults)
	}
	maxResults = min(maxResults, MaxSearchResultsLimit)

	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return "", err
	}

	path, err := r.resolveDocument(safe)
	if err != nil {
		return "", err
	}

	text, err := r.extractFull(ctx, safe, path)
	if err != nil {
		return "", err
	}

	lines := strings.Split(text, "\n")
	queryLower := strings.ToLower(query)
	contextLines := 2

	var results []string
	matchCount := 0
	pageNum := 1

	for i, line := range lines {
		// Track page breaks (form feed character)
		if strings.Contains(line, "\f") {
			pageNum++
		}

		if strings.Contains(strings.ToLower(line), queryLower) {
			matchCount++

			// Matches past the cap are still counted for the header, but
			// their context is not rendered.
			if matchCount > maxResults {
				continue
			}

			start := i - contextLines
			if start < 0 {
				start = 0
			}
			end := i + contextLines + 1
			if end > len(lines) {
				end = len(lines)
			}

			results = append(results, fmt.Sprintf("--- Match %d (page ~%d, line %d) ---", matchCount, pageNum, i+1))
			for j := start; j < end; j++ {
				prefix := "  "
				if j == i {
					prefix = "> "
				}
				results = append(results, prefix+lines[j])
			}
			results = append(results, "")
		}
	}

	if matchCount == 0 {
		return fmt.Sprintf("No matches found for '%s' in %s", query, safe), nil
	}

	header := fmt.Sprintf("Found %d matches for '%s' in %s:\n\n", matchCount, query, safe)
	if matchCount > maxResults {
		header = fmt.Sprintf("Found %d matches for '%s' in %s, showing the first %d; narrow the query or raise max_results to see more:\n\n", matchCount, query, safe, maxResults)
	}

	return header + strings.Join(results, "\n"), nil
}

// summaryPages is how much of a PDF GetDocumentSummary returns: three pages
// usually hold the title, the abstract or the table of contents.
const summaryPages = 3

// GetDocumentSummary returns the first pages of a PDF, or the first lines of
// any other document, as a summary.
func (r *Reader) GetDocumentSummary(ctx context.Context, filename string) (string, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return "", err
	}

	path, err := r.resolveDocument(safe)
	if err != nil {
		return "", err
	}

	if !isPDF(safe) {
		text, err := r.extractFull(ctx, safe, path)
		if err != nil {
			return "", err
		}
		lines := strings.SplitAfter(text, "\n")
		if len(lines) > summaryLines {
			lines = lines[:summaryLines]
		}
		return fmt.Sprintf("Summary (first %d lines) of %s:\n\n%s", len(lines), safe, strings.Join(lines, "")), nil
	}

	// The summary is cut from the cached full text, so it costs nothing
	// after read_document or search_document and benefits from the OCR
	// fallback. A scan too long to recognise in one call can still be
	// summarised: then only its first pages are read, without the cache.
	text, err := r.extractFull(ctx, safe, path)
	if errors.Is(err, errTooManyPages) {
		text, err = r.readPageRanges(ctx, path, [][2]int{{1, summaryPages}})
	}
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Summary (first %d pages) of %s:\n\n%s", summaryPages, safe, firstPages(text, summaryPages)), nil
}

// firstPages returns the first count pages of pdftotext or OCR text, where
// every page ends with a form feed, or all of it when it has fewer pages.
func firstPages(text string, count int) string {
	end := 0
	for range count {
		next := strings.Index(text[end:], "\f")
		if next < 0 {
			return text
		}
		end += next + 1
	}
	return text[:end]
}

// GetDocumentMetadata returns full PDF metadata using pdfinfo.
func (r *Reader) GetDocumentMetadata(ctx context.Context, filename string) (*DocumentMetadata, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return nil, err
	}

	path, err := r.resolveDocument(safe)
	if err != nil {
		return nil, err
	}

	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("cannot stat file: %w", withoutPath(err))
	}

	out, err := runTool(ctx, toolTimeout, nil, path, r.pdfinfoBin, path)
	if err != nil {
		return nil, err
	}

	meta := &DocumentMetadata{
		FileSize: fi.Size(),
	}

	for _, line := range strings.Split(out, "\n") {
		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			switch key {
			case "Title":
				meta.Title = value
			case "Author":
				meta.Author = value
			case "Subject":
				meta.Subject = value
			case "Creator":
				meta.Creator = value
			case "Producer":
				meta.Producer = value
			case "CreationDate":
				meta.CreationDate = value
			case "ModDate":
				meta.ModDate = value
			case "Pages":
				n, err := strconv.Atoi(value)
				if err == nil {
					meta.Pages = n
				}
			case "PDF version":
				meta.PDFVersion = value
			}
		}
	}

	return meta, nil
}

// MaxImageSide is the longest side, in pixels, of an image extract_images
// returns. Vision models scale anything larger down to about this size
// before looking at it, so sending more only costs bytes.
const MaxImageSide = 1600

// ImageBudgetBytes is how much base64 image data one extract_images call may
// return in total. 8 MB holds several figures at full size and keeps the
// result far from the tens of megabytes raw extraction used to produce.
const ImageBudgetBytes = 8 << 20

// maxImageBytes caps the base64 data of a single image. Clients reject an
// image over 5 MB, so an image still larger after scaling is shrunk further
// until it fits under 4 MB.
const maxImageBytes = 4 << 20

// DefaultMaxImages is how many images extract_images returns when the
// client does not say.
const DefaultMaxImages = 10

// MaxImagesLimit caps max_images. Every image is decoded and scaled in
// memory, so the cap bounds the work of one call as the budget bounds its
// size.
const MaxImagesLimit = 50

// maxDecodePixels refuses to decode an image larger than 40 megapixels, an
// A4 page scanned at 600 dpi with room to spare. Decoding takes four bytes a
// pixel, and a crafted PDF can declare an image of any size.
const maxDecodePixels = 40_000_000

// minImageSide is the smallest longer side an oversized image is shrunk to
// before giving up: a picture under 64 px no longer shows anything.
const minImageSide = 64

// ImageInfo describes one image extracted from a PDF, always as PNG. Width
// and Height are the size of the PNG returned, OriginalWidth and
// OriginalHeight the size embedded in the document. Index is the image
// number pdfimages gives it within the requested pages.
type ImageInfo struct {
	Page           int    `json:"page"`
	Index          int    `json:"index"`
	Format         string `json:"format"`
	MIMEType       string `json:"mime_type"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	OriginalWidth  int    `json:"original_width"`
	OriginalHeight int    `json:"original_height"`

	// DataBase64 is the PNG itself. It is left out of the JSON because the
	// tool sends it as image content, not inside the text.
	DataBase64 string `json:"-"`
}

// ImageExtraction is the result of ExtractImages: the images returned, how
// many pictures the requested pages hold (masks are not counted), and a note
// for every image left out and why.
type ImageExtraction struct {
	Images []ImageInfo `json:"images"`
	Found  int         `json:"found"`
	Notes  []string    `json:"notes,omitempty"`
}

// listedImage is one row of pdfimages -list.
type listedImage struct {
	page   int
	number int
	kind   string
	width  int
	height int
}

// parseImageList reads the rows of pdfimages -list. The kind column says
// whether a row is a picture ("image") or the transparency of one ("smask",
// "mask", "stencil").
func parseImageList(listing string) []listedImage {
	var images []listedImage

	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		page, errPage := strconv.Atoi(fields[0])
		number, errNumber := strconv.Atoi(fields[1])
		width, errWidth := strconv.Atoi(fields[3])
		height, errHeight := strconv.Atoi(fields[4])

		// The header and the dashed rule under it do not parse as numbers.
		if errors.Join(errPage, errNumber, errWidth, errHeight) != nil {
			continue
		}

		images = append(images, listedImage{page: page, number: number, kind: fields[2], width: width, height: height})
	}

	return images
}

// ExtractImages extracts the pictures of a PDF as PNG, scaled to at most
// MaxImageSide on the longer side. If page > 0, only that page is read. It
// returns at most maxImages of them (capped at MaxImagesLimit) and stops
// before the base64 data passes ImageBudgetBytes. Soft masks and stencils
// are never returned as images of their own.
func (r *Reader) ExtractImages(ctx context.Context, filename string, page, maxImages int) (*ImageExtraction, error) {
	if maxImages < 1 {
		return nil, fmt.Errorf("max_images must be 1 or more, got %d", maxImages)
	}
	maxImages = min(maxImages, MaxImagesLimit)

	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return nil, err
	}

	path, err := r.resolveDocument(safe)
	if err != nil {
		return nil, err
	}

	return r.extractImagesFromPath(ctx, path, page, maxImages)
}

// extractImagesFromPath extracts the pictures of the PDF at path for
// ExtractImages.
func (r *Reader) extractImagesFromPath(ctx context.Context, path string, page, maxImages int) (*ImageExtraction, error) {
	var firstPageArgs []string
	if page > 0 {
		firstPageArgs = []string{"-f", strconv.Itoa(page)}
	}

	listArgs := append([]string{"-list"}, firstPageArgs...)
	if page > 0 {
		listArgs = append(listArgs, "-l", strconv.Itoa(page))
	}
	listArgs = append(listArgs, path)

	listing, err := runTool(ctx, toolTimeout, nil, path, r.pdfimagesBin, listArgs...)
	if err != nil {
		return nil, err
	}

	// A soft mask is listed and extracted as an image of its own, a grey
	// picture of the transparency; only the real pictures are returned.
	var pictures []listedImage
	for _, listed := range parseImageList(listing) {
		if listed.kind == "image" {
			pictures = append(pictures, listed)
		}
	}

	extraction := &ImageExtraction{Images: []ImageInfo{}, Found: len(pictures)}
	if len(pictures) == 0 {
		return extraction, nil
	}

	selected := pictures[:min(maxImages, len(pictures))]
	if len(selected) < len(pictures) {
		extraction.Notes = append(extraction.Notes, fmt.Sprintf("returned the first %d of %d images; raise max_images or ask for one page to see the others", len(selected), len(pictures)))
	}

	tmpDir, err := os.MkdirTemp("", "go-docs-mcp-images-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", withoutPath(err))
	}
	defer os.RemoveAll(tmpDir)

	prefix := filepath.Join(tmpDir, "img")

	// Extraction stops at the last page that holds a selected picture, so a
	// long document does not have every image written to disk for ten of
	// them. The first page is the listing's, which keeps pdfimages' image
	// numbers the same in both runs.
	//
	// Without -png, pdfimages writes PNM, which is decoded and encoded as
	// PNG here. Its own PNG writer tries every row filter at the highest
	// compression, and on a 2048 px photo that took several times longer
	// than writing PNM and doing the rest in Go.
	lastPage := selected[len(selected)-1].page
	extractArgs := append(slices.Clone(firstPageArgs), "-l", strconv.Itoa(lastPage), path, prefix)

	if _, err := runTool(ctx, toolTimeout, nil, path, r.pdfimagesBin, extractArgs...); err != nil {
		return nil, err
	}

	budgetUsed := 0

	for _, picture := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		info, err := preparePicture(prefix, picture)
		if err != nil {
			extraction.Notes = append(extraction.Notes, fmt.Sprintf("image %d on page %d was left out: %v", picture.number, picture.page, err))
			continue
		}

		if budgetUsed+len(info.DataBase64) > ImageBudgetBytes {
			extraction.Notes = append(extraction.Notes, fmt.Sprintf("stopped at the %d MB image budget after %d images; ask for one page with page to see the rest", ImageBudgetBytes>>20, len(extraction.Images)))
			break
		}

		budgetUsed += len(info.DataBase64)
		extraction.Images = append(extraction.Images, info)
	}

	return extraction, nil
}

// pnmExtensions are the files pdfimages writes when it is not asked for
// PNG: PPM for colour and grey, PBM for one bit a pixel, and PGM, which
// poppler 25 does not write for pictures but older releases may.
var pnmExtensions = []string{".ppm", ".pbm", ".pgm"}

// preparePicture reads the PNM pdfimages wrote for picture under prefix and
// returns it as a PNG ImageInfo, scaled to MaxImageSide on the longer side
// and shrunk further while its base64 is over maxImageBytes.
func preparePicture(prefix string, picture listedImage) (ImageInfo, error) {
	file, err := openPNM(prefix, picture.number)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("pdfimages did not write it as PNM: %w", withoutPath(err))
	}
	defer file.Close()

	decoded, err := decodePNM(file)
	if err != nil {
		return ImageInfo{}, err
	}

	bounds := decoded.Bounds()
	info := ImageInfo{
		Page:           picture.page,
		Index:          picture.number,
		Format:         "png",
		MIMEType:       "image/png",
		OriginalWidth:  bounds.Dx(),
		OriginalHeight: bounds.Dy(),
	}

	// Every attempt scales the original again, a quarter smaller than the
	// last, so the quality does not degrade from resampling a resample. The
	// first attempt always runs, so an icon under minImageSide still goes
	// out at its own size.
	width, height := scaledSize(bounds.Dx(), bounds.Dy(), MaxImageSide)
	longest := max(width, height)

	for {
		width, height = scaledSize(bounds.Dx(), bounds.Dy(), longest)

		scaled := decoded
		if width != bounds.Dx() || height != bounds.Dy() {
			scaled = boxResample(decoded, width, height)
		}

		// DefaultCompression on purpose: png.BestSpeed encodes a 1600 px
		// photo about a fifth faster, but next to pdfimages and the file
		// I/O the whole call did not get measurably faster, and the PNG
		// grew by 4 %.
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, scaled); err != nil {
			return ImageInfo{}, fmt.Errorf("failed to encode PNG: %w", err)
		}

		if base64.StdEncoding.EncodedLen(encoded.Len()) <= maxImageBytes {
			info.Width = width
			info.Height = height
			info.DataBase64 = base64.StdEncoding.EncodeToString(encoded.Bytes())
			return info, nil
		}

		longest = longest * 3 / 4
		if longest < minImageSide {
			return ImageInfo{}, fmt.Errorf("does not fit in %d MB even at %d px", maxImageBytes>>20, minImageSide)
		}
	}
}

// openPNM opens the file pdfimages wrote for image number under prefix,
// whichever of the PNM extensions it has.
func openPNM(prefix string, number int) (*os.File, error) {
	var lastErr error

	for _, extension := range pnmExtensions {
		file, err := os.Open(fmt.Sprintf("%s-%03d%s", prefix, number, extension))
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		lastErr = err
	}

	return nil, lastErr
}

// scaledSize returns width and height scaled so the longer side is at most
// longest, keeping the aspect ratio. A size that already fits is returned
// as it is, and neither side drops below one pixel.
func scaledSize(width, height, longest int) (int, int) {
	if width <= longest && height <= longest {
		return width, height
	}

	if width >= height {
		return longest, max(1, (height*longest+width/2)/width)
	}

	return max(1, (width*longest+height/2)/height), longest
}

// pixelPlane is the raw pixel memory of an image with one byte per channel:
// the Pix and Stride of an image.Gray or image.RGBA, with its size.
type pixelPlane struct {
	pix    []uint8
	stride int
	width  int
	height int
}

// boxResample scales source down to width by height with a box filter: each
// target pixel is the average of the source pixels it covers, which keeps
// thin lines and text that nearest-neighbour sampling would drop. A grey
// source gives a grey image, so its PNG stays a third of the size; anything
// else is averaged as premultiplied RGBA, which keeps the colour of
// half-transparent edges right.
func boxResample(source image.Image, width, height int) image.Image {
	bounds := source.Bounds()

	if gray, ok := source.(*image.Gray); ok {
		target := image.NewGray(image.Rect(0, 0, width, height))
		sourcePlane := pixelPlane{pix: gray.Pix[gray.PixOffset(bounds.Min.X, bounds.Min.Y):], stride: gray.Stride, width: bounds.Dx(), height: bounds.Dy()}
		boxAverage(sourcePlane, pixelPlane{pix: target.Pix, stride: target.Stride, width: width, height: height}, 1)
		return target
	}

	// image/draw converts any other model to RGBA with fast paths for the
	// types the PNG decoder returns.
	rgba, ok := source.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
		draw.Draw(rgba, rgba.Bounds(), source, bounds.Min, draw.Src)
		bounds = rgba.Bounds()
	}

	target := image.NewRGBA(image.Rect(0, 0, width, height))
	sourcePlane := pixelPlane{pix: rgba.Pix[rgba.PixOffset(bounds.Min.X, bounds.Min.Y):], stride: rgba.Stride, width: bounds.Dx(), height: bounds.Dy()}
	boxAverage(sourcePlane, pixelPlane{pix: target.Pix, stride: target.Stride, width: width, height: height}, 4)

	return target
}

// boxAverage fills target with the rounded average of the block of source
// pixels under each target pixel, channel by channel. It only scales down;
// a block is at least one source pixel wide and tall.
func boxAverage(source, target pixelPlane, channels int) {
	// The column ranges are the same for every row, so they are worked out
	// once.
	columnStarts := make([]int, target.width+1)
	for column := range columnStarts {
		columnStarts[column] = column * source.width / target.width
	}

	sums := make([]uint64, channels)

	for row := range target.height {
		top := row * source.height / target.height
		bottom := max((row+1)*source.height/target.height, top+1)

		for column := range target.width {
			left := columnStarts[column]
			right := max(columnStarts[column+1], left+1)

			clear(sums)
			for y := top; y < bottom; y++ {
				offset := y*source.stride + left*channels
				for x := left; x < right; x++ {
					for channel := range channels {
						sums[channel] += uint64(source.pix[offset+channel])
					}
					offset += channels
				}
			}

			count := uint64((bottom - top) * (right - left))
			targetOffset := row*target.stride + column*channels
			for channel := range channels {
				target.pix[targetOffset+channel] = uint8((sums[channel] + count/2) / count)
			}
		}
	}
}

// maxPNMHeaderNumber caps every number in a PNM header. A crafted file can
// write any number of digits, and 16 million is far above the side of any
// real picture while width times height still fits in an int.
const maxPNMHeaderNumber = 1 << 24

// pnmHeader is the header of a binary PNM file: its magic number ("P4",
// "P5" or "P6"), its size, and maxval, the value of a full-intensity
// sample (1 for a PBM).
type pnmHeader struct {
	magic  string
	width  int
	height int
	maxval int
}

// decodePNM decodes the binary PNM files pdfimages writes when it is not
// asked for PNG: P6 for colour, P5 for grey and P4 for one bit a pixel, with
// at most one byte a sample. A P6 decodes to an image.RGBA, or to an
// image.Gray when its three channels are equal everywhere, because
// pdfimages writes grey pictures as P6 too. P5 and P4 decode to an
// image.Gray. Anything else, and an image over maxDecodePixels, is refused
// before its pixels are allocated.
func decodePNM(reader io.Reader) (image.Image, error) {
	buffered := bufio.NewReader(reader)

	header, err := readPNMHeader(buffered)
	if err != nil {
		return nil, err
	}

	if header.width*header.height > maxDecodePixels {
		return nil, fmt.Errorf("%dx%d pixels is too large to decode", header.width, header.height)
	}

	switch header.magic {
	case "P4":
		return decodePBMRaster(buffered, header)
	case "P5":
		return decodePGMRaster(buffered, header)
	default:
		return decodePPMRaster(buffered, header)
	}
}

// readPNMHeader reads the header of a binary PNM file up to and including
// the single whitespace byte that separates it from the raster.
func readPNMHeader(reader *bufio.Reader) (pnmHeader, error) {
	magic := make([]byte, 2)
	if _, err := io.ReadFull(reader, magic); err != nil {
		return pnmHeader{}, fmt.Errorf("not a PNM file: %w", err)
	}

	header := pnmHeader{magic: string(magic), maxval: 1}
	if header.magic != "P4" && header.magic != "P5" && header.magic != "P6" {
		return pnmHeader{}, fmt.Errorf("not a binary PNM file (P4, P5 or P6): starts with %q", magic)
	}

	// A PBM has no maxval: every sample is one bit.
	fields := []*int{&header.width, &header.height}
	if header.magic != "P4" {
		fields = append(fields, &header.maxval)
	}

	for _, field := range fields {
		value, err := readPNMNumber(reader)
		if err != nil {
			return pnmHeader{}, err
		}
		*field = value
	}

	if header.maxval > 255 {
		return pnmHeader{}, fmt.Errorf("PNM maxval %d takes two bytes a sample, only one is read", header.maxval)
	}

	separator, err := reader.ReadByte()
	if err != nil || !isPNMSpace(separator) {
		return pnmHeader{}, errors.New("PNM header does not end in whitespace")
	}

	return header, nil
}

// readPNMNumber skips the whitespace and comments before the next number of
// a PNM header and reads it. No number in a header can be zero, so a zero
// is refused like a missing one.
func readPNMNumber(reader *bufio.Reader) (int, error) {
	if err := skipPNMSpace(reader); err != nil {
		return 0, err
	}

	value := 0
	digits := 0

	for {
		next, err := reader.ReadByte()
		if err != nil {
			break
		}

		if next < '0' || next > '9' {
			reader.UnreadByte()
			break
		}

		value = value*10 + int(next-'0')
		digits++

		if value > maxPNMHeaderNumber {
			return 0, fmt.Errorf("PNM header number is over %d", maxPNMHeaderNumber)
		}
	}

	if digits == 0 || value == 0 {
		return 0, errors.New("PNM header is missing a size or maxval")
	}

	return value, nil
}

// skipPNMSpace skips the whitespace and the comments, from "#" to the end
// of the line, that may come before a number of a PNM header.
func skipPNMSpace(reader *bufio.Reader) error {
	inComment := false

	for {
		next, err := reader.ReadByte()
		if err != nil {
			return fmt.Errorf("PNM header is truncated: %w", err)
		}

		switch {
		case inComment:
			inComment = next != '\n' && next != '\r'
		case next == '#':
			inComment = true
		case !isPNMSpace(next):
			reader.UnreadByte()
			return nil
		}
	}
}

// isPNMSpace reports whether b is one of the whitespace bytes the PNM
// format allows between header fields.
func isPNMSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\v' || b == '\f' || b == '\r'
}

// decodePBMRaster reads the raster of a P4 file. Each row is padded to a
// whole byte, the leftmost pixel is the high bit, and a set bit is black.
func decodePBMRaster(reader io.Reader, header pnmHeader) (image.Image, error) {
	gray := image.NewGray(image.Rect(0, 0, header.width, header.height))
	row := make([]byte, (header.width+7)/8)

	for y := range header.height {
		if _, err := io.ReadFull(reader, row); err != nil {
			return nil, fmt.Errorf("PNM raster is truncated: %w", err)
		}

		// A new image.Gray is all black, so only the clear bits are set.
		pixels := gray.Pix[y*gray.Stride : y*gray.Stride+header.width]
		for x := range pixels {
			if row[x/8]&(0x80>>(x%8)) == 0 {
				pixels[x] = 255
			}
		}
	}

	return gray, nil
}

// decodePGMRaster reads the raster of a P5 file, one byte a pixel.
func decodePGMRaster(reader io.Reader, header pnmHeader) (image.Image, error) {
	gray := image.NewGray(image.Rect(0, 0, header.width, header.height))
	if _, err := io.ReadFull(reader, gray.Pix); err != nil {
		return nil, fmt.Errorf("PNM raster is truncated: %w", err)
	}

	if err := stretchSamples(gray.Pix, header.maxval); err != nil {
		return nil, err
	}

	return gray, nil
}

// decodePPMRaster reads the raster of a P6 file, three bytes a pixel, into
// an opaque image.RGBA, or into an image.Gray when every pixel is grey.
func decodePPMRaster(reader io.Reader, header pnmHeader) (image.Image, error) {
	pixelCount := header.width * header.height

	samples := make([]byte, pixelCount*3)
	if _, err := io.ReadFull(reader, samples); err != nil {
		return nil, fmt.Errorf("PNM raster is truncated: %w", err)
	}

	if err := stretchSamples(samples, header.maxval); err != nil {
		return nil, err
	}

	bounds := image.Rect(0, 0, header.width, header.height)

	if isGreyRGB(samples) {
		gray := image.NewGray(bounds)
		for pixel := range pixelCount {
			gray.Pix[pixel] = samples[pixel*3]
		}
		return gray, nil
	}

	rgba := image.NewRGBA(bounds)
	for pixel := range pixelCount {
		copy(rgba.Pix[pixel*4:pixel*4+3], samples[pixel*3:pixel*3+3])
		rgba.Pix[pixel*4+3] = 255
	}

	return rgba, nil
}

// isGreyRGB reports whether every pixel of an RGB raster has its three
// channels equal. It stops at the first colour pixel, so a colour picture
// costs little more than its first row.
func isGreyRGB(samples []byte) bool {
	for offset := 0; offset+2 < len(samples); offset += 3 {
		if samples[offset] != samples[offset+1] || samples[offset] != samples[offset+2] {
			return false
		}
	}

	return true
}

// stretchSamples scales samples from 0..maxval to 0..255 in place, rounding
// to the nearest value. A sample above maxval means a damaged file.
func stretchSamples(samples []byte, maxval int) error {
	if maxval == 255 {
		return nil
	}

	for index, sample := range samples {
		if int(sample) > maxval {
			return fmt.Errorf("PNM sample %d is over maxval %d", sample, maxval)
		}
		samples[index] = uint8((int(sample)*255 + maxval/2) / maxval)
	}

	return nil
}

// OutlineEntry represents a heading/section in a document outline.
type OutlineEntry struct {
	Level int    `json:"level"`
	Title string `json:"title"`
	Page  int    `json:"page"`
}

// TableData represents a table extracted from a document.
type TableData struct {
	Page int        `json:"page"`
	Rows [][]string `json:"rows"`
}

// FormatInfo describes a supported document format.
type FormatInfo struct {
	Extension string `json:"extension"`
	Status    string `json:"status"`
	Requires  string `json:"requires"`
	Installed bool   `json:"installed"`
}

// ListFormats returns information about supported document formats and dependency status.
func (r *Reader) ListFormats() []FormatInfo {
	_, errPdftotext := exec.LookPath(r.pdftotextBin)
	_, errTesseract := exec.LookPath(r.tesseractBin)
	_, errPandoc := exec.LookPath(r.pandocBin)

	formats := []FormatInfo{
		{Extension: ".pdf", Status: "supported", Requires: "poppler", Installed: errPdftotext == nil},
		{Extension: ".txt", Status: "supported", Requires: "none", Installed: true},
		{Extension: ".md", Status: "supported", Requires: "none", Installed: true},
		{Extension: ".csv", Status: "supported", Requires: "none", Installed: true},
	}

	for _, format := range pandocFormats {
		formats = append(formats, FormatInfo{Extension: format.extension, Status: "optional", Requires: "pandoc", Installed: errPandoc == nil})
	}

	formats = append(formats, FormatInfo{Extension: strings.Join(imageExtensions, "/"), Status: "optional", Requires: "tesseract", Installed: errTesseract == nil})

	return formats
}

// ReadImage performs OCR on a standalone image file using tesseract directly.
func (r *Reader) ReadImage(ctx context.Context, filename string, language string) (string, error) {
	safe, err := r.sanitizeImageFilename(filename)
	if err != nil {
		return "", err
	}

	language, err = ocrLanguage(language)
	if err != nil {
		return "", err
	}

	path, err := r.resolveDocument(safe)
	if err != nil {
		return "", err
	}

	if _, err := exec.LookPath(r.tesseractBin); err != nil {
		return "", fmt.Errorf("tesseract not available: install with 'brew install tesseract'")
	}

	// Open image and pipe to tesseract via stdin
	imgData, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open image file: %w", withoutPath(err))
	}
	defer imgData.Close()

	tesseractArgs := []string{"stdin", "stdout", "-l", language}
	text, err := runTool(ctx, ocrPageTimeout, imgData, path, r.tesseractBin, tesseractArgs...)

	// As with a PDF page, text printed before a non-zero exit is kept unless
	// the deadline cut it short.
	if err != nil && (text == "" || isContextError(err)) {
		return "", err
	}

	return text, nil
}

// GetDocumentOutline extracts heading structure from a document.
func (r *Reader) GetDocumentOutline(ctx context.Context, filename string) ([]OutlineEntry, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return nil, err
	}

	path, err := r.resolveDocument(safe)
	if err != nil {
		return nil, err
	}

	ext := strings.ToLower(filepath.Ext(safe))
	if ext != ".md" && ext != ".txt" && ext != ".pdf" {
		return nil, fmt.Errorf("outline extraction not supported for %s files", ext)
	}

	// The text comes from read_document's cache, with the OCR fallback for
	// a scanned PDF.
	text, err := r.extractFull(ctx, safe, path)
	if err != nil {
		return nil, err
	}

	if ext == ".pdf" {
		return r.outlinePDF(text), nil
	}

	// A .txt is searched for Markdown headings too.
	return r.outlineMarkdown(text), nil
}

// outlineMarkdown parses markdown headings from the text of a file.
func (r *Reader) outlineMarkdown(text string) []OutlineEntry {
	var entries []OutlineEntry
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			level := 0
			for _, ch := range trimmed {
				if ch == '#' {
					level++
				} else {
					break
				}
			}
			title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if title != "" && level <= 6 {
				entries = append(entries, OutlineEntry{
					Level: level,
					Title: title,
					Page:  i + 1, // line number as "page" for non-PDF
				})
			}
		}
	}

	return entries
}

// outlinePDF extracts heading-like structures from PDF text, counting pages
// by the form feed that ends each one.
func (r *Reader) outlinePDF(text string) []OutlineEntry {
	var entries []OutlineEntry
	lines := strings.Split(text, "\n")
	pageNum := 1

	for i, line := range lines {
		// Track page breaks
		if strings.Contains(line, "\f") {
			pageNum++
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Skip long lines (not headings)
		if len(trimmed) >= 80 {
			continue
		}

		isHeading := false
		level := 1

		// Check if followed by a blank line (heading pattern)
		followedByBlank := (i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "")

		// ALL-CAPS short lines (< 60 chars)
		if len(trimmed) < 60 && trimmed == strings.ToUpper(trimmed) && len(trimmed) > 3 {
			// Verify it has at least some letters
			hasLetter := false
			for _, ch := range trimmed {
				if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
					hasLetter = true
					break
				}
			}
			if hasLetter {
				isHeading = true
				level = 1
			}
		}

		// Numbered section patterns: "1.", "1.1", "1.1.1", "Chapter X"
		if !isHeading {
			if matched := matchNumberedHeading(trimmed); matched > 0 {
				isHeading = true
				level = matched
			}
		}

		// Short line followed by blank (potential heading)
		if !isHeading && followedByBlank && len(trimmed) < 60 && len(trimmed) > 2 {
			// Heuristic: doesn't end with common sentence endings
			if !strings.HasSuffix(trimmed, ".") && !strings.HasSuffix(trimmed, ",") && !strings.HasSuffix(trimmed, ";") {
				isHeading = true
				level = 2
			}
		}

		if isHeading {
			entries = append(entries, OutlineEntry{
				Level: level,
				Title: trimmed,
				Page:  pageNum,
			})
		}
	}

	return entries
}

// matchNumberedHeading checks if a line matches numbered heading patterns.
// Returns the heading level (1-3) or 0 if no match.
func matchNumberedHeading(line string) int {
	// "Chapter X" pattern
	if strings.HasPrefix(strings.ToLower(line), "chapter ") {
		return 1
	}
	// "Part X" pattern
	if strings.HasPrefix(strings.ToLower(line), "part ") {
		return 1
	}

	// Numbered patterns: "1.", "1.1", "1.1.1"
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0
	}
	num := parts[0]

	// Count dots to determine level
	if len(num) > 0 {
		// Strip trailing dot
		numClean := strings.TrimRight(num, ".")
		segments := strings.Split(numClean, ".")
		allDigits := true
		for _, seg := range segments {
			if seg == "" {
				allDigits = false
				break
			}
			for _, ch := range seg {
				if ch < '0' || ch > '9' {
					allDigits = false
					break
				}
			}
			if !allDigits {
				break
			}
		}
		if allDigits && len(segments) >= 1 && len(segments) <= 4 {
			return len(segments)
		}
	}

	return 0
}

// ExtractTables extracts table-like structures from a document.
func (r *Reader) ExtractTables(ctx context.Context, filename string, page int) ([]TableData, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return nil, err
	}

	path, err := r.resolveDocument(safe)
	if err != nil {
		return nil, err
	}

	ext := strings.ToLower(filepath.Ext(safe))
	if ext != ".csv" && ext != ".md" && ext != ".txt" && ext != ".pdf" {
		return nil, fmt.Errorf("table extraction not supported for %s files", ext)
	}

	if ext == ".pdf" {
		return r.tablesPDF(ctx, safe, path, page)
	}

	// Text formats come from read_document's cache like everything else.
	text, err := r.extractFull(ctx, safe, path)
	if err != nil {
		return nil, err
	}

	switch ext {
	case ".csv":
		return r.tablesCSV(text)
	case ".md":
		return r.tablesMarkdown(text), nil
	default:
		return r.detectTablesFromLayout(text, 0), nil
	}
}

// tablesCSV parses the text of a CSV file as a single table with encoding/csv,
// so a quoted field may hold commas, doubled quotes and line breaks. Rows may
// have different lengths and every cell is trimmed, as the table of a
// hand-written CSV usually needs.
func (r *Reader) tablesCSV(text string) ([]TableData, error) {
	csvReader := csv.NewReader(strings.NewReader(text))
	csvReader.FieldsPerRecord = -1

	// A stray quote inside an unquoted field is common in exported files;
	// LazyQuotes keeps it as text instead of refusing the whole table.
	csvReader.LazyQuotes = true

	records, err := csvReader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to parse CSV: %w", err)
	}

	rows := [][]string{}
	for _, record := range records {
		cells := make([]string, len(record))
		for index, field := range record {
			cells[index] = strings.TrimSpace(field)
		}
		rows = append(rows, cells)
	}

	return []TableData{{Page: 1, Rows: rows}}, nil
}

// tablesMarkdown parses pipe-delimited tables from the text of a markdown
// file.
func (r *Reader) tablesMarkdown(text string) []TableData {
	lines := strings.Split(text, "\n")
	var tables []TableData
	var currentRows [][]string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "|") && strings.Count(trimmed, "|") >= 2 {
			// Skip separator lines (e.g., |---|---|)
			stripped := strings.ReplaceAll(trimmed, "|", "")
			stripped = strings.ReplaceAll(stripped, "-", "")
			stripped = strings.ReplaceAll(stripped, ":", "")
			stripped = strings.TrimSpace(stripped)
			if stripped == "" {
				continue // separator line
			}

			// Parse pipe-delimited row
			cells := parsePipeRow(trimmed)
			currentRows = append(currentRows, cells)
		} else {
			// End of table
			if len(currentRows) > 0 {
				tables = append(tables, TableData{Page: 1, Rows: currentRows})
				currentRows = nil
			}
		}
	}

	// Don't forget last table
	if len(currentRows) > 0 {
		tables = append(tables, TableData{Page: 1, Rows: currentRows})
	}

	return tables
}

// parsePipeRow splits a pipe-delimited row into cells.
func parsePipeRow(line string) []string {
	// Trim leading/trailing pipes
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "|") {
		line = line[1:]
	}
	if strings.HasSuffix(line, "|") {
		line = line[:len(line)-1]
	}

	parts := strings.Split(line, "|")
	var cells []string
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	return cells
}

// tablesPDF extracts table-like structures from PDF text using layout
// analysis. The text is read as read_document reads it: the whole document
// from the cache, or one page checked against the page count, both with the
// OCR fallback for scanned pages.
func (r *Reader) tablesPDF(ctx context.Context, filename, path string, page int) ([]TableData, error) {
	var text string
	var err error
	if page > 0 {
		text, err = r.readPageRanges(ctx, path, [][2]int{{page, page}})
	} else {
		text, err = r.extractFull(ctx, filename, path)
	}
	if err != nil {
		return nil, err
	}

	return r.detectTablesFromLayout(text, page), nil
}

// detectTablesFromLayout detects table-like patterns in text with spatial layout.
func (r *Reader) detectTablesFromLayout(text string, startPage int) []TableData {
	lines := strings.Split(text, "\n")
	pageNum := startPage
	if pageNum == 0 {
		pageNum = 1
	}

	var tables []TableData
	var currentRows [][]string
	currentPage := pageNum

	for _, line := range lines {
		// Track page breaks
		if strings.Contains(line, "\f") {
			if len(currentRows) >= 2 {
				tables = append(tables, TableData{Page: currentPage, Rows: currentRows})
			}
			currentRows = nil
			pageNum++
			currentPage = pageNum
			continue
		}

		row := detectTableRow(line)
		if row != nil && len(row) >= 2 {
			if currentRows == nil {
				currentPage = pageNum
			}
			currentRows = append(currentRows, row)
		} else {
			// End of potential table (need at least 2 rows)
			if len(currentRows) >= 2 {
				tables = append(tables, TableData{Page: currentPage, Rows: currentRows})
			}
			currentRows = nil
		}
	}

	// Don't forget last table
	if len(currentRows) >= 2 {
		tables = append(tables, TableData{Page: currentPage, Rows: currentRows})
	}

	return tables
}

// detectTableRow checks if a line looks like a table row and returns its cells.
func detectTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}

	// Pipe-delimited
	if strings.Contains(trimmed, "|") && strings.Count(trimmed, "|") >= 2 {
		// Skip separator lines
		stripped := strings.ReplaceAll(trimmed, "|", "")
		stripped = strings.ReplaceAll(stripped, "-", "")
		stripped = strings.ReplaceAll(stripped, ":", "")
		stripped = strings.TrimSpace(stripped)
		if stripped == "" {
			return nil
		}
		return parsePipeRow(trimmed)
	}

	// Tab-delimited
	if strings.Contains(line, "\t") {
		parts := strings.Split(line, "\t")
		if len(parts) >= 2 {
			var cells []string
			for _, p := range parts {
				cells = append(cells, strings.TrimSpace(p))
			}
			return cells
		}
	}

	// Multi-space delimited (3+ spaces between columns)
	if strings.Contains(line, "   ") {
		parts := splitMultiSpace(line)
		if len(parts) >= 2 {
			return parts
		}
	}

	return nil
}

// ConvertToMarkdown converts a document to clean Markdown format.
// For .md files, returns raw content. For .txt/.csv, wraps in code blocks.
// For .pdf, extracts text and formats headings. The pandoc formats are
// written by pandoc as GitHub-flavoured Markdown.
func (r *Reader) ConvertToMarkdown(ctx context.Context, filename string) (string, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return "", err
	}

	path, err := r.resolveDocument(safe)
	if err != nil {
		return "", err
	}

	if format, ok := pandocFormatFor(safe); ok {
		return r.convertWithPandoc(ctx, path, format, pandocMarkdown)
	}

	ext := strings.ToLower(filepath.Ext(safe))

	switch ext {
	case ".md":
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("failed to read file: %w", withoutPath(err))
		}
		return string(data), nil

	case ".txt":
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("failed to read file: %w", withoutPath(err))
		}
		return fmt.Sprintf("```text\n%s\n```", string(data)), nil

	case ".csv":
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("failed to read file: %w", withoutPath(err))
		}
		return fmt.Sprintf("```csv\n%s\n```", string(data)), nil

	case ".pdf":
		text, err := r.extractFull(ctx, safe, path)
		if err != nil {
			return "", err
		}
		return r.pdfTextToMarkdown(text), nil

	default:
		// Anything else that passed the allowlist, an image for instance,
		// goes through read_document's extraction with a header.
		text, err := r.extractFull(ctx, safe, path)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("# %s\n\n> Converted from %s format\n\n%s", safe, ext, text), nil
	}
}

// pdfTextToMarkdown converts extracted PDF text to Markdown with heading detection.
func (r *Reader) pdfTextToMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	var result []string
	pageNum := 1

	for i, line := range lines {
		// Track page breaks
		if strings.Contains(line, "\f") {
			pageNum++
			// Replace form feed with a horizontal rule
			cleaned := strings.ReplaceAll(line, "\f", "")
			if strings.TrimSpace(cleaned) != "" {
				result = append(result, "\n---\n")
				result = append(result, cleaned)
			} else {
				result = append(result, "\n---\n")
			}
			_ = pageNum
			continue
		}

		trimmed := strings.TrimSpace(line)

		// Empty lines become paragraph breaks
		if trimmed == "" {
			result = append(result, "")
			continue
		}

		// Detect headings: ALL-CAPS lines (short, with letters)
		if len(trimmed) < 60 && len(trimmed) > 3 && trimmed == strings.ToUpper(trimmed) {
			hasLetter := false
			for _, ch := range trimmed {
				if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
					hasLetter = true
					break
				}
			}
			if hasLetter {
				result = append(result, fmt.Sprintf("## %s", trimmed))
				continue
			}
		}

		// Detect headings: short lines before blanks (not ending with punctuation)
		followedByBlank := (i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "")
		if followedByBlank && len(trimmed) < 60 && len(trimmed) > 2 {
			if !strings.HasSuffix(trimmed, ".") && !strings.HasSuffix(trimmed, ",") && !strings.HasSuffix(trimmed, ";") {
				// Check for numbered heading patterns
				if matched := matchNumberedHeading(trimmed); matched > 0 {
					prefix := "##"
					if matched >= 2 {
						prefix = "###"
					}
					if matched >= 3 {
						prefix = "####"
					}
					result = append(result, fmt.Sprintf("%s %s", prefix, trimmed))
					continue
				}
				// Generic short heading
				result = append(result, fmt.Sprintf("## %s", trimmed))
				continue
			}
		}

		// Regular line
		result = append(result, trimmed)
	}

	return strings.Join(result, "\n")
}

// splitMultiSpace splits a line by runs of 3+ spaces.
func splitMultiSpace(line string) []string {
	var parts []string
	var current strings.Builder
	spaceCount := 0

	for _, ch := range line {
		if ch == ' ' {
			spaceCount++
		} else {
			if spaceCount >= 3 && current.Len() > 0 {
				parts = append(parts, strings.TrimSpace(current.String()))
				current.Reset()
			} else {
				for i := 0; i < spaceCount; i++ {
					current.WriteByte(' ')
				}
			}
			spaceCount = 0
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, strings.TrimSpace(current.String()))
	}
	return parts
}

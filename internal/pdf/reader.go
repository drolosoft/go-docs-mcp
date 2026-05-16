package pdf

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DocumentInfo holds metadata about a PDF file.
type DocumentInfo struct {
	Filename  string `json:"filename"`
	Title     string `json:"title"`
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

// ImageInfo holds information about an extracted image.
type ImageInfo struct {
	Page       int    `json:"page"`
	Index      int    `json:"index"`
	Format     string `json:"format"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DataBase64 string `json:"data_base64"`
}

// cacheEntry stores extracted text with its modification time for invalidation.
type cacheEntry struct {
	text  string
	mtime time.Time
}

// Reader handles PDF text extraction via pdftotext with in-memory caching.
// Supports automatic OCR fallback for image-based PDFs using tesseract.
type Reader struct {
	docsDir       string
	pdftotextBin  string
	pdfinfoBin    string
	pdfimagesBin  string
	pdftoppmBin   string
	tesseractBin  string
	hasOCR        bool // true if both pdftoppm and tesseract are available

	mu    sync.RWMutex
	cache map[string]*cacheEntry
}

// NewReader creates a new PDF reader for the given documents directory.
func NewReader(docsDir string) *Reader {
	pdftotextBin := "pdftotext"
	pdfinfoBin := "pdfinfo"
	pdfimagesBin := "pdfimages"
	pdftoppmBin := "pdftoppm"
	tesseractBin := "tesseract"

	// Check for Homebrew installation on macOS
	if _, err := os.Stat("/opt/homebrew/bin/pdftotext"); err == nil {
		pdftotextBin = "/opt/homebrew/bin/pdftotext"
	}
	if _, err := os.Stat("/opt/homebrew/bin/pdfinfo"); err == nil {
		pdfinfoBin = "/opt/homebrew/bin/pdfinfo"
	}
	if _, err := os.Stat("/opt/homebrew/bin/pdfimages"); err == nil {
		pdfimagesBin = "/opt/homebrew/bin/pdfimages"
	}
	if _, err := os.Stat("/opt/homebrew/bin/pdftoppm"); err == nil {
		pdftoppmBin = "/opt/homebrew/bin/pdftoppm"
	}
	if _, err := os.Stat("/opt/homebrew/bin/tesseract"); err == nil {
		tesseractBin = "/opt/homebrew/bin/tesseract"
	}

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
		hasOCR:       hasOCR,
		cache:        make(map[string]*cacheEntry),
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

// sanitizeFilename ensures the filename is safe (no directory traversal) and is a .pdf file.
func (r *Reader) sanitizeFilename(filename string) (string, error) {
	base := filepath.Base(filename)
	if base != filename {
		return "", fmt.Errorf("invalid filename: directory traversal not allowed")
	}
	if !strings.HasSuffix(strings.ToLower(base), ".pdf") {
		return "", fmt.Errorf("invalid filename: only .pdf files are supported")
	}
	return base, nil
}

// fullPath returns the full filesystem path for a sanitized filename.
func (r *Reader) fullPath(filename string) string {
	return filepath.Join(r.docsDir, filename)
}

// ListDocuments returns metadata for all PDF files in the documents directory.
func (r *Reader) ListDocuments() ([]DocumentInfo, error) {
	entries, err := os.ReadDir(r.docsDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read documents directory %s: %w", r.docsDir, err)
	}

	var docs []DocumentInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		pages := r.getPageCount(filepath.Join(r.docsDir, name))
		title := strings.TrimSuffix(name, filepath.Ext(name))

		docs = append(docs, DocumentInfo{
			Filename:  name,
			Title:     title,
			Pages:     pages,
			SizeBytes: info.Size(),
		})
	}

	return docs, nil
}

// getPageCount uses pdfinfo to get the page count for a PDF file.
func (r *Reader) getPageCount(path string) int {
	cmd := exec.Command(r.pdfinfoBin, path)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Pages:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				n, err := strconv.Atoi(parts[1])
				if err == nil {
					return n
				}
			}
		}
	}
	return 0
}

// ReadDocument extracts text from a PDF file. If page > 0, only that page is returned.
// Automatically falls back to OCR if pdftotext returns empty text.
func (r *Reader) ReadDocument(filename string, page int) (string, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return "", err
	}

	path := r.fullPath(safe)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("document not found: %s", safe)
	}

	if page > 0 {
		text, method, err := r.readWithOCRFallback(path, page, page)
		if err != nil {
			return "", err
		}
		_ = method // method info available for logging if needed
		return text, nil
	}

	return r.extractFull(safe, path)
}

// ReadDocumentPages extracts text from specific page ranges of a PDF file.
// Supports ranges like "1-5", "10", "1-3,7,10-12".
func (r *Reader) ReadDocumentPages(filename, pagesStr string) (string, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return "", err
	}

	path := r.fullPath(safe)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("document not found: %s", safe)
	}

	return r.extractPageRanges(path, pagesStr)
}

// ReadFile extracts full text from an arbitrary PDF file path (used for URL downloads).
// Automatically falls back to OCR if pdftotext returns empty text.
func (r *Reader) ReadFile(path string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("file not found: %s", path)
	}
	text, _, err := r.readWithOCRFallback(path, 0, 0)
	return text, err
}

// ReadFilePages extracts text from specific page ranges of an arbitrary PDF file path.
func (r *Reader) ReadFilePages(path, pagesStr string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("file not found: %s", path)
	}
	return r.extractPageRanges(path, pagesStr)
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

// extractPageRanges extracts text from multiple page ranges and combines the results.
// Falls back to OCR if pdftotext returns empty text.
func (r *Reader) extractPageRanges(path, pagesStr string) (string, error) {
	ranges, err := parsePageRanges(pagesStr)
	if err != nil {
		return "", err
	}

	var parts []string
	for _, rng := range ranges {
		text, _, err := r.readWithOCRFallback(path, rng[0], rng[1])
		if err != nil {
			return "", fmt.Errorf("error extracting pages %d-%d: %w", rng[0], rng[1], err)
		}
		parts = append(parts, text)
	}

	return strings.Join(parts, "\n"), nil
}

// extractFull extracts the full text of a PDF, using cache when possible.
// Falls back to OCR if pdftotext returns empty text.
func (r *Reader) extractFull(filename, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("cannot stat file: %w", err)
	}
	mtime := info.ModTime()

	// Check cache
	r.mu.RLock()
	if entry, ok := r.cache[filename]; ok && entry.mtime.Equal(mtime) {
		r.mu.RUnlock()
		return entry.text, nil
	}
	r.mu.RUnlock()

	// Extract text with OCR fallback
	text, _, err := r.readWithOCRFallback(path, 0, 0)
	if err != nil {
		return "", err
	}

	// Update cache
	r.mu.Lock()
	r.cache[filename] = &cacheEntry{text: text, mtime: mtime}
	r.mu.Unlock()

	return text, nil
}

// extractPage extracts text from a specific page range.
func (r *Reader) extractPage(path string, firstPage, lastPage int) (string, error) {
	return r.runPdftotext(path, firstPage, lastPage)
}

// runPdftotext invokes pdftotext with the given options.
func (r *Reader) runPdftotext(path string, firstPage, lastPage int) (string, error) {
	args := []string{"-layout"}
	if firstPage > 0 {
		args = append(args, "-f", strconv.Itoa(firstPage))
	}
	if lastPage > 0 {
		args = append(args, "-l", strconv.Itoa(lastPage))
	}
	args = append(args, path, "-")

	cmd := exec.Command(r.pdftotextBin, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext failed: %w", err)
	}
	return string(out), nil
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

// ocrPage runs OCR on a single page of a PDF using pdftoppm + tesseract.
// Uses TIFF format with temp files, piping the TIFF data via stdin to tesseract
// to work around leptonica file-open bugs on macOS.
func (r *Reader) ocrPage(path string, page int, language string) (string, error) {
	if language == "" {
		language = "eng"
	}

	// Create temp dir for the TIFF file
	tmpDir, err := os.MkdirTemp("", "go-pdf-mcp-ocr-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	prefix := filepath.Join(tmpDir, "page")

	// pdftoppm -tiff -r 200 -f N -l N -singlefile <pdf> <prefix>
	// This creates <prefix>.tif
	pdftoppmArgs := []string{"-tiff", "-r", "200", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-singlefile", path, prefix}
	pdftoppmCmd := exec.Command(r.pdftoppmBin, pdftoppmArgs...)
	if out, err := pdftoppmCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("pdftoppm failed on page %d: %w (output: %s)", page, err, string(out))
	}

	tiffPath := prefix + ".tif"
	if _, err := os.Stat(tiffPath); os.IsNotExist(err) {
		return "", fmt.Errorf("pdftoppm did not produce output for page %d", page)
	}

	// Pipe TIFF data via stdin to tesseract to work around leptonica bug
	// where tesseract cannot open files directly on some macOS installations
	tiffData, err := os.Open(tiffPath)
	if err != nil {
		return "", fmt.Errorf("failed to open TIFF file: %w", err)
	}
	defer tiffData.Close()

	tesseractArgs := []string{"stdin", "stdout", "-l", language}
	tesseractCmd := exec.Command(r.tesseractBin, tesseractArgs...)
	tesseractCmd.Stdin = tiffData

	var tesseractOut strings.Builder
	var tesseractErr strings.Builder
	tesseractCmd.Stdout = &tesseractOut
	tesseractCmd.Stderr = &tesseractErr

	if err := tesseractCmd.Run(); err != nil {
		if tesseractOut.Len() > 0 {
			return tesseractOut.String(), nil
		}
		return "", fmt.Errorf("tesseract failed on page %d: %w (stderr: %s)", page, err, tesseractErr.String())
	}

	return tesseractOut.String(), nil
}

// ocrDocument runs OCR on all pages (or a specific page) of a PDF.
// Returns the extracted text and the method used.
func (r *Reader) ocrDocument(path string, page int, language string) (string, error) {
	if !r.hasOCR {
		return "", fmt.Errorf("OCR not available: tesseract and/or pdftoppm not installed")
	}

	if page > 0 {
		return r.ocrPage(path, page, language)
	}

	// Get total page count
	totalPages := r.getPageCount(path)
	if totalPages == 0 {
		// Fallback: try just page 1
		totalPages = 1
	}

	var parts []string
	for p := 1; p <= totalPages; p++ {
		text, err := r.ocrPage(path, p, language)
		if err != nil {
			// Log but continue with other pages
			parts = append(parts, fmt.Sprintf("[OCR failed on page %d: %v]", p, err))
			continue
		}
		if p > 1 {
			parts = append(parts, fmt.Sprintf("\n--- Page %d ---\n", p))
		}
		parts = append(parts, text)
	}

	return strings.Join(parts, ""), nil
}

// OCRDocument performs OCR on a document in the configured directory.
// Forces OCR regardless of whether pdftotext works. Useful for garbled text.
func (r *Reader) OCRDocument(filename string, page int, language string) (string, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return "", err
	}

	path := r.fullPath(safe)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("document not found: %s", safe)
	}

	return r.ocrDocument(path, page, language)
}

// readWithOCRFallback extracts text from a PDF, falling back to OCR if pdftotext
// returns empty/whitespace-only text. Returns the text and extraction method used.
func (r *Reader) readWithOCRFallback(path string, firstPage, lastPage int) (text string, method string, err error) {
	text, err = r.runPdftotext(path, firstPage, lastPage)
	if err != nil {
		return "", "", err
	}

	// Determine page count for threshold calculation
	pageCount := 1
	if firstPage > 0 && lastPage > 0 {
		pageCount = lastPage - firstPage + 1
	} else if firstPage == 0 && lastPage == 0 {
		pageCount = r.getPageCount(path)
		if pageCount == 0 {
			pageCount = 1
		}
	}

	if !isTextEmpty(text, pageCount) {
		return text, "pdftotext", nil
	}

	// Text is empty or near-empty; try OCR fallback
	if !r.hasOCR {
		return text, "pdftotext (empty, no OCR available)", nil
	}

	page := 0
	if firstPage > 0 && firstPage == lastPage {
		page = firstPage
	}
	// For ranges, OCR all pages in range
	if firstPage > 0 && lastPage > firstPage {
		var parts []string
		for p := firstPage; p <= lastPage; p++ {
			ocrText, ocrErr := r.ocrPage(path, p, "eng")
			if ocrErr != nil {
				parts = append(parts, fmt.Sprintf("[OCR failed on page %d: %v]", p, ocrErr))
				continue
			}
			if p > firstPage {
				parts = append(parts, fmt.Sprintf("\n--- Page %d ---\n", p))
			}
			parts = append(parts, ocrText)
		}
		return strings.Join(parts, ""), "ocr", nil
	}

	ocrText, ocrErr := r.ocrDocument(path, page, "eng")
	if ocrErr != nil {
		// Return the original (possibly empty) pdftotext result
		return text, "pdftotext (OCR fallback failed)", nil
	}
	return ocrText, "ocr", nil
}

// SearchDocument searches a document for lines matching the query (case-insensitive).
// Returns matching lines with 2 lines of context before and after, with page number hints.
func (r *Reader) SearchDocument(filename, query string) (string, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return "", err
	}

	path := r.fullPath(safe)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("document not found: %s", safe)
	}

	text, err := r.extractFull(safe, path)
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
	return header + strings.Join(results, "\n"), nil
}

// GetDocumentSummary returns the first 3 pages of text as a summary.
func (r *Reader) GetDocumentSummary(filename string) (string, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return "", err
	}

	path := r.fullPath(safe)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("document not found: %s", safe)
	}

	text, err := r.extractPage(path, 1, 3)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Summary (first 3 pages) of %s:\n\n%s", safe, text), nil
}

// GetDocumentMetadata returns full PDF metadata using pdfinfo.
func (r *Reader) GetDocumentMetadata(filename string) (*DocumentMetadata, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return nil, err
	}

	path := r.fullPath(safe)
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("document not found: %s", safe)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot stat file: %w", err)
	}

	cmd := exec.Command(r.pdfinfoBin, path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("pdfinfo failed: %w", err)
	}

	meta := &DocumentMetadata{
		FileSize: fi.Size(),
	}

	for _, line := range strings.Split(string(out), "\n") {
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

// ExtractImages extracts images from a PDF using pdfimages.
// If page > 0, only extracts from that page. Returns up to 10 images.
func (r *Reader) ExtractImages(filename string, page int) ([]ImageInfo, error) {
	safe, err := r.sanitizeFilename(filename)
	if err != nil {
		return nil, err
	}

	path := r.fullPath(safe)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("document not found: %s", safe)
	}

	return r.extractImagesFromPath(path, page)
}

// extractImagesFromPath extracts images from an arbitrary PDF path.
func (r *Reader) extractImagesFromPath(path string, page int) ([]ImageInfo, error) {
	// Create temp directory for extracted images
	tmpDir, err := os.MkdirTemp("", "go-pdf-mcp-images-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	prefix := filepath.Join(tmpDir, "img")

	// Build pdfimages command: -j outputs in native format (jpeg/png/etc)
	args := []string{"-j"}
	if page > 0 {
		args = append(args, "-f", strconv.Itoa(page), "-l", strconv.Itoa(page))
	}
	args = append(args, path, prefix)

	cmd := exec.Command(r.pdfimagesBin, args...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdfimages failed: %w", err)
	}

	// List the extracted images using pdfimages -list for metadata
	listArgs := []string{"-list"}
	if page > 0 {
		listArgs = append(listArgs, "-f", strconv.Itoa(page), "-l", strconv.Itoa(page))
	}
	listArgs = append(listArgs, path)

	listCmd := exec.Command(r.pdfimagesBin, listArgs...)
	listOut, _ := listCmd.Output()

	// Parse the list output for page/width/height info
	type imgMeta struct {
		page   int
		width  int
		height int
	}
	var metas []imgMeta
	lines := strings.Split(string(listOut), "\n")
	for i, line := range lines {
		if i < 2 { // skip header lines
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		p, _ := strconv.Atoi(fields[0])
		w, _ := strconv.Atoi(fields[3])
		h, _ := strconv.Atoi(fields[4])
		metas = append(metas, imgMeta{page: p, width: w, height: h})
	}

	// Read extracted image files
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read temp dir: %w", err)
	}

	var images []ImageInfo
	maxImages := 10

	for i, entry := range entries {
		if entry.IsDir() || i >= maxImages {
			break
		}

		imgPath := filepath.Join(tmpDir, entry.Name())
		data, err := os.ReadFile(imgPath)
		if err != nil {
			continue
		}

		// Determine format from extension
		ext := strings.TrimPrefix(filepath.Ext(entry.Name()), ".")
		if ext == "jpg" {
			ext = "jpeg"
		}

		img := ImageInfo{
			Index:      i,
			Format:     ext,
			DataBase64: base64.StdEncoding.EncodeToString(data),
		}

		// Attach metadata if available
		if i < len(metas) {
			img.Page = metas[i].page
			img.Width = metas[i].width
			img.Height = metas[i].height
		}

		images = append(images, img)
	}

	if len(images) == 0 {
		return []ImageInfo{}, nil
	}

	return images, nil
}

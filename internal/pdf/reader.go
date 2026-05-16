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
type Reader struct {
	docsDir       string
	pdftotextBin  string
	pdfinfoBin    string
	pdfimagesBin  string

	mu    sync.RWMutex
	cache map[string]*cacheEntry
}

// NewReader creates a new PDF reader for the given documents directory.
func NewReader(docsDir string) *Reader {
	pdftotextBin := "pdftotext"
	pdfinfoBin := "pdfinfo"
	pdfimagesBin := "pdfimages"

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

	return &Reader{
		docsDir:      docsDir,
		pdftotextBin: pdftotextBin,
		pdfinfoBin:   pdfinfoBin,
		pdfimagesBin: pdfimagesBin,
		cache:        make(map[string]*cacheEntry),
	}
}

// CheckDependencies verifies that pdftotext, pdfinfo, and pdfimages are available.
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
		return r.extractPage(path, page, page)
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
func (r *Reader) ReadFile(path string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("file not found: %s", path)
	}
	return r.runPdftotext(path, 0, 0)
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
func (r *Reader) extractPageRanges(path, pagesStr string) (string, error) {
	ranges, err := parsePageRanges(pagesStr)
	if err != nil {
		return "", err
	}

	var parts []string
	for _, rng := range ranges {
		text, err := r.runPdftotext(path, rng[0], rng[1])
		if err != nil {
			return "", fmt.Errorf("error extracting pages %d-%d: %w", rng[0], rng[1], err)
		}
		parts = append(parts, text)
	}

	return strings.Join(parts, "\n"), nil
}

// extractFull extracts the full text of a PDF, using cache when possible.
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

	// Extract text
	text, err := r.runPdftotext(path, 0, 0)
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

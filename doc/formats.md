# 📄 Supported Formats

What each format can do, which external program it needs, and the limits that
apply to a call. The values come from the constants in
[`internal/pdf/reader.go`](../internal/pdf/reader.go) and
[`main.go`](../main.go). How to install the external programs is in
[install.md](install.md).

---

## 📋 At a glance

| Format | Extensions | External tool | What works | Limits |
|--------|------------|---------------|------------|--------|
| PDF | `.pdf` | poppler (`pdftotext`, `pdfinfo`, `pdfimages`, `pdftoppm`), tesseract for OCR | text by page or range, search, summary (first 3 pages), metadata, outline, tables, images, OCR, Markdown | 200 pages per call, 120 s per OCR page, 10 min of OCR per call, images 1600 px and 8 MB per call |
| Plain text | `.txt` | none | text, search, summary (first 100 lines), outline, tables, Markdown | paging with `max_chars` |
| Markdown | `.md` | none | text, search, summary, outline, tables, Markdown | paging with `max_chars` |
| CSV | `.csv` | none | text, search, summary, tables, Markdown | paging with `max_chars` |
| Office, books, web, notebooks | `.docx`, `.xlsx`, `.pptx`, `.odt`, `.epub`, `.html`, `.htm`, `.rtf`, `.ipynb` | pandoc, with the reader for that format | text, search, summary (first 100 lines), Markdown | 60 s per pandoc run, paging with `max_chars` |
| Images | `.png`, `.jpg`, `.jpeg`, `.webp`, `.tiff`, `.tif`, `.bmp` | tesseract | OCR with `read_image` | 120 s per image |

"Text" means `read_document`, and the `docs://` resource of the file. "Markdown"
means `convert_to_markdown`. Pages, page ranges, `get_document_metadata`,
`extract_images` and `ocr_document` are for PDFs only; on any other file they
return an error.

---

## 📕 PDF

poppler does the work. `pdftotext -layout` gives the text with its columns in
place, and a form feed at the end of each page, which is how search, outline
and tables know the page a line is on. `pdfinfo` gives the page count and the
metadata, `pdfimages` the embedded images, and `pdftoppm` renders pages for
OCR.

- **Pages:** `page` on `read_document`, `extract_tables`, `extract_images` and
  `ocr_document`; `pages` (for example `"1-3,7,10-12"`) on `read_document` and
  `read_url`. A range that runs past the last page stops there; one that
  starts after it is an error. One call covers at most 200 pages.
- **OCR fallback:** when `pdftotext` returns fewer than 50 non-blank characters
  per page, the document is treated as a scan and read with OCR, if tesseract
  and `pdftoppm` are installed. Pages are rendered at 200 dpi. The language is
  `DOCS_MCP_OCR_LANG`, or English.
- **Forced OCR:** `ocr_document` runs OCR whatever `pdftotext` returns, and
  takes a `language`. It sends progress notifications when the call carries a
  progress token. A page that fails is marked in the text and the next page is
  tried.
- **Outline:** deduced from the text (short lines that look like headings),
  not read from the PDF bookmarks.
- **Tables:** deduced from the layout: lines with two or more `|`, with tabs,
  or with columns three or more spaces apart.
- **Markdown:** the text with the headings found in it marked as Markdown
  headings.
- **Images:** returned as `image/png` image content. Alpha and stencil masks
  are skipped. An image is scaled down to 1600 px on its longer side; one that
  is still over 4 MB is made smaller until it fits, and dropped if it does not
  fit at 64 px. An image over 40 megapixels is not decoded. One call returns
  10 images by default, at most 50, and 8 MB in total.

---

## 📝 Plain text, Markdown and CSV

Read straight from disk, with no external program. They have no pages, so long
files are read in slices with `offset` and `max_chars`.

- **Summary:** the first 100 lines.
- **Outline:** lines that start with `#`, in `.md` and in `.txt`. CSV has no
  outline.
- **Tables:** CSV is one table parsed with `encoding/csv`, so quoted fields can
  hold commas, doubled quotes and line breaks. Markdown tables come from rows
  with `|`. A `.txt` file is read like PDF text.
- **Markdown:** `.md` is returned as it is; `.txt` and `.csv` come back inside a
  code block.

---

## 📚 pandoc formats

DOCX, XLSX, PPTX, ODT, EPUB, HTML (`.html` and `.htm`), RTF and Jupyter
notebooks (IPYNB) go through pandoc. The reader is named with `-f`, so pandoc
never guesses the format from the file name.

- **Text:** pandoc's plain text writer, without line wrapping.
- **Markdown:** GitHub-flavoured Markdown, which keeps headings, emphasis and
  tables.
- **Not available:** pages, outline, tables, metadata and images. Use
  `convert_to_markdown` to see the headings and tables of these files.

The installed pandoc must have the reader for the format. Distribution
packages can be old: Ubuntu's pandoc has no `xlsx` or `pptx` reader.

The server asks `pandoc --list-input-formats` once, the first time it needs
it. `list_formats` reports each pandoc format as installed only when that
reader is there, and a file whose reader is missing gets an error that says
so. After installing a newer pandoc, restart the server so it asks again.

---

## 📷 Images

PNG, JPEG, WebP, TIFF and BMP are read with `read_image`, which passes the file
to tesseract and returns the recognised text. It takes a `language`, like
`ocr_document`. The other text tools do not read images.

---

## 🌐 Documents from a URL

`read_url` downloads one document and reads it. The `Content-Type` of the
response decides how:

| Content-Type | Read as |
|--------------|---------|
| `application/pdf`, `application/octet-stream`, or none | PDF, with `pages` and the OCR fallback |
| `text/markdown`, `text/x-markdown` | Markdown |
| `text/csv` | CSV |
| any other `text/*`, `text/html` included | plain text, so HTML comes back as markup |

Anything else is refused. The download stops at 50 MB and 60 s, the connection
must open within 10 s, and at most 10 redirects are followed. `read_url` has no
`offset` or `max_chars`. Which hosts it refuses, and how to turn it off, is in
the [Security section of the README](../README.md#-security).

---

## 🔎 What the listing says

`list_documents` gives one entry per supported file in the folder, images
included. Its `type` tells which tools fit:

| `type` | Files |
|--------|-------|
| `pdf` | `.pdf` |
| `text` | `.txt` |
| `markdown` | `.md` |
| `csv` | `.csv` |
| `docx`, `xlsx`, `pptx`, `odt`, `epub`, `rtf`, `ipynb` | the file with that extension |
| `html` | `.html` and `.htm` |
| `image` | `.png`, `.jpg`, `.jpeg`, `.webp`, `.tiff`, `.tif`, `.bmp` |

`pages` is the PDF page count from `pdfinfo`, and `0` for every other file or
when `pdfinfo` cannot tell.

`list_formats` gives one entry per extension, and one shared entry for the
images, with `status` (`supported` for PDF, TXT, MD and CSV, `optional` for the
rest), `requires` (`poppler`, `none`, `pandoc` or `tesseract`) and `installed`.

---

## 📏 Limits

| Limit | Value | Constant |
|-------|-------|----------|
| Characters per answer of `read_document`, `convert_to_markdown`, `ocr_document` | 50,000 by default, at most 200,000 | `DefaultMaxChars`, `MaxCharsLimit` |
| Matches shown by `search_document` | 50 by default, at most 500 | `DefaultSearchResults`, `MaxSearchResultsLimit` |
| Images per `extract_images` call | 10 by default, at most 50 | `DefaultMaxImages`, `MaxImagesLimit` |
| Longer side of a returned image | 1600 px | `MaxImageSide` |
| One image | 4 MB | `maxImageBytes` |
| All images of one call | 8 MB | `ImageBudgetBytes` |
| Pages per call | 200 | `maxPagesPerCall` |
| One run of `pdftotext`, `pdfinfo`, `pdfimages` or `pandoc` | 60 s | `toolTimeout` |
| OCR of one page or one image | 120 s | `ocrPageTimeout` |
| All the OCR of one call | 10 min | `ocrCallTimeout` |
| `read_url` download | 50 MB, 60 s | `maxURLFileSize`, `urlDownloadTimeout` |
| Any tool result | 9 MB, replaced with an error above that | `maxToolResultBytes` |
| Text cache | 256 MB | `DOCS_MCP_CACHE_MB`, see [install.md](install.md#-configuration) |

Offsets and `max_chars` count characters, not bytes. A page of text that does
not reach the end of the document ends with a line that names the next
`offset`.

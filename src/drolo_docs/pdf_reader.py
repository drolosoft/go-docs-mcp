"""PDF extraction and caching logic using PyMuPDF."""
from __future__ import annotations
import os
from dataclasses import dataclass, field
from pathlib import Path
import fitz

@dataclass
class PageText:
    page_number: int
    text: str

@dataclass
class DocumentInfo:
    filename: str
    filepath: Path
    title: str
    page_count: int
    file_size: int
    mtime: float
    pages: list[PageText] = field(default_factory=list)

class PDFReader:
    def __init__(self, documents_dir: str | Path | None = None) -> None:
        if documents_dir is None:
            documents_dir = os.environ.get("DROLO_DOCS_DIR", os.path.expanduser("~/.drolo/documents"))
        self.documents_dir = Path(documents_dir).expanduser().resolve()
        self._cache: dict[str, DocumentInfo] = {}

    def _ensure_dir(self) -> None:
        if not self.documents_dir.exists():
            raise FileNotFoundError(f"Documents directory not found: {self.documents_dir}")

    def _pdf_path(self, filename: str) -> Path:
        path = (self.documents_dir / filename).resolve()
        if not path.is_relative_to(self.documents_dir):
            raise ValueError(f"Invalid filename (path traversal attempt): {filename}")
        if not path.exists():
            raise FileNotFoundError(f"Document not found: {filename}")
        if not path.suffix.lower() == ".pdf":
            raise ValueError(f"Not a PDF file: {filename}")
        return path

    def _extract(self, filepath: Path) -> DocumentInfo:
        try:
            doc = fitz.open(str(filepath))
        except Exception as exc:
            raise RuntimeError(f"Failed to open PDF: {filepath.name} ({exc})") from exc
        try:
            title = doc.metadata.get("title", "") if doc.metadata else ""
            if not title or title.strip() == "":
                title = filepath.stem
            pages: list[PageText] = []
            for i in range(doc.page_count):
                pages.append(PageText(page_number=i+1, text=doc.load_page(i).get_text("text")))
            stat = filepath.stat()
            return DocumentInfo(filename=filepath.name, filepath=filepath, title=title,
                page_count=doc.page_count, file_size=stat.st_size, mtime=stat.st_mtime, pages=pages)
        finally:
            doc.close()

    def _get_cached(self, filename: str) -> DocumentInfo:
        filepath = self._pdf_path(filename)
        mtime = filepath.stat().st_mtime
        cached = self._cache.get(filename)
        if cached and cached.mtime == mtime:
            return cached
        info = self._extract(filepath)
        self._cache[filename] = info
        return info

    def list_documents(self) -> list[dict]:
        self._ensure_dir()
        results: list[dict] = []
        for path in sorted(self.documents_dir.glob("*.pdf")):
            try:
                info = self._get_cached(path.name)
                results.append({"filename": info.filename, "title": info.title,
                    "page_count": info.page_count, "file_size": info.file_size,
                    "file_size_human": _human_size(info.file_size)})
            except Exception as exc:
                results.append({"filename": path.name, "error": str(exc)})
        return results

    def read_document(self, filename: str, page: int | None = None) -> str:
        info = self._get_cached(filename)
        if page is not None:
            if page < 1 or page > info.page_count:
                raise ValueError(f"Page {page} out of range (document has {info.page_count} pages)")
            return info.pages[page - 1].text
        return "\n\n".join(f"--- Page {p.page_number} of {info.page_count} ---\n{p.text}" for p in info.pages)

    def search_document(self, filename: str, query: str) -> list[dict]:
        if not query or not query.strip():
            raise ValueError("Search query cannot be empty")
        info = self._get_cached(filename)
        q = query.lower()
        results: list[dict] = []
        for page in info.pages:
            if q in page.text.lower():
                for s in _extract_snippets(page.text, query):
                    results.append({"page_number": page.page_number, "snippet": s})
        return results

    def get_document_summary(self, filename: str) -> str:
        info = self._get_cached(filename)
        n = min(2, info.page_count)
        return "\n\n".join(f"--- Page {info.pages[i].page_number} of {info.page_count} ---\n{info.pages[i].text}" for i in range(n))

def _extract_snippets(text: str, query: str, ctx: int = 150) -> list[str]:
    tl, ql = text.lower(), query.lower()
    snippets, start = [], 0
    while True:
        idx = tl.find(ql, start)
        if idx == -1: break
        s, e = max(0, idx - ctx), min(len(text), idx + len(query) + ctx)
        snippet = text[s:e].strip()
        if s > 0: snippet = "..." + snippet
        if e < len(text): snippet += "..."
        snippets.append(snippet)
        start = idx + len(query)
    return snippets

def _human_size(b: int) -> str:
    for u in ("B", "KB", "MB", "GB"):
        if b < 1024: return f"{b:.1f} {u}"
        b /= 1024
    return f"{b:.1f} TB"

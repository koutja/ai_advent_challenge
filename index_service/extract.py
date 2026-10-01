"""extract.py — загрузка документов (README, статьи, код, PDF) в текст.

Поддерживаемые форматы:
  - markdown/text:  .md, .markdown, .txt
  - код:            .go .py .js .ts .json .yaml .yml .toml .html .css
                    .sh .sql .java .rb .mod
  - PDF:            .pdf (извлечение текста через pypdf)

Сканирование папки рекурсивное; артефакты, бинарники, секреты (.env)
и служебные каталоги пропускаются. Можно указать и один файл, и папку.
"""

from __future__ import annotations

import os
from dataclasses import dataclass

# --- Расширения и правила обхода -------------------------------------------------

TEXT_EXTS = {".md", ".markdown", ".txt"}
CODE_EXTS = {
    ".go", ".py", ".js", ".ts", ".json", ".yaml", ".yml", ".toml",
    ".html", ".css", ".sh", ".sql", ".java", ".rb", ".mod",
}
PDF_EXTS = {".pdf"}

# Каталоги, которые никогда не индексируем (артефакты, вендор, скрытое).
IGNORE_DIRS = {
    ".git", ".venv", "__pycache__", ".pytest_cache", "node_modules",
    "bin", "logs", "results", "index", "screenshots", ".codeassistant",
    ".idea", ".vscode", "dist", "build", "vendor", "demo",
}
# Имена файлов-«секретов» и мусора.
IGNORE_FILES = {".env", ".DS_Store", "go.sum", "go.work.sum"}

# Файлы, которые гарантированно пропускаем (медиа/бинарное/архивы).
BINARY_EXTS = {
    ".png", ".jpg", ".jpeg", ".gif", ".webp", ".webm", ".mp4", ".avi", ".mov",
    ".db", ".sqlite", ".faiss", ".pyc", ".cast", ".ico", ".woff", ".ttf",
    ".zip", ".gz", ".tar", ".pdf",  # placeholder, см. ниже
}
BINARY_EXTS.discard(".pdf")  # PDF мы как раз читаем

MAX_FILE_BYTES = 10 * 1024 * 1024  # защита от гигантских файлов


@dataclass
class Document:
    """Один исходный документ: относительный путь + извлечённый текст."""

    rel_path: str      # путь относительно корня сканирования
    title: str         # имя файла
    format: str        # md | txt | code | pdf
    text: str
    abs_path: str = ""


def classify(path: str) -> str | None:
    """Возвращает формат документа по расширению (или None — не индексеруем)."""
    ext = os.path.splitext(path)[1].lower()
    if ext in TEXT_EXTS:
        return "md" if ext in {".md", ".markdown"} else "txt"
    if ext in CODE_EXTS:
        return "code"
    if ext in PDF_EXTS:
        return "pdf"
    return None


def _is_ignored_dir(name: str) -> bool:
    return name in IGNORE_DIRS or name.endswith(".egg-info")


def _is_ignored_file(name: str) -> bool:
    if name in IGNORE_FILES or name.startswith(".#") or name == ".DS_Store":
        return True
    ext = os.path.splitext(name)[1].lower()
    return ext in BINARY_EXTS


# --- Загрузчики -------------------------------------------------------------------

def _load_text_file(abs_path: str) -> str | None:
    """Читает текстовый файл. Возвращает None, если это бинарь (NUL-байты)."""
    with open(abs_path, "rb") as f:
        raw = f.read(MAX_FILE_BYTES)
    if b"\x00" in raw[:4096]:
        return None  # бинарный файл с «неправильным» расширением
    for enc in ("utf-8", "utf-8-sig", "cp1251", "latin-1"):
        try:
            return raw.decode(enc)
        except UnicodeDecodeError:
            continue
    return raw.decode("utf-8", errors="replace")


def _load_pdf(abs_path: str) -> str:
    """Извлекает текст из PDF постранично (через pypdf)."""
    from pypdf import PdfReader  # ленивый импорт — тяжёлая зависимость

    reader = PdfReader(abs_path)
    pages = []
    for i, page in enumerate(reader.pages):
        text = (page.extract_text() or "").strip()
        if text:
            pages.append(f"[стр. {i + 1}]\n{text}")
    return "\n\n".join(pages)


def load_document(abs_path: str, rel_path: str) -> Document | None:
    """Читает один файл и возвращает Document (None — не удалось)."""
    fmt = classify(abs_path)
    if fmt is None:
        return None
    if fmt == "pdf":
        text = _load_pdf(abs_path)
    else:
        text = _load_text_file(abs_path)
        if text is None:
            return None
    text = text.strip()
    if not text:
        return None
    return Document(
        rel_path=rel_path,
        title=os.path.basename(abs_path),
        format=fmt,
        text=text,
        abs_path=abs_path,
    )


# --- Сканирование ------------------------------------------------------------------

def scan_corpus(docs_path: str) -> tuple[list[Document], str]:
    """Рекурсивно сканирует папку (или один файл) и возвращает список документов.

    Аргумент:
        docs_path — путь к папке с документами ИЛИ к одному файлу.
    Возвращает:
        (список документов, корень сканирования) — относительные пути документов
        считаются от корня, чтобы метаданные source были стабильными.
    """
    docs_path = os.path.abspath(docs_path)
    documents: list[Document] = []

    if os.path.isfile(docs_path):
        root = os.path.dirname(docs_path)
        rel = os.path.basename(docs_path)
        if not _is_ignored_file(rel):
            doc = load_document(docs_path, rel)
            if doc:
                documents.append(doc)
        return documents, root

    root = docs_path
    for dirpath, dirnames, filenames in os.walk(docs_path):
        # Пропускаем только явный список служебных каталогов (.git, .venv и т.п.).
        # Скрытые каталоги-документацию (.agents и пр.) индексируем.
        dirnames[:] = sorted(d for d in dirnames if not _is_ignored_dir(d))
        for name in sorted(filenames):
            if _is_ignored_file(name) or name.startswith("."):
                continue
            abs_path = os.path.join(dirpath, name)
            if os.path.getsize(abs_path) > MAX_FILE_BYTES:
                continue
            rel = os.path.relpath(abs_path, root)
            doc = load_document(abs_path, rel)
            if doc:
                documents.append(doc)

    return documents, root


# --- Вспомогательное ---------------------------------------------------------------

CHARS_PER_PAGE = 2500  # ~1 страница текста ≈ 2500 символов


def total_chars(documents: list[Document]) -> int:
    return sum(len(d.text) for d in documents)


def estimate_pages(documents: list[Document]) -> float:
    """Оценка объёма корпуса в «страницах» (для проверки требования 20–30 стр.)."""
    return total_chars(documents) / CHARS_PER_PAGE
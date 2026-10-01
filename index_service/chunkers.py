"""chunkers.py — две стратегии разбиения документов на чанки.

Стратегии:
  1. fixed     — скользящее окно фиксированного размера (в символах) с
                 перекрытием; границы выравниваются по пробелам/переводам строк.
  2. structure — по структуре документа:
                   * markdown/txt: секции по заголовкам ##/###/####,
                     длинные секции дробятся по параграфам и затем overflow-окном;
                   * код: топ-уровневые объявления (func/type/const в Go,
                     def/class в Python и т.п.), файл целиком — если он мал;
                   * PDF: страницы → абзацы.

Каждый чанк несёт метаданные: source, title, section, chunk_id, format,
диапазон символов в исходнике и стратегию.
"""

from __future__ import annotations

import os
import re
from dataclasses import dataclass, field
from typing import Callable

from extract import Document

# --- Модель чанка ------------------------------------------------------------------

MIN_CHUNK_CHARS = 120  # чанки короче — «мусорные», помечаются в отчёте
SMALL_BLOCK_CHARS = 800  # мелкие соседние блоки кода склеиваются (до max_chunk)


@dataclass
class Chunk:
    """Один чанк: текст + метаданные для поиска и сравнения стратегий."""

    chunk_id: str        # уникальный id: <rel_path без '/' -> '__'>#<NNNN>
    source: str          # относительный путь к файлу-источнику
    title: str           # имя файла
    section: str         # секция/заголовок/сигнатура ("—" если нет структуры)
    format: str          # md | txt | code | pdf
    text: str
    char_start: int      # позиция начала в исходном документе
    char_end: int        # позиция конца в исходном документе
    strategy: str        # fixed | structure
    n_chars: int = 0

    def __post_init__(self) -> None:
        self.n_chars = len(self.text)

    def to_dict(self) -> dict:
        return {
            "chunk_id": self.chunk_id,
            "source": self.source,
            "title": self.title,
            "section": self.section,
            "format": self.format,
            "strategy": self.strategy,
            "char_start": self.char_start,
            "char_end": self.char_end,
            "n_chars": self.n_chars,
        }


def make_chunk_id(doc: Document, index: int) -> str:
    safe = doc.rel_path.replace("/", "__")
    return f"{safe}#{index:04d}"


# --- Общие помощники ----------------------------------------------------------------

def _align_end(text: str, start: int, limit: int, max_chunk: int) -> int:
    """Выравнивает конец окна по границе слова (не дальше max_chunk от start)."""
    end = min(start + limit, len(text))
    if end >= len(text):
        return end
    # ищем последний пробел/перенос в окне (но не ближе, чем на пол-окна)
    window = text[start + max_chunk // 2 : end]
    cut = max(window.rfind(" "), window.rfind("\n"))
    if cut > 0:
        return start + max_chunk // 2 + cut
    return end


def _fixed_split(text: str, size: int, overlap: int) -> list[tuple[int, int, str]]:
    """Скользящее окно: (start, end, подстрока) с выравниванием по словам."""
    out: list[tuple[int, int, str]] = []
    n = len(text)
    if n == 0:
        return out
    start = 0
    while start < n:
        end = _align_end(text, start, size, size)
        out.append((start, end, text[start:end]))
        if end >= n:
            break
        next_start = max(end - overlap, start + 1)
        if next_start >= end:  # защита от зацикливания
            next_start = end
        start = next_start
    return out


def _group_paragraphs(paragraphs: list[str], max_chunk: int) -> list[tuple[int, str]]:
    """Жадная группировка абзацев до max_chunk. Возвращает (offset, text).

    offset — позиция начала группы в «сыром» тексте абзацев (для char_start).
    """
    groups: list[tuple[int, str]] = []
    current = ""
    start_of_current = 0
    running_offset = 0
    for para in paragraphs:
        if not para.strip():
            running_offset += len(para) + 2
            continue
        candidate = (current + "\n\n" + para) if current else para
        if len(candidate) > max_chunk and current:
            groups.append((start_of_current, current))
            current = para
            start_of_current = running_offset
        else:
            if not current:
                start_of_current = running_offset
            current = candidate
        running_offset += len(para) + 2
    if current:
        groups.append((start_of_current, current))
    return groups


def _split_overflow(text: str, max_chunk: int) -> list[tuple[int, int, str]]:
    """Дробление очень длинного куска (абзац/блок) overflow-окном."""
    return _fixed_split(text, max_chunk, min(200, max_chunk // 5))


# --- Стратегия 1: фиксированный размер -------------------------------------------------

def chunk_fixed(doc: Document, size: int = 2000, overlap: int = 200) -> list[Chunk]:
    """Скользящее окно фиксированного размера по всему документу."""
    chunks: list[Chunk] = []
    for i, (s, e, sub) in enumerate(_fixed_split(doc.text, size, overlap)):
        chunks.append(
            Chunk(
                chunk_id=make_chunk_id(doc, i),
                source=doc.rel_path,
                title=doc.title,
                section="—",
                format=doc.format,
                text=sub,
                char_start=s,
                char_end=e,
                strategy="fixed",
            )
        )
    return chunks


# --- Стратегия 2: по структуре --------------------------------------------------------

HEADER_RE = re.compile(r"^(#{1,6})\s+(.+?)\s*$", re.MULTILINE)

# Сигнатуры топ-уровневых объявлений по языкам (по началу строки на колонке 0).
BLOCK_RE: dict[str, re.Pattern] = {
    "go": re.compile(r"^(func|type|const|var)\s", re.MULTILINE),
    "py": re.compile(r"^(def|class)\s", re.MULTILINE),
    "js": re.compile(r"^(export\s+)?(function|class|const|let|var)\s", re.MULTILINE),
    "ts": re.compile(r"^(export\s+)?(function|class|const|let|var|interface|type)\s", re.MULTILINE),
    "sh": re.compile(r"^(function\s+\w+|[\w-]+\(\)\s*\{)", re.MULTILINE),
}


def _lang_of(doc: Document) -> str:
    ext = os.path.splitext(doc.title)[1].lstrip(".")
    return ext if ext in BLOCK_RE else ""


def _split_markdown_sections(doc: Document, max_chunk: int) -> list[Chunk]:
    """Разбивает md/txt по заголовкам; длинные секции дробятся абзацами."""
    text = doc.text
    matches = list(HEADER_RE.finditer(text))
    # уровень заголовка = количество символов '#'
    boundaries = [(m.start(), len(m.group(1)), m.group(2).strip()) for m in matches]

    sections: list[tuple[int, int, str]] = []  # (start, end, section_path)
    prev_start = 0
    header_stack: list[tuple[int, str]] = []  # (level, title) — иерархия заголовков

    def _path() -> str:
        return " → ".join(t for _, t in header_stack) if header_stack else "—"

    for start, level, title in boundaries:
        # секция до текущего заголовка — путь из уже собранной иерархии
        if start > prev_start:
            sections.append((prev_start, start, _path()))
        # обновляем иерархию заголовков текущим заголовком
        while header_stack and header_stack[-1][0] >= level:
            header_stack.pop()
        header_stack.append((level, title))
        prev_start = start
    # хвост документа после последнего заголовка
    if len(text) > prev_start:
        sections.append((prev_start, len(text), _path()))

    chunks: list[Chunk] = []
    idx = 0
    for sec_start, sec_end, sec_path in sections:
        content = text[sec_start:sec_end].strip()
        if not content:
            continue
        if len(content) <= max_chunk:
            chunks.append(
                Chunk(
                    chunk_id=make_chunk_id(doc, idx),
                    source=doc.rel_path, title=doc.title, section=sec_path,
                    format=doc.format, text=content,
                    char_start=sec_start, char_end=sec_end, strategy="structure",
                )
            )
            idx += 1
            continue
        # Длинная секция → абзацы → при необходимости overflow-окно.
        paragraphs = re.split(r"(\n\s*\n)", content)
        parts: list[str] = []
        for p in paragraphs:
            if not p.strip():
                continue
            if len(p) <= max_chunk:
                parts.append(p)
            else:
                for _, _, sub in _split_overflow(p, max_chunk):
                    parts.append(sub)
        for group_off, group_text in _group_paragraphs(parts, max_chunk):
            off = sec_start + group_off
            chunks.append(
                Chunk(
                    chunk_id=make_chunk_id(doc, idx),
                    source=doc.rel_path, title=doc.title, section=sec_path,
                    format=doc.format, text=group_text,
                    char_start=off, char_end=off + len(group_text),
                    strategy="structure",
                )
            )
            idx += 1
    return chunks


def _split_code_blocks(doc: Document, max_chunk: int) -> list[Chunk]:
    """Разбивает код по топ-уровневым объявлениям.

    - преамбула файла (package/imports/комментарии до первого объявления)
      попадает в отдельный чанк — текст не теряется;
    - мелкие соседние блоки склеиваются (меньше SMALL_BLOCK_CHARS и в сумме
      укладываются в max_chunk) — меньше «мусорных» чанков;
    - очень длинный блок дробится overflow-окном.
    """
    lang = _lang_of(doc)
    pattern = BLOCK_RE.get(lang)
    text = doc.text
    chunks: list[Chunk] = []
    idx = 0

    def _emit(s: int, e: int, sig: str) -> None:
        """Добавляет чанк; длинные куски дробит overflow-окном."""
        nonlocal idx
        block = text[s:e].strip()
        if not block:
            return
        if len(block) <= max_chunk:
            chunks.append(
                Chunk(
                    chunk_id=make_chunk_id(doc, idx), source=doc.rel_path,
                    title=doc.title, section=sig[:80], format=doc.format,
                    text=block, char_start=s, char_end=e, strategy="structure",
                )
            )
            idx += 1
            return
        for j, (ss, ee, sub) in enumerate(_split_overflow(block, max_chunk)):
            chunks.append(
                Chunk(
                    chunk_id=make_chunk_id(doc, idx), source=doc.rel_path,
                    title=doc.title, section=sig[:80], format=doc.format,
                    text=sub, char_start=s + ss, char_end=s + ee,
                    strategy="structure",
                )
            )
            idx += 1

    if pattern is None:
        # Язык не распознан — файл цельным чанком (или фиксированное дробление).
        if len(text) <= max_chunk:
            _emit(0, len(text), "—")
        else:
            for s, e, _sub in _fixed_split(text, max_chunk, 200):
                _emit(s, e, "—")
        return chunks

    matches = list(pattern.finditer(text))
    if not matches:
        # Объявлений не найдено — файл как есть (или дробим, если огромный).
        if len(text) <= max_chunk:
            _emit(0, len(text), "—")
        else:
            for s, e, _sub in _fixed_split(text, max_chunk, 200):
                _emit(s, e, "—")
        return chunks

    # Границы: преамбула [0, первое объявление) + блоки между объявлениями.
    bounds = [0] + [m.start() for m in matches] + [len(text)]
    blocks: list[tuple[int, int, str]] = []  # (start, end, сигнатура)
    for i in range(len(bounds) - 1):
        s, e = bounds[i], bounds[i + 1]
        content = text[s:e].strip()
        if not content:
            continue
        first_line = content.splitlines()[0].strip() or "—"
        blocks.append((s, e, first_line[:80]))

    # Склейка мелких соседних блоков.
    merged: list[tuple[int, int, str]] = []
    for s, e, sig in blocks:
        if merged:
            ps, pe, psig = merged[-1]
            prev_len = len(text[ps:pe].strip())
            cur_len = len(text[s:e].strip())
            if prev_len < SMALL_BLOCK_CHARS and cur_len < SMALL_BLOCK_CHARS \
                    and len(text[ps:e].strip()) <= max_chunk:
                merged[-1] = (ps, e, psig)
                continue
        merged.append((s, e, sig))

    for s, e, sig in merged:
        _emit(s, e, sig)
    return chunks


def _split_pdf_pages(doc: Document, max_chunk: int) -> list[Chunk]:
    """PDF: текст уже размечен маркерами [стр. N] → дробим по абзацам."""
    text = doc.text
    paragraphs = re.split(r"(\n\s*\n)", text)
    parts: list[str] = []
    for p in paragraphs:
        if not p.strip():
            continue
        if len(p) <= max_chunk:
            parts.append(p)
        else:
            for _, _, sub in _split_overflow(p, max_chunk):
                parts.append(sub)

    chunks: list[Chunk] = []
    for idx, (off, group) in enumerate(_group_paragraphs(parts, max_chunk)):
        page_marker = re.search(r"\[стр\. \d+\]", group)
        section = page_marker.group(0) if page_marker else "PDF"
        chunks.append(
            Chunk(
                chunk_id=make_chunk_id(doc, idx), source=doc.rel_path,
                title=doc.title, section=section, format=doc.format, text=group,
                char_start=off, char_end=off + len(group), strategy="structure",
            )
        )
    return chunks


def chunk_structure(doc: Document, max_chunk: int = 3000) -> list[Chunk]:
    """Разбиение по структуре документа (см. docstring модуля)."""
    if doc.format in {"md", "txt"}:
        return _split_markdown_sections(doc, max_chunk)
    if doc.format == "code":
        return _split_code_blocks(doc, max_chunk)
    if doc.format == "pdf":
        return _split_pdf_pages(doc, max_chunk)
    # Неизвестный формат — фиксированное окно как fallback.
    return chunk_fixed(doc, max_chunk, min(200, max_chunk // 5))


# --- Общая точка входа -----------------------------------------------------------------

STRATEGIES: dict[str, Callable[[Document, int], list[Chunk]]] = {
    "structure": lambda doc, max_chunk: chunk_structure(doc, max_chunk),
}


def chunk_all(
    documents: list[Document],
    strategy: str,
    chunk_size_chars: int = 2000,
    overlap_chars: int = 200,
    max_chunk_chars: int = 3000,
) -> list[Chunk]:
    """Применяет выбранную стратегию ко всем документам корпуса."""
    if strategy == "fixed":
        out: list[Chunk] = []
        for doc in documents:
            out.extend(chunk_fixed(doc, chunk_size_chars, overlap_chars))
        return out
    if strategy == "structure":
        out = []
        for doc in documents:
            out.extend(chunk_structure(doc, max_chunk_chars))
        return out
    raise ValueError(f"Неизвестная стратегия chunking: {strategy}")
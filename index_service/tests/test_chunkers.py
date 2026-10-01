"""Юнит-тесты чанкеров index_service (pytest).

Запуск: make test  (или .venv/bin/python -m pytest -q)
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from chunkers import chunk_fixed, chunk_structure  # noqa: E402
from extract import Document  # noqa: E402

MD_SAMPLE = """# Заголовок A

Текст первого раздела. Говорим про индексацию.

## Подраздел B

Детали подраздела B.

# Заголовок C

Финальный раздел.
"""

def big_func(name: str, body_lines: int = 100) -> str:
    """Генерирует Go-функцию достаточно большую, чтобы блок не склеился."""
    lines = [f"func {name}(x int) int {{"]
    for i in range(body_lines):
        lines.append(f"\tv{i} := x + {i}")
    lines.append("\treturn v0")
    lines.append("}")
    return "\n".join(lines)


GO_SAMPLE = (
    "// Package main — тестовый пример.\npackage main\n\n"
    + big_func("Alpha")
    + "\n\n"
    + big_func("Beta")
)

GO_SMALL_SAMPLE = """package main

func Alpha() int {
	return 1
}

func Beta(x int) int {
	return x * 2
}

type Point struct {
	X, Y int
}
"""


def make_doc(text: str, fmt: str = "md", rel: str = "docs/sample.md") -> Document:
    return Document(rel_path=rel, title=os.path.basename(rel), format=fmt, text=text)


def test_fixed_covers_all_text():
    doc = make_doc(MD_SAMPLE)
    chunks = chunk_fixed(doc, size=500, overlap=50)
    assert chunks
    covered = set()
    for c in chunks:
        covered.update(range(c.char_start, c.char_end))
    assert len(covered) == len(doc.text), "все символы должны попасть в чанки"


def test_fixed_window_grows():
    doc = make_doc(MD_SAMPLE)
    chunks = chunk_fixed(doc, size=300, overlap=40)
    starts = [c.char_start for c in chunks]
    assert starts == sorted(starts)
    assert len(set(starts)) == len(starts), "старты окон не повторяются"


def test_fixed_max_size():
    doc = make_doc("слово " * 1000)
    chunks = chunk_fixed(doc, size=200, overlap=20)
    assert all(c.n_chars <= 200 for c in chunks)


def test_fixed_empty_doc():
    assert chunk_fixed(make_doc(""), size=200, overlap=20) == []


def test_md_sections_and_paths():
    doc = make_doc(MD_SAMPLE)
    chunks = chunk_structure(doc, max_chunk=3000)
    sections = [c.section for c in chunks]
    assert "Заголовок A" in sections
    assert "Заголовок A → Подраздел B" in sections, (
        f"вложенный путь секции ожидался 'Заголовок A → Подраздел B', есть: {sections}"
    )
    assert "Заголовок C" in sections
    # заголовок входит в текст своего чанка
    first = chunks[0].text
    assert first.startswith("# Заголовок A")


def test_md_long_section_split_by_paragraphs():
    body = "Абзац. " * 80  # ~640 символов
    text = "# Большая\n\n" + body + "\n\n" + body
    chunks = chunk_structure(make_doc(text), max_chunk=400)
    assert len(chunks) >= 2
    assert all(c.section == "Большая" for c in chunks)
    assert all(c.n_chars <= 450 for c in chunks)  # небольшой запас на выравнивание


def test_code_split_by_top_level_blocks():
    doc = make_doc(GO_SAMPLE, fmt="code", rel="sample.go")
    chunks = chunk_structure(doc, max_chunk=3000)
    sigs = [c.section for c in chunks]
    assert any(s.startswith("func Alpha") for s in sigs), sigs
    assert any(s.startswith("func Beta") for s in sigs), sigs
    # преамбула (package/комментарий) тоже попадает в чанки
    assert any("package main" in c.text for c in chunks), [c.text[:40] for c in chunks]


def test_code_preamble_not_lost():
    """Весь текст файла (включая преамбулу) должен быть покрыт чанками."""
    doc = make_doc(GO_SAMPLE, fmt="code", rel="sample.go")
    chunks = chunk_structure(doc, max_chunk=3000)
    covered = set()
    for c in chunks:
        covered.update(range(c.char_start, c.char_end))
    assert len(covered) == len(doc.text), f"потеряно {len(doc.text) - len(covered)} симв."


def test_code_small_blocks_merged():
    doc = make_doc(GO_SMALL_SAMPLE, fmt="code", rel="small.go")
    chunks = chunk_structure(doc, max_chunk=3000)
    # преамбула + 3 мелких блока склеиваются в один чанк
    assert len(chunks) == 1, [c.section for c in chunks]
    assert "func Beta" in chunks[0].text and "type Point" in chunks[0].text


def test_code_no_blocks_is_single_chunk():
    doc = make_doc("просто текст без объявлений", fmt="code", rel="x.txt")
    chunks = chunk_structure(doc, max_chunk=3000)
    assert len(chunks) == 1
    assert chunks[0].text == "просто текст без объявлений"


def test_metadata_fields_present():
    doc = make_doc(MD_SAMPLE)
    for strategy, chunks in (
        ("fixed", chunk_fixed(doc, 500, 50)),
        ("structure", chunk_structure(doc, 3000)),
    ):
        for c in chunks:
            assert c.strategy == strategy
            for field in ("chunk_id", "source", "title", "section", "format"):
                assert getattr(c, field) is not None
            assert c.char_start < c.char_end
            assert c.n_chars == len(c.text)


def test_chunk_ids_unique():
    doc = make_doc(MD_SAMPLE)
    ids = [c.chunk_id for c in chunk_fixed(doc, 200, 20)]
    assert len(ids) == len(set(ids))
    ids2 = [c.chunk_id for c in chunk_structure(doc, 3000)]
    assert len(ids2) == len(set(ids2))
"""evaluate.py — сравнение двух стратегий chunking + retrieval-оценка.

Метрики чанков (по готовому индексу каждой стратегии):
  - число чанков;
  - средний/медианный/мин/макс размер чанка (в символах);
  - доля «мусорных» чанков (короче MIN_CHUNK_CHARS);
  - покрытие текста: доля символов исходников, попавших хотя бы в один чанк;
  - коэффициент дублирования (сумма длин чанков / объём корпуса);
  - время сборки индекса (сек).

Retrieval-оценка (одинакова для обеих стратегий):
  - «золотые» запросы из config.json (query + source + needle);
  - для каждого запроса ищем top-k по индексу; попадание — найденный чанк
    содержит needle (фрагмент ответа);
  - агрегаты: recall@k (доля запросов с попаданием) и MRR.

Итог — markdown-отчёт в results/chunking_comparison.md.
"""

from __future__ import annotations

import logging
import statistics
import time

from chunkers import MIN_CHUNK_CHARS
from embedder import Embedder
from indexer import IndexBundle, search

log = logging.getLogger("day21")


# --- Метрики чанков --------------------------------------------------------------------

def _coverage(bundle: IndexBundle, corpus_by_source: dict[str, int]) -> float:
    """Доля символов корпуса, попавших хотя бы в один чанк (union интервалов)."""
    intervals: dict[str, list[tuple[int, int]]] = {}
    for meta, _text in bundle.chunks:
        src = meta.get("source", "")
        intervals.setdefault(src, []).append((meta.get("char_start", 0), meta.get("char_end", 0)))
    covered = 0
    for src, ivs in intervals.items():
        ivs.sort()
        merged: list[tuple[int, int]] = []
        for s, e in ivs:
            if merged and s <= merged[-1][1]:
                merged[-1] = (merged[-1][0], max(merged[-1][1], e))
            else:
                merged.append((s, e))
        covered += sum(e - s for s, e in merged)
    total = sum(corpus_by_source.values())
    return covered / total if total else 0.0


def chunk_metrics(bundle: IndexBundle, corpus_by_source: dict[str, int]) -> dict:
    """Считает метрики чанков одной стратегии."""
    sizes = [meta.get("n_chars", 0) for meta, _ in bundle.chunks]
    tiny = sum(1 for s in sizes if s < MIN_CHUNK_CHARS)
    return {
        "strategy": bundle.strategy,
        "n_chunks": len(sizes),
        "avg_chars": round(statistics.mean(sizes), 1) if sizes else 0,
        "median_chars": int(statistics.median(sizes)) if sizes else 0,
        "min_chars": min(sizes) if sizes else 0,
        "max_chars": max(sizes) if sizes else 0,
        "tiny_chunks": tiny,
        "tiny_share": round(tiny / len(sizes), 3) if sizes else 0,
        "coverage": round(_coverage(bundle, corpus_by_source), 4),
        "dup_factor": round(sum(sizes) / sum(corpus_by_source.values()), 3)
        if sum(corpus_by_source.values())
        else 0,
        "build_time_s": round(bundle.meta.get("build_time_s", 0), 2),
        "engine": bundle.meta.get("engine", "?"),
    }


# --- Retrieval-оценка --------------------------------------------------------------------

def _find_needle_rank(bundle: IndexBundle, results: list[dict], needle: str) -> int | None:
    """Ранг первого результата, содержащего needle (None — промах)."""
    needle_l = needle.lower()
    for r in results:
        _meta, text = bundle.chunks[r["index"]]
        if needle_l in text.lower():
            return r["rank"]
    return None


def retrieval_eval(
    bundle: IndexBundle,
    embedder: Embedder,
    queries: list[dict],
    top_k: int = 5,
) -> dict:
    """Прогоняет золотые запросы через индекс стратегии."""
    sources = {meta.get("source", "") for meta, _ in bundle.chunks}
    applicable = 0
    hits = 0
    inv_ranks: list[float] = []
    details: list[dict] = []

    for q in queries:
        source, needle = q.get("source", ""), q.get("needle", "")
        # запрос применим, только если его документ-источник есть в корпусе
        if source and not any(s.endswith(source) for s in sources):
            details.append(
                {"query": q.get("query", ""), "source": source,
                 "hit": "нет в корпусе", "rank": None}
            )
            continue
        applicable += 1
        vec = embedder.embed_query(q["query"])
        results = search(bundle, vec, top_k)
        rank = _find_needle_rank(bundle, results, needle)
        if rank is not None:
            hits += 1
            inv_ranks.append(1.0 / rank)
        else:
            inv_ranks.append(0.0)
        details.append(
            {
                "query": q.get("query", ""),
                "source": source,
                "hit": f"hit@{rank}" if rank is not None else "miss",
                "rank": rank,
            }
        )

    return {
        "queries": details,
        "applicable": applicable,
        "recall_at_k": round(hits / applicable, 3) if applicable else 0.0,
        "mrr": round(statistics.mean(inv_ranks), 3) if inv_ranks else 0.0,
    }


# --- Отчёт и вердикт ---------------------------------------------------------------------

def make_verdict(m_fixed: dict, m_struct: dict, e_fixed: dict, e_struct: dict) -> list[str]:
    """Формулирует короткий вердикт сравнения построчно."""
    lines: list[str] = []
    if e_fixed["recall_at_k"] != e_struct["recall_at_k"]:
        winner = "fixed" if e_fixed["recall_at_k"] > e_struct["recall_at_k"] else "structure"
        lines.append(
            f"- По качеству поиска (recall@{5}) выигрывает **{winner}**: "
            f"{e_fixed['recall_at_k']} vs {e_struct['recall_at_k']}."
        )
    elif e_fixed["mrr"] != e_struct["mrr"]:
        winner = "fixed" if e_fixed["mrr"] > e_struct["mrr"] else "structure"
        lines.append(f"- По MRR выигрывает **{winner}**: {e_fixed['mrr']} vs {e_struct['mrr']}.")
    else:
        lines.append("- Качество поиска у стратегий одинаково (recall@5 и MRR совпали).")
    if m_fixed["tiny_share"] != m_struct["tiny_share"]:
        better = "fixed" if m_fixed["tiny_share"] < m_struct["tiny_share"] else "structure"
        lines.append(
            f"- Меньше «мусорных» чанков у **{better}**: "
            f"{m_fixed['tiny_share']:.1%} vs {m_struct['tiny_share']:.1%}."
        )
    lines.append(
        f"- Structure даёт более осмысленные границы (секции/объявления) и меньшее "
        f"дублирование: {m_struct['dup_factor']} vs {m_fixed['dup_factor']}."
    )
    return lines


def write_comparison_report(
    path: str,
    *,
    docs_root: str,
    model_name: str,
    corpus_stats: dict,
    m_fixed: dict,
    m_struct: dict,
    e_fixed: dict,
    e_struct: dict,
) -> str:
    """Пишет markdown-отчёт и возвращает путь к нему."""
    verdict = make_verdict(m_fixed, m_struct, e_fixed, e_struct)
    today = time.strftime("%Y-%m-%d %H:%M:%S")

    rows = [
        ("Число чанков", m_fixed["n_chunks"], m_struct["n_chunks"]),
        ("Средний размер, симв.", m_fixed["avg_chars"], m_struct["avg_chars"]),
        ("Медианный размер, симв.", m_fixed["median_chars"], m_struct["median_chars"]),
        ("Мин. размер, симв.", m_fixed["min_chars"], m_struct["min_chars"]),
        ("Макс. размер, симв.", m_fixed["max_chars"], m_struct["max_chars"]),
        ("«Мусорные» чанки (<120 симв.), шт.", m_fixed["tiny_chunks"], m_struct["tiny_chunks"]),
        ("Покрытие текста", f"{m_fixed['coverage']:.1%}", f"{m_struct['coverage']:.1%}"),
        ("Дублирование (сумма длин/корпус)", m_fixed["dup_factor"], m_struct["dup_factor"]),
        ("Время сборки индекса, с", m_fixed["build_time_s"], m_struct["build_time_s"]),
        ("Движок индекса", m_fixed["engine"], m_struct["engine"]),
    ]
    table = "\n".join(f"| {name} | {a} | {b} |" for name, a, b in rows)

    # у обеих стратегий порядок запросов одинаковый — zip попарно
    qrows = "\n".join(
        f"| {df['query']} | `{df['source']}` | {df['hit']} | {ds['hit']} |"
        for df, ds in zip(e_fixed["queries"], e_struct["queries"])
    )

    report = f"""# Сравнение стратегий chunking — index_service

_Сформировано {today}_

## Корпус

- Папка: `{docs_root}`
- Документов: {corpus_stats['documents']}, всего символов: {corpus_stats['chars']:,}
- Объём: ≈ **{corpus_stats['pages']:.1f} стр.** (2500 символов ≈ 1 стр.)
- Модель эмбеддингов: `{model_name}`

## Метрики чанков

| Метрика | fixed | structure |
|---|---|---|
{table}

> «Дублирование» показывает, во сколько раз суммарная длина чанков больше корпуса
> (у fixed — из-за перекрытия окон; у structure — из-за повторяющихся заголовков
> секций в чанках).

## Retrieval-оценка (recall@5, топ-{5} чанков на запрос)

Применимых золотых запросов: {e_fixed['applicable']}

| Запрос | Источник | fixed | structure |
|---|---|---|---|
{qrows}

Итог: **recall@5** — fixed {e_fixed['recall_at_k']} vs structure {e_struct['recall_at_k']};  
**MRR** — fixed {e_fixed['mrr']} vs structure {e_struct['mrr']}.

## Вывод

{chr(10).join(verdict)}
"""
    with open(path, "w", encoding="utf-8") as f:
        f.write(report)
    log.info("Отчёт сравнения сохранён: %s", path)
    return path
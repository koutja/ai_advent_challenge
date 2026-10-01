#!/usr/bin/env python3
"""main.py — CLI дня «Индексация документов» (локальные эмбеддинги + FAISS).

Подкоманды:
  index   — сканирование документов, chunking (fixed + structure), эмбеддинги,
            сохранение индекса (FAISS/JSON) для каждой стратегии;
  query   — семантический поиск по готовому индексу (топ-k с метаданными);
  compare — сравнение двух стратегий chunking + retrieval-оценка (recall@k, MRR),
            отчёт в results/chunking_comparison.md;
  run-all — полный прогон: index -> compare -> пример поиска.

Примеры:
  python main.py index --docs ../              # весь репозиторий (обе стратегии)
  python main.py index                         # отладка: один корневой README.md
  python main.py query "как подключить пакет llm"
  python main.py compare --docs ../
  python main.py run-all --docs ../
"""

from __future__ import annotations

import argparse
import json
import logging
import os
import sys
import time

DAY_DIR = os.path.dirname(os.path.abspath(__file__))
if DAY_DIR not in sys.path:
    sys.path.insert(0, DAY_DIR)

import config as cfgmod
from config import index_dir, load_config, logs_dir, resolve_docs_dir, results_dir
from embedder import Embedder
from evaluate import (
    chunk_metrics,
    retrieval_eval,
    write_comparison_report,
)
from extract import estimate_pages, scan_corpus, total_chars
from indexer import load_bundle, save_bundle, search
from chunkers import chunk_all

log = logging.getLogger("day21")


# --- Логирование -----------------------------------------------------------------------

def setup_logging(log_dir: str) -> None:
    os.makedirs(log_dir, exist_ok=True)
    log_path = os.path.join(log_dir, "day21.log")
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)-7s %(message)s",
        handlers=[
            logging.FileHandler(log_path, encoding="utf-8"),
            logging.StreamHandler(sys.stdout),
        ],
    )


def die(msg: str, code: int = 1) -> None:
    print(f"Ошибка: {msg}", file=sys.stderr)
    sys.exit(code)


# --- Общая логика индексации -------------------------------------------------------------

def _make_embedder(cfg: dict, model_override: str | None) -> Embedder:
    emb = cfg.get("embed", {})
    return Embedder(
        model_name=model_override or emb.get("model", "intfloat/multilingual-e5-small"),
        batch_size=emb.get("batch_size", 32),
        use_prefixes=emb.get("use_prefixes", True),
    )


def _build_strategy_index(
    strategy: str,
    documents,
    docs_root: str,
    embedder: Embedder,
    cfg: dict,
) -> dict:
    """Чанкинг → эмбеддинги → сохранение индекса одной стратегии."""
    ch = cfg["chunking"]
    params = (
        ch["fixed"]
        if strategy == "fixed"
        else ch.get("structure", {"max_chunk_chars": 3000})
    )
    t0 = time.time()
    chunks = chunk_all(documents, strategy, **params)
    log.info("[%s] чанков: %d", strategy, len(chunks))
    vectors = embedder.embed_passages([c.text for c in chunks])
    dt = time.time() - t0

    meta = {
        "strategy": strategy,
        "model_name": embedder.model_name,
        "chunking_params": params,
        "docs_count": len(documents),
        "corpus_chars": total_chars(documents),
        "est_pages": round(estimate_pages(documents), 1),
        "corpus_root": docs_root,
        "corpus_fingerprint": f"{docs_root}|{len(documents)}|{total_chars(documents)}",
        "build_time_s": round(dt, 2),
    }
    stats = save_bundle(index_dir(cfg), strategy, vectors, chunks, meta)
    stats["build_time_s"] = round(dt, 2)
    stats["est_pages"] = meta["est_pages"]
    return stats


def _ensure_indexes(
    cfg: dict,
    documents,
    docs_root: str,
    embedder: Embedder,
    strategies: tuple[str, ...],
    force: bool = False,
) -> list[dict]:
    # Отпечаток корпуса: если папка/состав/объём изменились — индекс устарел.
    fingerprint = f"{docs_root}|{len(documents)}|{total_chars(documents)}"
    built: list[dict] = []
    for strategy in strategies:
        meta_path = os.path.join(index_dir(cfg), strategy, "meta.json")
        stale = False
        if os.path.exists(meta_path):
            try:
                with open(meta_path, encoding="utf-8") as f:
                    existing = json.load(f)
                stale = existing.get("corpus_fingerprint") != fingerprint
            except Exception:  # noqa: BLE001 — битый meta.json пересоберём
                stale = True
            if stale:
                log.info("[%s] корпус изменился — пересобираю индекс", strategy)
        if force or stale or not os.path.exists(meta_path):
            built.append(_build_strategy_index(strategy, documents, docs_root, embedder, cfg))
        else:
            log.info("[%s] индекс актуален — пропускаю", strategy)
    return built


def _print_corpus_summary(documents, docs_root: str) -> None:
    chars = total_chars(documents)
    pages = estimate_pages(documents)
    print(f"\nКорпус: {docs_root}")
    print(f"Документов: {len(documents)}, символов: {chars:,}, объём: ≈ {pages:.1f} стр.")
    if pages < 20:
        print(
            "⚠  Корпус меньше ~20 страниц — для полного прогона укажите папку "
            "с документами: python main.py index --docs <папка>"
        )
    print()


# --- Подкоманды --------------------------------------------------------------------------

def cmd_index(args: argparse.Namespace, cfg: dict, embedder: Embedder | None = None) -> None:
    docs = resolve_docs_dir(cfg, args.docs)
    documents, docs_root = scan_corpus(docs)
    if not documents:
        die(f"Не найдено документов в {docs}")
    _print_corpus_summary(documents, docs_root)

    strategies = tuple(
        s.strip() for s in getattr(args, "strategies", "fixed,structure").split(",") if s.strip()
    )
    embedder = embedder or _make_embedder(cfg, args.model)
    stats = _ensure_indexes(
        cfg, documents, docs_root, embedder, strategies, force=getattr(args, "force", False)
    )

    print("\nИндексация завершена:")
    for s in stats:
        print(
            f"  [{s['strategy']}] {s['n_chunks']} чанков, "
            f"движок {s['engine']}, {s['build_time_s']} с"
        )
    print(f"\nИндексы сохранены в {index_dir(cfg)}/"
          " (vectors.faiss / vectors.npy / chunks.jsonl / meta.json)")


def cmd_query(args: argparse.Namespace, cfg: dict) -> None:
    text = " ".join(args.query).strip()
    if not text:
        die("Укажите текст запроса: python main.py query \"текст\"")
    top_k = args.top_k or cfg["search"].get("top_k", 5)

    bundle = load_bundle(index_dir(cfg), args.strategy)
    if bundle is None:
        die(
            f"Индекс [{args.strategy}] не найден в {index_dir(cfg)}. "
            "Сначала выполните: python main.py index --docs <папка>"
        )
    embedder = _make_embedder(cfg, None)
    vec = embedder.embed_query(text)
    results = search(bundle, vec, top_k)

    print(f"\nЗапрос: {text}\n")
    if not results:
        print("Ничего не найдено.")
        return
    for r in results:
        print(
            f"{r['rank']}. score={r['score']}  [{r['format']}]  {r['source']}"
        )
        print(f"   секция: {r['section']} | chunk: {r['chunk_id']}")
        print(f"   {r['snippet']}…")


def cmd_compare(args: argparse.Namespace, cfg: dict, embedder: Embedder | None = None) -> None:
    docs = resolve_docs_dir(cfg, args.docs)
    documents, docs_root = scan_corpus(docs)
    if not documents:
        die(f"Не найдено документов в {docs}")
    corpus_by_source = {d.rel_path: len(d.text) for d in documents}

    embedder = embedder or _make_embedder(cfg, args.model)
    _ensure_indexes(
        cfg, documents, docs_root, embedder,
        ("fixed", "structure"), force=getattr(args, "force", False),
    )

    b_fixed = load_bundle(index_dir(cfg), "fixed")
    b_struct = load_bundle(index_dir(cfg), "structure")
    if b_fixed is None or b_struct is None:
        die("Не удалось загрузить оба индекса — выполните index.")

    m_fixed = chunk_metrics(b_fixed, corpus_by_source)
    m_struct = chunk_metrics(b_struct, corpus_by_source)
    top_k = cfg["search"].get("top_k", 5)
    e_fixed = retrieval_eval(b_fixed, embedder, cfg.get("eval_queries", []), top_k)
    e_struct = retrieval_eval(b_struct, embedder, cfg.get("eval_queries", []), top_k)

    report_path = os.path.join(results_dir(cfg), "chunking_comparison.md")
    os.makedirs(results_dir(cfg), exist_ok=True)
    write_comparison_report(
        report_path,
        docs_root=docs_root,
        model_name=embedder.model_name,
        corpus_stats={
            "documents": len(documents),
            "chars": total_chars(documents),
            "pages": estimate_pages(documents),
        },
        m_fixed=m_fixed,
        m_struct=m_struct,
        e_fixed=e_fixed,
        e_struct=e_struct,
    )

    print("\n===== Сравнение стратегий chunking =====")
    print(f"{'Метрика':<28}{'fixed':>10}{'structure':>12}")
    for name, a, b in [
        ("Чанков", m_fixed["n_chunks"], m_struct["n_chunks"]),
        ("Средний размер", m_fixed["avg_chars"], m_struct["avg_chars"]),
        ("«Мусорные» чанки", m_fixed["tiny_chunks"], m_struct["tiny_chunks"]),
        ("Покрытие текста", f"{m_fixed['coverage']:.1%}", f"{m_struct['coverage']:.1%}"),
        ("Дублирование", m_fixed["dup_factor"], m_struct["dup_factor"]),
        ("Время сборки, с", m_fixed["build_time_s"], m_struct["build_time_s"]),
        ("Recall@5", e_fixed["recall_at_k"], e_struct["recall_at_k"]),
        ("MRR", e_fixed["mrr"], e_struct["mrr"]),
    ]:
        print(f"{name:<28}{str(a):>10}{str(b):>12}")
    print(f"\nПолный отчёт: {report_path}")


def cmd_run_all(args: argparse.Namespace, cfg: dict) -> None:
    # модель загружаем один раз на весь прогон (MPS-инициализация медленная)
    embedder = _make_embedder(cfg, args.model)
    print("=== Шаг 1: индексация (обе стратегии) ===")
    cmd_index(args, cfg, embedder)
    print("\n=== Шаг 2: сравнение стратегий ===")
    cmd_compare(args, cfg, embedder)
    print("\n=== Шаг 3: пример семантического поиска ===")
    queries = cfg.get("eval_queries", [])
    demo = queries[0]["query"] if queries else "как подключить пакет llm"
    print(f"\nЗапрос: {demo}\n")
    for strategy in ("fixed", "structure"):
        bundle = load_bundle(index_dir(cfg), strategy)
        if bundle is None:
            continue
        vec = embedder.embed_query(demo)
        results = search(bundle, vec, cfg["search"].get("top_k", 3))
        print(f"--- {strategy} ---")
        for r in results[:3]:
            print(f"  {r['rank']}. score={r['score']} | {r['source']} | {r['section']}")


# --- Точка входа --------------------------------------------------------------------------

def main() -> None:
    cfg = load_config()
    setup_logging(logs_dir(cfg))

    parser = argparse.ArgumentParser(
        prog="index_service",
        description="Локальная индексация документов: chunking, эмбеддинги, FAISS-индекс.",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_index = sub.add_parser("index", help="построить индекс (обе стратегии chunking)")
    p_index.add_argument("--docs", help="папка с документами (или один файл)")
    p_index.add_argument("--strategies", default="fixed,structure", help="через запятую")
    p_index.add_argument("--model", help="переопределить модель эмбеддингов")
    p_index.add_argument("--force", action="store_true", help="пересобрать индексы")
    p_index.set_defaults(func=cmd_index)

    p_query = sub.add_parser("query", help="семантический поиск по индексу")
    p_query.add_argument("query", nargs="*", help="текст запроса")
    p_query.add_argument("--strategy", default="structure", choices=["fixed", "structure"])
    p_query.add_argument("--top-k", type=int, help="сколько результатов показать")
    p_query.set_defaults(func=cmd_query)

    p_compare = sub.add_parser("compare", help="сравнить стратегии и сформировать отчёт")
    p_compare.add_argument("--docs", help="папка с документами (для корпуса)")
    p_compare.add_argument("--model", help="переопределить модель эмбеддингов")
    p_compare.add_argument("--force", action="store_true", help="пересобрать индексы")
    p_compare.set_defaults(func=cmd_compare)

    p_runall = sub.add_parser("run-all", help="полный прогон: index → compare → query")
    p_runall.add_argument("--docs", help="папка с документами")
    p_runall.add_argument("--model", help="переопределить модель эмбеддингов")
    p_runall.add_argument("--force", action="store_true", help="пересобрать индексы")
    p_runall.set_defaults(func=cmd_run_all)

    args = parser.parse_args()
    try:
        args.func(args, cfg)
    except KeyboardInterrupt:
        print("\nПрервано пользователем.", file=sys.stderr)
        sys.exit(130)
    except Exception as exc:  # noqa: BLE001 — понятная ошибка в CLI
        log.exception("Команда %s упала", args.command)
        die(str(exc))


if __name__ == "__main__":
    main()
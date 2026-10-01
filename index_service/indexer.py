"""indexer.py — сборка, сохранение и загрузка векторного индекса.

Формат на диске — для каждой стратегии отдельная папка:

    <index_dir>/<strategy>/
      vectors.faiss   — FAISS-индекс (если установлен faiss-cpu)
      vectors.npy     — матрица векторов float32 (всегда; fallback и экспорт)
      chunks.jsonl    — метаданные и текст каждого чанка (JSON на строку)
      meta.json       — модель, размерность, число чанков, время сборки и пр.

Если faiss-cpu недоступен — поиск работает «в лоб» через numpy по
нормализованным векторам (косинусная близость); формат папки тот же,
поэтому индекс можно достроить позже, поставив faiss.
"""

from __future__ import annotations

import json
import logging
import os
import time
from dataclasses import dataclass

import numpy as np

from chunkers import Chunk

log = logging.getLogger("day21")

# Метаданные чанков без текста — для компактных строк в chunks.jsonl.
_META_FIELDS = ("chunk_id", "source", "title", "section", "format", "strategy",
                "char_start", "char_end", "n_chars")


def _write_chunks_jsonl(chunks: list[Chunk], path: str) -> None:
    with open(path, "w", encoding="utf-8") as f:
        for c in chunks:
            record = {"metadata": c.to_dict(), "text": c.text}
            f.write(json.dumps(record, ensure_ascii=False) + "\n")


def _read_chunks_jsonl(path: str) -> list[tuple[dict, str]]:
    out: list[tuple[dict, str]] = []
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            rec = json.loads(line)
            out.append((rec["metadata"], rec["text"]))
    return out


@dataclass
class IndexBundle:
    """Загруженный индекс одной стратегии: векторы + чанки + метаданные."""

    strategy: str
    vectors: np.ndarray          # (N, dim), нормализованные
    chunks: list[tuple[dict, str]]  # (метаданные, текст) в порядке векторов
    meta: dict
    faiss_index: object | None = None  # faiss.Index или None (numpy-fallback)


# --- Сборка и сохранение --------------------------------------------------------------

def save_bundle(
    index_dir: str,
    strategy: str,
    vectors: np.ndarray,
    chunks: list[Chunk],
    meta: dict,
) -> dict:
    """Сохраняет индекс стратегии на диск и возвращает статистику."""
    target = os.path.join(index_dir, strategy)
    os.makedirs(target, exist_ok=True)

    np.save(os.path.join(target, "vectors.npy"), vectors)
    _write_chunks_jsonl(chunks, os.path.join(target, "chunks.jsonl"))

    faiss_index = None
    engine = "numpy"
    try:
        import faiss  # ленивый импорт: тяжёлая, но опциональная зависимость

        index = faiss.IndexFlatIP(vectors.shape[1])  # косинус на нормализ. векторах
        index.add(vectors)
        faiss.write_index(index, os.path.join(target, "vectors.faiss"))
        faiss_index = index
        engine = "faiss"
    except Exception as exc:  # noqa: BLE001 — faiss не критичен
        log.warning("faiss недоступен (%s), индекс сохранён в numpy-формате", exc)

    meta["engine"] = engine
    meta["created_at"] = time.strftime("%Y-%m-%d %H:%M:%S")
    meta["dim"] = int(vectors.shape[1])
    meta["n_chunks"] = len(chunks)
    with open(os.path.join(target, "meta.json"), "w", encoding="utf-8") as f:
        json.dump(meta, f, ensure_ascii=False, indent=2)

    log.info(
        "Сохранён индекс [%s]: %d чанков, движок %s → %s",
        strategy, len(chunks), engine, target,
    )
    stats = {
        "strategy": strategy,
        "n_chunks": len(chunks),
        "engine": engine,
        "dir": target,
    }
    return stats


# --- Загрузка --------------------------------------------------------------------------

def load_bundle(index_dir: str, strategy: str) -> IndexBundle | None:
    target = os.path.join(index_dir, strategy)
    meta_path = os.path.join(target, "meta.json")
    chunks_path = os.path.join(target, "chunks.jsonl")
    if not (os.path.exists(meta_path) and os.path.exists(chunks_path)):
        return None

    with open(meta_path, encoding="utf-8") as f:
        meta = json.load(f)

    faiss_index = None
    faiss_file = os.path.join(target, "vectors.faiss")
    if os.path.exists(faiss_file):
        try:
            import faiss

            faiss_index = faiss.read_index(faiss_file)
        except Exception as exc:  # noqa: BLE001
            log.warning("Не удалось прочитать FAISS-индекс: %s", exc)

    vectors_path = os.path.join(target, "vectors.npy")
    vectors = np.load(vectors_path)
    chunks = _read_chunks_jsonl(chunks_path)
    log.info("Загружен индекс [%s]: %d чанков (%s)", strategy, len(chunks), meta.get("engine", "?"))
    return IndexBundle(
        strategy=strategy,
        vectors=vectors,
        chunks=chunks,
        meta=meta,
        faiss_index=faiss_index,
    )


def search(bundle: IndexBundle, query_vec: np.ndarray, top_k: int = 5) -> list[dict]:
    """Топ-k ближайших чанков к запросу. Возвращает ранжированный список."""
    top_k = min(top_k, len(bundle.chunks))
    if top_k <= 0:
        return []

    if bundle.faiss_index is not None:
        scores, idxs = bundle.faiss_index.search(np.asarray(query_vec, dtype="float32"), top_k)
        idxs = idxs[0]
        scores = scores[0]
    else:
        scores_all = bundle.vectors @ np.asarray(query_vec, dtype="float32").reshape(-1)
        order = np.argsort(scores_all)[::-1][:top_k]
        idxs = order
        scores = scores_all[idxs]

    results: list[dict] = []
    for rank, (idx, score) in enumerate(zip(idxs, scores), start=1):
        meta, text = bundle.chunks[int(idx)]
        snippet = " ".join(text.split())[:220]
        results.append(
            {
                "rank": rank,
                "index": int(idx),
                "score": round(float(score), 4),
                "chunk_id": meta.get("chunk_id"),
                "source": meta.get("source"),
                "title": meta.get("title"),
                "section": meta.get("section"),
                "format": meta.get("format"),
                "snippet": snippet,
            }
        )
    return results
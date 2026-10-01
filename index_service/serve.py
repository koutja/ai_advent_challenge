#!/usr/bin/env python3
"""serve.py — HTTP-микросервис доступа к индексу документов.

Отдаёт наружу результаты индексации index_service (FAISS + chunks.jsonl),
чтобы другие сервисы (например, агент из agent/feature/rag) могли делать
семантический поиск без знания Python-внутренностей.

Эндпоинты:
  GET  /health            → {"status": "ok", "index_dir": ..., "strategy": ..., "chunks": N}
  POST /search            → body: {"query": "...", "top_k": 5, "strategy": "structure"}
                              ответ: {"engine": "faiss", "strategy": "...",
                                      "results": [{rank, score, chunk_id, source,
                                                   title, section, format, text}]}

Запуск:
  .venv/bin/python serve.py [--port 8734] [--host 127.0.0.1]
                            [--index-dir index] [--strategy structure]

Только стандартная библиотека Python (модель и индекс — из .venv index_service).
"""

from __future__ import annotations

import argparse
import json
import logging
import os
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DAY_DIR = os.path.dirname(os.path.abspath(__file__))
if DAY_DIR not in sys.path:
    sys.path.insert(0, DAY_DIR)

from config import DAY_DIR as _CFG_DIR  # noqa: E402
from embedder import Embedder  # noqa: E402
from indexer import load_bundle, search  # noqa: E402
from config import load_config  # noqa: E402

log = logging.getLogger("index_service")


class IndexService:
    """Обёртка над индексом: ленивая загрузка модели и бандла стратегии."""

    def __init__(self, index_dir: str, strategy: str, cfg: dict):
        self.index_dir = os.path.abspath(index_dir)
        self.strategy = strategy
        emb = cfg.get("embed", {})
        self._embedder: Embedder | None = None
        self._bundle = None
        self._embed_cfg = emb
        self._bundle_loaded_at: float | None = None

    # --- ленивая инициализация ------------------------------------------------

    def _ensure(self) -> None:
        if self._bundle is None:
            t0 = time.time()
            log.info("Загрузка модели эмбеддингов...")
            self._embedder = Embedder(
                model_name=self._embed_cfg.get("model", "intfloat/multilingual-e5-small"),
                batch_size=self._embed_cfg.get("batch_size", 32),
                use_prefixes=self._embed_cfg.get("use_prefixes", True),
            )
            self._bundle = load_bundle(self.index_dir, self.strategy)
            if self._bundle is None:
                raise RuntimeError(
                    f"Индекс [{self.strategy}] не найден в {self.index_dir}. "
                    "Сначала постройте его: python main.py index --docs <папка>"
                )
            self._bundle_loaded_at = time.time()
            log.info(
                "Готово: %d чанков, движок %s, загрузка заняла %.1f с",
                len(self._bundle.chunks), self._bundle.meta.get("engine", "?"),
                time.time() - t0,
            )

    @property
    def chunk_count(self) -> int:
        self._ensure()
        return len(self._bundle.chunks)

    def health(self) -> dict:
        self._ensure()
        return {
            "status": "ok",
            "index_dir": self.index_dir,
            "strategy": self.strategy,
            "chunks": len(self._bundle.chunks),
            "engine": self._bundle.meta.get("engine", "?"),
            "model": self._embedder.model_name,
        }

    # --- поиск ----------------------------------------------------------------

    def search(self, query: str, top_k: int) -> dict:
        self._ensure()
        vec = self._embedder.embed_query(query)
        results = search(self._bundle, vec, top_k)
        out = []
        for r in results:
            # полный текст чанка — из бандла (search возвращает только сниппет)
            text = ""
            if 0 <= r.get("index", -1) < len(self._bundle.chunks):
                text = self._bundle.chunks[r["index"]][1]
            out.append(
                {
                    "rank": r.get("rank"),
                    "score": r.get("score"),
                    "chunk_id": r.get("chunk_id"),
                    "source": r.get("source"),
                    "title": r.get("title"),
                    "section": r.get("section"),
                    "format": r.get("format"),
                    "text": text,
                }
            )
        return {
            "engine": self._bundle.meta.get("engine", "?"),
            "strategy": self.strategy,
            "model": self._embedder.model_name,
            "top_k": top_k,
            "results": out,
        }


def _read_json_body(handler: BaseHTTPRequestHandler) -> dict | None:
    length = int(handler.headers.get("Content-Length") or 0)
    if length <= 0 or length > 1 << 20:  # защитный лимит 1 МБ
        return None
    raw = handler.rfile.read(length)
    try:
        data = json.loads(raw.decode("utf-8"))
    except (json.JSONDecodeError, UnicodeDecodeError):
        return None
    return data if isinstance(data, dict) else None


def _send_json(handler: BaseHTTPRequestHandler, status: int, payload: dict) -> None:
    body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    handler.send_response(status)
    handler.send_header("Content-Type", "application/json; charset=utf-8")
    handler.send_header("Content-Length", str(len(body)))
    handler.end_headers()
    handler.wfile.write(body)


class Handler(BaseHTTPRequestHandler):
    service: IndexService  # подставляется при создании сервера

    def log_message(self, fmt: str, *args) -> None:  # тише логируем
        log.info("%s %s", self.address_string(), fmt % args)

    def do_GET(self) -> None:  # noqa: N802 — имя из http.server
        if self.path.split("?")[0] == "/health":
            try:
                _send_json(self, 200, self.service.health())
            except Exception as exc:  # noqa: BLE001
                _send_json(self, 500, {"status": "error", "message": str(exc)})
            return
        _send_json(self, 404, {"error": f"неизвестный путь: {self.path}"})

    def do_POST(self) -> None:  # noqa: N802 — имя из http.server
        if self.path.split("?")[0] != "/search":
            _send_json(self, 404, {"error": f"неизвестный путь: {self.path}"})
            return
        data = _read_json_body(self)
        if data is None or not data.get("query", "").strip():
            _send_json(self, 400, {"error": "ожидается JSON-объект с полем query"})
            return
        query = str(data["query"]).strip()
        try:
            top_k = max(1, min(int(data.get("top_k") or 5), 20))
        except (TypeError, ValueError):
            top_k = 5
        try:
            _send_json(self, 200, self.service.search(query, top_k))
        except Exception as exc:  # noqa: BLE001
            log.exception("ошибка поиска")
            _send_json(self, 500, {"error": str(exc)})


def main() -> None:
    parser = argparse.ArgumentParser(prog="index_service.serve")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8734)
    parser.add_argument("--index-dir", default=os.path.join(DAY_DIR, "index"))
    parser.add_argument("--strategy", default="structure", choices=["fixed", "structure"])
    args = parser.parse_args()

    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)-7s %(message)s")

    cfg = load_config()
    service = IndexService(args.index_dir, args.strategy, cfg)

    # Проверяем доступность индекса сразу (fail-fast), модель грузим лениво.
    try:
        _ = service.chunk_count
    except Exception as exc:  # noqa: BLE001
        print(f"Ошибка инициализации: {exc}", file=sys.stderr)
        sys.exit(1)

    Handler.service = service
    httpd = ThreadingHTTPServer((args.host, args.port), Handler)
    print(f"index_service: слушаю http://{args.host}:{args.port} "
          f"(index_dir={args.index_dir}, strategy={args.strategy})")
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\nОстановлен.")
        httpd.server_close()


if __name__ == "__main__":
    main()
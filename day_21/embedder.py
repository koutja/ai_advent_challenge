"""embedder.py — локальные эмбеддинги текста через sentence-transformers.

Модель по умолчанию — `intfloat/multilingual-e5-small` (384-мерные векторы,
хорошо работает и с русским, и с английским текстом). Для e5-семейства
используются префиксы `query:` / `passage:` — они добавляются автоматически
(отключаются через config.json, поле embed.use_prefixes, если вы поставите
другую модель, например paraphrase-multilingual-MiniLM-L12-v2).

Модель скачивается один раз с Hugging Face при первом запуске (~470 МБ) и
кэшируется в ~/.cache/huggingface. API-ключ и интернет в рантайме не нужны.
"""

from __future__ import annotations

import logging

import numpy as np

log = logging.getLogger("day21")


class Embedder:
    """Обёртка над SentenceTransformer: батч-эмбеддинги + префиксы e5."""

    def __init__(self, model_name: str, batch_size: int = 32, use_prefixes: bool = True):
        try:
            from sentence_transformers import SentenceTransformer
        except ImportError as exc:  # pragma: no cover
            raise RuntimeError(
                "Не установлен пакет sentence-transformers. "
                "Выполните сначала: make setup"
            ) from exc

        log.info(
            "Загрузка модели эмбеддингов: %s (при первом запуске скачается с Hugging Face)",
            model_name,
        )
        self.model = SentenceTransformer(model_name)
        self.batch_size = batch_size
        self.use_prefixes = use_prefixes
        self.model_name = model_name
        dim_getter = getattr(self.model, "get_embedding_dimension", None)
        if dim_getter is None:  # старые версии sentence-transformers
            dim_getter = self.model.get_sentence_embedding_dimension
        self.dim = dim_getter()
        log.info("Модель загружена, размерность эмбеддингов: %d", self.dim)

    def _with_prefix(self, text: str, query: bool) -> str:
        if not self.use_prefixes:
            return text
        return ("query: " if query else "passage: ") + text

    def embed_passages(self, texts: list[str]) -> np.ndarray:
        """Эмбеддинги чанков-«документов». Векторы нормализуются (косинус)."""
        prefixed = [self._with_prefix(t, query=False) for t in texts]
        vecs = self.model.encode(
            prefixed,
            batch_size=self.batch_size,
            normalize_embeddings=True,
            show_progress_bar=True,
        )
        return np.asarray(vecs, dtype="float32")

    def embed_query(self, text: str) -> np.ndarray:
        """Эмбеддинг пользовательского запроса (форма (1, dim))."""
        vec = self.model.encode(
            [self._with_prefix(text, query=True)],
            normalize_embeddings=True,
            show_progress_bar=False,
        )
        return np.asarray(vec, dtype="float32")
"""config.py — загрузка настроек дня и резолв папки документов.

Настройки лежат в config.json рядом с этим файлом. LLM-подключений здесь нет:
эмбеддинги локальные, API-ключ не требуется (см. AGENTS.md — правило про env
касается подключения к LLM-API; в этом дне его нет).
"""

from __future__ import annotations

import json
import os

DAY_DIR = os.path.dirname(os.path.abspath(__file__))
CONFIG_PATH = os.path.join(DAY_DIR, "config.json")


def load_config(path: str = CONFIG_PATH) -> dict:
    """Читает config.json; при проблемах — понятная ошибка."""
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError as exc:
        raise RuntimeError(f"Не найден конфиг: {path}") from exc


def repo_root() -> str:
    """Корень репозитория (родитель папки дня)."""
    return os.path.dirname(DAY_DIR)


def resolve_docs_dir(cfg: dict, docs_arg: str | None = None) -> str:
    """Определяет, что индексировать.

    Приоритет: аргумент CLI --docs → config.json docs_dir → отладочный дефолт
    (один корневой README.md репозитория).
    """
    if docs_arg:
        return os.path.abspath(docs_arg)
    if cfg.get("docs_dir"):
        return os.path.abspath(os.path.join(DAY_DIR, cfg["docs_dir"]))
    return os.path.join(repo_root(), "README.md")


def index_dir(cfg: dict) -> str:
    return os.path.join(DAY_DIR, cfg["paths"]["index_dir"])


def results_dir(cfg: dict) -> str:
    return os.path.join(DAY_DIR, cfg["paths"]["results_dir"])


def logs_dir(cfg: dict) -> str:
    return os.path.join(DAY_DIR, cfg["paths"]["logs_dir"])
.PHONY: setup index index-debug query compare run-all serve test clean

PY := .venv/bin/python

# ===== Установка окружения =====
# Создаёт изолированный .venv и ставит зависимости из requirements.txt.
setup:
	python3 -m venv .venv
	$(PY) -m pip install --upgrade pip
	$(PY) -m pip install -r requirements.txt

# ===== Режимы запуска =====

# Быстрая отладка: индексация одного корневого README.md репозитория
# (docs_dir в config.json пуст → берётся ../README.md).
index-debug:
	$(PY) main.py index

# Полная индексация документов репозитория (корень проекта),
# обе стратегии chunking: fixed + structure.
index:
	$(PY) main.py index --docs ../

# Поиск по готовому индексу: make query QUERY="как подключить llm"
query:
	$(PY) main.py query "$(QUERY)"

# Сравнение двух стратегий chunking + retrieval-оценка (recall@k, MRR).
compare:
	$(PY) main.py compare --docs ../

# Полный прогон: index (обе стратегии) + compare + пример поиска.
run-all:
	$(PY) main.py run-all --docs ../

# HTTP-микросервис доступа к индексу (для агента: agent/feature/rag).
# Порт можно переопределить: make serve PORT=9000
serve:
	$(PY) serve.py --port $(PORT)

# Юнит-тесты чанкеров.
test:
	$(PY) -m pytest -q

# ===== Очистка артефактов =====
clean:
	rm -rf index results logs .venv __pycache__
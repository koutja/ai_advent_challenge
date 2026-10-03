.PHONY: run run-stats run-compare run-compare-strategies run-compare-memory run-compare-profiles run-check-invariants run-mcp-tools run-mcp-call run-mcp-demo run-mcp-pipeline run-mcp-orchestrate run-rag run-rag-eval run-rag-modes run-rag-citations run-web stop-web build-mcp-server build test reset-history

# ===== Консольный интерфейс (REPL) =====
run:
	go run ./cmd/cli

# ===== Сравнение токенов (Этап 3): короткий/длинный/переполненный диалог =====
run-stats:
	go run ./cmd/cli --stats

# ===== Сравнение сжатия (Этап 4): без сжатия vs со сжатием =====
run-compare:
	go run ./cmd/cli --compare

# ===== Сравнение стратегий контекста: window / facts / branch =====
run-compare-strategies:
	go run ./cmd/cli --compare-strategies

# ===== Сравнение влияния долговременной памяти (long vs без) =====
run-compare-memory:
	go run ./cmd/cli --compare-memory

# ===== Сравнение персонализации (профили terse vs detailed) =====
run-compare-profiles:
	go run ./cmd/cli --compare-profiles

# ===== Проверка инвариантов: конфликт запроса и правила → отказ =====
run-check-invariants:
	go run ./cmd/cli --check-invariants

# ===== MCP: подключиться к серверу и вывести список инструментов =====
# Запускает cmd/mcp-server как отдельный процесс (stdio), устанавливает
# соединение и печатает tools/list. Проверка: соединение + список инструментов.
run-mcp-tools:
	go run ./cmd/cli --mcp-tools

# ===== MCP: вызвать инструмент и напечатать результат =====
# Примеры:
#   make run-mcp-call TOOL=get_task  ARGS='{"task_id":"T-001"}'
#   make run-mcp-call TOOL=create_task ARGS='{"title":"Отчёт","priority":"high"}'
run-mcp-call:
	go run ./cmd/cli --mcp-call $(TOOL) --mcp-args '$(ARGS)'

# ===== MCP: демо планировщика (напоминание + периодический сбор + сводка) =====
run-mcp-demo:
	go run ./cmd/cli --mcp-demo

# ===== MCP: пайплайн search → summarize → save_to_file =====
# Пример: make run-mcp-pipeline QUERY=mcp
run-mcp-pipeline:
	go run ./cmd/cli --mcp-pipeline "$(QUERY)"

# ===== MCP: длинный флоу через несколько серверов (tasks/scheduler/knowledge) =====
# Пример: make run-mcp-orchestrate QUERY=mcp
run-mcp-orchestrate:
	go run ./cmd/cli --mcp-orchestrate "$(QUERY)"

# ===== RAG (День 22): первый RAG-запрос =====
# Требует индекс index_service (cd ../index_service && make index) и, для
# семантического движка, запущенный микросервис (make serve в index_service).
# Пример: make run-rag QUERY="как подключить пакет llm"
run-rag:
	go run ./cmd/cli --rag "$(QUERY)"

# Сравнение качества: без RAG vs с RAG на 10 контрольных вопросах.
# Отчёт: agent/results/rag_comparison.md. Опционально добавить LLM-as-judge:
#   make run-rag-eval JUDGE=1
run-rag-eval:
	go run ./cmd/cli --rag-eval $(if $(JUDGE),--judge,)

# Сравнение режимов пайплайна: base / filter / full (этап 2 + query rewrite).
run-rag-modes:
	go run ./cmd/cli --rag-modes

# Проверка обязательных источников/цитат + режим «не знаю» (День 24).
# Отчёт: agent/results/rag_citations.md. Опционально LLM-as-judge:
#   make run-rag-citations JUDGE=1
run-rag-citations:
	go run ./cmd/cli --rag-citations $(if $(JUDGE),--judge,)

# ===== Сборка автономного MCP-сервера (отдельный процесс) в bin/ =====
build-mcp-server:
	mkdir -p bin
	go build -o bin/mcp-server ./cmd/mcp-server

# ===== Веб-интерфейс (опционально) =====
run-web:
	go run ./cmd/web

# Остановить ранее запущенный web-сервер (по порту из config.json: web_addr).
# `go run` компилирует бинарник во временную папку, поэтому гасим процесс,
# слушающий порт (по умолчанию 8080).
WEB_PORT ?= 8080
stop-web:
	@pid=$$(lsof -ti tcp:$(WEB_PORT) 2>/dev/null); \
	if [ -n "$$pid" ]; then \
		kill $$pid && echo "web остановлен (pid $$pid, порт $(WEB_PORT))"; \
	else \
		echo "web на порту $(WEB_PORT) не запущен"; \
	fi

# ===== Сборка бинарников в agent/bin/ =====
build:
	mkdir -p bin
	go build -o bin/agent_cli ./cmd/cli
	go build -o bin/agent_web ./cmd/web
	go build -o bin/mcp-server ./cmd/mcp-server

# ===== Тесты =====
test:
	go test ./...

# ===== Сброс истории и рабочих БД (удаление файлов SQLite) =====
reset-history:
	rm -f agent_history.db
	rm -f agent_invariants.db

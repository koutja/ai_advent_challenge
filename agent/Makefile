.PHONY: run run-stats run-compare run-compare-strategies run-compare-memory run-compare-profiles run-check-invariants run-mcp-tools run-mcp-call run-web stop-web build-mcp-server build test reset-history

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

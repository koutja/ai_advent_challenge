.PHONY: run run-stats run-compare run-compare-strategies run-compare-memory run-compare-profiles run-web stop-web build test reset-history

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

# ===== Сборка обоих бинарников в agent/bin/ =====
build:
	mkdir -p bin
	go build -o bin/agent_cli ./cmd/cli
	go build -o bin/agent_web ./cmd/web

# ===== Тесты =====
test:
	go test ./...

# ===== Сброс истории (удаление файла SQLite, Этап 2) =====
reset-history:
	rm -f agent_history.db

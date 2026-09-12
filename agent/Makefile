.PHONY: run run-stats run-compare run-web build test reset-history

# ===== Консольный интерфейс (REPL) =====
run:
	go run ./cmd/cli

# ===== Сравнение токенов (Этап 3): короткий/длинный/переполненный диалог =====
run-stats:
	go run ./cmd/cli --stats

# ===== Сравнение сжатия (Этап 4): без сжатия vs со сжатием =====
run-compare:
	go run ./cmd/cli --compare

# ===== Веб-интерфейс (опционально) =====
run-web:
	go run ./cmd/web

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

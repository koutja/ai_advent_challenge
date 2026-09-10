.PHONY: run run-web build test reset-history demo

# ===== Консольный интерфейс (REPL) =====
run:
	go run ./cmd/cli

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

# ===== Демо-прогон: см. skill record-demo =====
demo:
	@echo "запись демо: см. skill record-demo (make new-demo/record)"
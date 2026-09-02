.PHONY: run-free run-limited run-all build

# ===== Запуск отдельных режимов =====
# Запрос по умолчанию берётся из константы defaultPrompt в main.go,
# можно передать свой аргументом: go run . --free "твой запрос"

run-free:
	go run . --free

run-limited:
	go run . --limited

# Запустить оба режима и вывести сравнение
run-all:
	go run . --run-all

# Сборка исполняемого файла
build:
	go build -o llm_client .
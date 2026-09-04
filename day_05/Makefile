.PHONY: run run-all run-cheap run-weak run-medium run-strong build

# ===== Запуск =====
# Без аргументов — прогнать запрос на всех настроенных тирах.
run: run-all

run-all:
	go run . --run-all

# Быстрая отладка на дешёвой модели (weak).
run-cheap:
	go run . --cheap

run-weak:
	go run . --tier weak

run-medium:
	go run . --tier medium

run-strong:
	go run . --tier strong

# Сборка исполняемого файла в day_05/bin/ (папка в .gitignore).
build:
	mkdir -p bin
	go build -o bin/llm_client .
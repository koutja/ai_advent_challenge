.PHONY: run run-simple run-medium run-hard build

# ===== Запуск =====
#   make run          — все 3 запроса разной сложности
#   make run-simple   — только простой (приветствие)
#   make run-medium   — только средний (генерация кода на Go)
#   make run-hard     — только сложный (многошаговое рассуждение)
run:
	go run . all

run-simple:
	go run . simple

run-medium:
	go run . medium

run-hard:
	go run . hard

# Сборка исполняемого файла в day_26/bin/ (папка в .gitignore, бинарник не коммитится).
# Всегда с -o, чтобы не плодить бинарники в корне day_26.
build:
	mkdir -p bin
	go build -o bin/llm_client .

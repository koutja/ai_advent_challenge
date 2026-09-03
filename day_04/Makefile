.PHONY: run run-all run-single build

# ===== Запуск =====
# Запрос по умолчанию: «Главный вопрос жизни, Вселенной и всего такого».
# Прогон всех температур (0, 0.7, 1.2) со сравнением:
run run-all:
	go run . --run-all

# Прогон одного значения температуры, например: make run-single T=1.2
run-single:
	go run . --single $(T)

# Сборка исполняемого файла в day_04/bin/ (папка в .gitignore, бинарник не коммитится)
build:
	mkdir -p bin
	go build -o bin/llm_client .
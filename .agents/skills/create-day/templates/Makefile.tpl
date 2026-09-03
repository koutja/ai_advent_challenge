.PHONY: run build

# ===== Запуск =====
# Добавьте цель для каждого режима, например:
#   run-free:     go run . --free
#   run-limited:  go run . --limited
run:
	go run .

# Сборка исполняемого файла в day_NN/bin/ (папка в .gitignore, бинарник не коммитится).
# Всегда с -o, чтобы не плодить бинарники в корне day_NN.
build:
	mkdir -p bin
	go build -o bin/llm_client .
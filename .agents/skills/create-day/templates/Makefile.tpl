.PHONY: run build

# ===== Запуск =====
# Добавьте цель для каждого режима, например:
#   run-free:     go run . --free
#   run-limited:  go run . --limited
run:
	go run .

# Сборка исполняемого файла (всегда с -o, чтобы не плодить бинарники day_NN/day_NN)
build:
	go build -o llm_client .
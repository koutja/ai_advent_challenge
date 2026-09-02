.PHONY: run-direct run-step run-prompt-gen run-panel run-all build

# ===== Отдельные стратегии рассуждения =====
run-direct:
	go run . --direct

run-step:
	go run . --step

run-prompt-gen:
	go run . --prompt-gen

run-panel:
	go run . --panel

# Запустить все четыре и вывести сравнение
run-all:
	go run . --run-all

build:
	go build -o llm_client .
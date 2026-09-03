.PHONY: build run-direct run-step run-prompt-gen run-panel run-all report clean

BIN := bin/day_03

build:
	go build -o $(BIN) .

# ===== Отдельные стратегии (каждая пишет свой results/<метод>.json) =====
run-direct:
	go run . direct

run-step:
	go run . step

run-prompt-gen:
	go run . prompt-gen

run-panel:
	go run . panel

# ===== Последовательный запуск всех стратегий с интерактивным прогрессом =====
# В терминале обновляется панель статусов по каждому методу, детальный вывод
# пишется в logs/<метод>.log, результаты — в results/<метод>.json.
# Ошибка одного метода не прерывает прогон.
run-all: build
	@mkdir -p results logs
	@./$(BIN) run-all
	@echo "Результаты в results/, логи в logs/. Запустите: make report"

# ===== Отчёт/сравнение по собранным результатам =====
report: build
	./$(BIN) report

clean:
	rm -rf bin results
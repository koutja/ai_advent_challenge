.PHONY: run run-percent run-probability run-probability-easy run-scale run-easy run-sheet build test

# ===== Тренажёр количественной грамотности (numeracy) =====
# REPL-тренажёр: AI генерирует формулировки задач и объяснения, математика
# детерминированная. Категории: percent (проценты/доли), probability
# (условная вероятность), scale (порядки величин), all (все подряд).

# REPL по всем категориям (по умолчанию).
run:
	go run . --category all

# REPL по категории.
run-percent:
	go run . --category percent

run-probability:
	go run . --category probability

# Вероятность без сложных задач (Байес/Монти Холл): классика — кубик, монеты,
# шарики, билеты, дождь, карты.
run-probability-easy:
	go run . --category probability --level easy

run-scale:
	go run . --category scale

# Все категории, только лёгкий уровень.
run-easy:
	go run . --level easy

# Лист задач с ответами и объяснениями: make run-sheet N=3 CATEGORY=all
run-sheet:
	go run . --sheet $(N) --category $(CATEGORY)

build:
	mkdir -p bin && go build -o bin/numeracy .

test:
	go test ./...
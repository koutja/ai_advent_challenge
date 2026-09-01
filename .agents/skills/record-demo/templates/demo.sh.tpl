#!/usr/bin/env bash
# Демонстрация проекта (day_NN).
# Запись: make record DEMO=<путь_к_этому_скрипту>. Функции demo-magic — см. skill record-demo.

# Не используйте `set -e`: одна ошибка команды оборвала бы запись.

# Подключаем demo-magic (демо лежит в <day>/demo/, поэтому корень репо = ../..).
DEMO_MAGIC="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/.agents/skills/record-demo/scripts/demo-magic.sh"
[[ -f "$DEMO_MAGIC" ]] || DEMO_MAGIC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/demo-magic.sh"
source "$DEMO_MAGIC"

DEMO_PROMPT="${DEMO_PROMPT:-\033[32m➜\033[0m \$ }"

clear

p "# Демонстрация проекта day_NN"
wait

pe "cd day_01"

# pe "make run"                       # выполнить команду из Makefile
# pe "go run . \"Ваш вопрос?\""      # аргументный режим (без ожидания ввода)
# pe "ls -la"

wait
clear

p "# Конец демонстрации"
wait
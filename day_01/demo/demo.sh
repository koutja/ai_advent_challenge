#!/usr/bin/env bash
# Демонстрация day_01 — LLM-клиент на Go.
# Фокус: показать запуск программы и её вывод. Запись: make record DEMO=day_01/demo/demo.sh

# Не используйте `set -e`: одна ошибка команды оборвала бы запись.

# Подключаем demo-magic (демо лежит в <day>/demo/, поэтому корень репо = ../..).
DEMO_MAGIC="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/.agents/skills/record-demo/scripts/demo-magic.sh"
[[ -f "$DEMO_MAGIC" ]] || DEMO_MAGIC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/demo-magic.sh"
source "$DEMO_MAGIC"

DEMO_PROMPT="${DEMO_PROMPT:-\033[32m➜\033[0m \$ }"

clear

p "# LLM-клиент: отправляем запрос к API и получаем ответ"
pe "cd day_01"
pe "go run . \"ответь 2 слова: жажду кодить\""
wait

clear

p "# Ответ получен — демонстрация завершена"
wait
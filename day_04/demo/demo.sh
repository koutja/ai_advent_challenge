#!/usr/bin/env bash
# Демонстрация day_04 — один запрос при разной температуре (0 / 0.7 / 1.2)
# и сравнение ответов. Запись видео — см. skill record-demo. Видео пишем только
# по явной просьбе. Не используйте `set -e`: одна ошибка оборвала бы показ.

DEMO_MAGIC="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/.agents/skills/record-demo/scripts/demo-magic.sh"
[[ -f "$DEMO_MAGIC" ]] || DEMO_MAGIC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/demo-magic.sh"
source "$DEMO_MAGIC"

DEMO_PROMPT="${DEMO_PROMPT:-\033[32m➜\033[0m \$ }"

clear

p "# day_04: Температура — один вопрос при temperature 0 / 0.7 / 1.2"
pe "cd day_04"
pe "go run . --run-all \"Главный вопрос жизни, Вселенной и всего такого\""
wait

clear

p "# Ответы получены и сравнены — демонстрация завершена"
wait
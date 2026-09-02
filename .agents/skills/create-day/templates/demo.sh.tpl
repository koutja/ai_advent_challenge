#!/usr/bin/env bash
# Демонстрация __DAY__ — <краткое описание>.
# Запись видео — см. skill record-demo. Видео пишем только по явной просьбе.
# Не используйте `set -e`: одна ошибка команды оборвала бы показ.

DEMO_MAGIC="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/.agents/skills/record-demo/scripts/demo-magic.sh"
[[ -f "$DEMO_MAGIC" ]] || DEMO_MAGIC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/demo-magic.sh"
source "$DEMO_MAGIC"

DEMO_PROMPT="${DEMO_PROMPT:-\033[32m➜\033[0m \$ }"

clear

p "# __DAY__: <краткое описание>"
pe "cd __DAY__"
pe "go run ."
wait

clear

p "# Готово."
wait
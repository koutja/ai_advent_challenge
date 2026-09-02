#!/usr/bin/env bash
# Демонстрация day_02 — контроль формата ответа.
# Фокус: показать три режима (free / limited / run-all) и сравнение.
# Запись видео не запускается (см. skill record-demo).
# Не используйте `set -e`: одна ошибка команды оборвала бы показ.

DEMO_MAGIC="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/.agents/skills/record-demo/scripts/demo-magic.sh"
[[ -f "$DEMO_MAGIC" ]] || DEMO_MAGIC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/demo-magic.sh"
source "$DEMO_MAGIC"

DEMO_PROMPT="${DEMO_PROMPT:-\033[32m➜\033[0m \$ }"

clear

p "# Day 02: один и тот же запрос, разный уровень контроля ответа"
p "# Задача: по подсказке найти песню и вернуть исполнителя, жанр и год."

pe "cd day_02"

p "# 1) БЕЗ ограничений"
pe "go run . --free"
wait

clear

p "# 2) С ограничениями (формат JSON + лимит длины + стоп-маркер)"
pe "go run . --limited"
wait

clear

p "# 3) Полное сравнение"
pe "go run . --run-all"
wait

clear

p "# Готово — сравнение показано."
wait
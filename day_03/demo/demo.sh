#!/usr/bin/env bash
# Демонстрация day_03 — разные способы рассуждения.
# Фокус: показать 4 стратегии и полное сравнение.
# Запись видео не запускается (см. skill record-demo).
# Не используйте `set -e`: одна ошибка команды оборвала бы показ.

DEMO_MAGIC="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/.agents/skills/record-demo/scripts/demo-magic.sh"
[[ -f "$DEMO_MAGIC" ]] || DEMO_MAGIC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/demo-magic.sh"
source "$DEMO_MAGIC"

DEMO_PROMPT="${DEMO_PROMPT:-\033[32m➜\033[0m \$ }"

clear

p "# Day 03: одна задача — четыре способа рассуждения"
p "# Задача: по описанию найти песню и обосновать выбор (song/artist/genre/year)."

pe "cd day_03"

p "# 1) Прямой ответ (без доп. инструкций)"
pe "go run . --direct"
wait

clear

p "# 2) Пошаговое решение"
pe "go run . --step"
wait

clear

p "# 3) Модель сама составляет промпт, затем решает по нему"
pe "go run . --prompt-gen"
wait

clear

p "# 4) Группа экспертов (аналитик, инженер, критик)"
pe "go run . --panel"
wait

clear

p "# 5) Полное сравнение всех стратегий"
pe "go run . --run-all"
wait

clear

p "# Готово — сравнение показано."
wait
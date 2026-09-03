#!/usr/bin/env bash
# Демонстрация day_03 — разные способы рассуждения (параллельно + отчёт).
# Фокус: показать 4 стратегии, панель экспертов и итоговый отчёт.
# Запись видео не запускается (см. skill record-demo).
# Не используйте `set -e`: одна ошибка команды оборвала бы показ.

DEMO_MAGIC="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/.agents/skills/record-demo/scripts/demo-magic.sh"
[[ -f "$DEMO_MAGIC" ]] || DEMO_MAGIC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/demo-magic.sh"
source "$DEMO_MAGIC"

DEMO_PROMPT="${DEMO_PROMPT:-\033[32m➜\033[0m \$ }"

clear

p "# Day 03: одна задача — четыре способа рассуждения (параллельно + отчёт)"
p "# Задача: сколько комбинаций кофе/чая/какао на 5 напитков и 900 ₽ существует?"

pe "cd day_03"

p "# Каждый способ пишет свой JSON в results/"
pe "go run . direct"
wait

clear

p "# Панель экспертов: задача последовательно передаётся между ролями"
p "# Математик → Программист → Ревизор → Итоговый список"
pe "go run . panel"
wait

clear

p "# 1) Прямой ответ"
pe "make run-direct"
wait

clear

p "# 2) Пошаговое решение"
pe "make run-step"
wait

clear

p "# 3) Модель сама составляет промпт, затем решает по нему"
pe "make run-prompt-gen"
wait

clear

p "# 4) Параллельный запуск всех стратегий: строки обновляются на месте"
p "#    Детали пишутся в logs/<метод>.log"
pe "make run-all"
wait

clear

p "# Логи по одному из методов"
pe "cat logs/step.log | head -20"
wait

clear

p "# 5) Отчёт и статистика по собранным результатам"
pe "make report"
wait

clear

p "# Готово — ответы в results/, логи в logs/, сравнение в отчёте."
wait
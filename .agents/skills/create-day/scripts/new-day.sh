#!/usr/bin/env bash
#
# new-day.sh — генератор каркаса нового дня проекта (Go-модуль + Makefile + README + demo).
#
# Использование:
#   new-day.sh <day_dir>
#
#   <day_dir> — папка дня, напр. day_04.
#
# Что делает:
#   - создаёт <day_dir>/ и <day_dir>/demo/
#   - go.mod из шаблона (подстановка имени модуля) + require/replace на llm
#   - копирует Makefile.tpl и main.go.tpl (скелет на llm.Client)
#   - создаёт README.md-заглушку
#   - создаёт demo/demo.sh из шаблона и делает его исполняемым
#
# Важно: скрипт резолвит шаблоны относительно СВОЕГО расположения, поэтому он
# работает одинаково и из .agents/, и из .codeassistant/skills/ после make sync.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TPL_DIR="$SCRIPT_DIR/../templates"

day="${1:-}"
[[ -n "$day" ]] || { echo "Usage: new-day.sh <day_dir>  (например day_04)" >&2; exit 1; }
module="$(basename "$day")"

[[ -e "$day" ]] && { echo "Error: $day уже существует" >&2; exit 1; }
mkdir -p "$day/demo"

for f in go.mod.tpl Makefile.tpl main.go.tpl demo.sh.tpl; do
  [[ -f "$TPL_DIR/$f" ]] || { echo "Template not found: $TPL_DIR/$f" >&2; exit 1; }
done

# go.mod — с подстановкой имени модуля
sed -e "s/{{MODULE}}/$module/g" "$TPL_DIR/go.mod.tpl" > "$day/go.mod"

# статические файлы
cp "$TPL_DIR/Makefile.tpl" "$day/Makefile"
cp "$TPL_DIR/main.go.tpl" "$day/main.go"

# README — короткая заглушка
cat > "$day/README.md" <<EOF
# $(echo "$day" | tr '[:lower:]' '[:upper:]') — <краткое описание>

TODO: опишите задачу дня, запуск и структуру. См. day_01/README.md как пример.
EOF

# demo — с подстановкой имени дня
sed -e "s/__DAY__/$day/g" "$TPL_DIR/demo.sh.tpl" > "$day/demo/demo.sh"
chmod +x "$day/demo/demo.sh"

echo "Created day scaffold: $day"
echo "Next:"
echo "  1) отредактируйте main.go  — логику и флаги режимов;"
echo "  2) Makefile                — цели запуска режимов;"
echo "  3) README.md               — описание;"
echo "  4) demo/demo.sh            — команды демонстрации."
echo "Проверка: cd $day && go build ./... && go vet ./..."
#!/usr/bin/env bash
#
# new-demo.sh — генератор demo-скрипта для конкретного дня проекта.
#
# Использование:
#   new-demo.sh <day_dir> [имя_скрипта]
#
#   <day_dir>          — папка дня, напр. day_01 (или day_02).
#   [имя_скрипта]      — имя demo-скрипта (по умолчанию: demo.sh).
#
# Что делает:
#   - создаёт <day_dir>/demo/
#   - копирует шаблон templates/demo.sh.tpl в <day_dir>/demo/<имя_скрипта>
#   - делает скрипт исполняемым
#   - печатает путь и подсказку, что отредактировать.
#
# Важно: скрипт резолвит шаблон относительно СВОЕГО расположения, поэтому он
# работает одинаково и из .agents/, и из .codeassistant/skills/ после make sync.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE="$SCRIPT_DIR/../templates/demo.sh.tpl"

day="${1:-}"
[[ -n "$day" ]] || { echo "Usage: new-demo.sh <day_dir> [script_name]" >&2; exit 1; }

name="${2:-demo.sh}"

mkdir -p "$day/demo"
dest="$day/demo/$name"

if [[ -f "$TEMPLATE" ]]; then
  cp "$TEMPLATE" "$dest"
else
  echo "Template not found: $TEMPLATE" >&2
  exit 1
fi

chmod +x "$dest"

echo "Created: $dest"
echo "Edit the commands inside, then record with:"
echo "  record-demo.sh $dest"
echo "or from repo root:"
echo "  make record DEMO=$dest"
#!/usr/bin/env bash
#
# record-demo.sh — автоматическая запись демонстрации в видео (MP4/WEBM/GIF).
#
# Пайплайн: demo-magic.sh (печать команд) -> asciinema rec (.cast) -> agg (видео)
#
# Использование:
#   record-demo.sh <demo-script.sh> [outfile]
#
#   <demo-script.sh>  — исполняемый bash-скрипт с командами (source demo-magic.sh).
#   [outfile]         — куда сохранить видео (по умолчанию: рядом с demo-скриптом,
#                       то же имя с расширением .mp4).
#
# Примеры:
#   record-demo.sh day_01/demo/demo.sh
#   record-demo.sh day_01/demo/demo.sh day_01/demo/output.webm
#
# Переменные окружения (опционально):
#   DEMO_PROMPT_TIMEOUT  — таймаут ожидания между шагами demo-magic (по умолч. 1).
#                          Через него скрипт передаёт -w в demo-скрипт.
#   KEEP_CAST=1          — не удалять промежуточный .cast после рендера.
#   AGG_OPTS             — доп. флаги для agg (по умолч. "--theme=solarized-light").
#                          Любое заданное значение переопределяет дефолт целиком.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

demo="${1:-}"
if [[ -z "$demo" ]]; then
  echo "Usage: record-demo.sh <demo-script.sh> [outfile]" >&2
  exit 1
fi
demo="$(cd "$(dirname "$demo")" && pwd)/$(basename "$demo")"
[[ -f "$demo" ]] || { echo "Demo script not found: $demo" >&2; exit 1; }

# --- путь к выходному файлу (по умолчанию тот же basename, но .mp4) -------------
out="${2:-${demo%.*}.mp4}"
mkdir -p "$(dirname "$out")"

# --- временный .cast лежит рядом с demo --------------------------------------
cast="${demo%.*}.cast"

# --- проверка инструментов ----------------------------------------------------
need() { command -v "$1" >/dev/null 2>&1 || {
  echo "ERROR: '$1' not found." >&2
  echo "Install tools:" >&2
  echo "  brew install asciinema agg ffmpeg pv" >&2
  echo "or run:  make install-tools" >&2
  exit 1
}; }
need asciinema
need agg

# --- сборка аргументов для demo-скрипта (неинтерактивный режим) ---------------
# demo-magic парсит свои флаги (-w / -n) из аргументов скрипта, поэтому передаём -w.
timeout="${DEMO_PROMPT_TIMEOUT:-1}"

# --- тема рендера agg (можно переопределить через AGG_OPTS) -------------------
# Важно: default-тема agg может отрисовать «обычный» цвет текста (например
# demo-magic использует BOLD/GREY без явного цвета) чёрным на чёрном фоне —
# получится полностью чёрный ролик. Светлая тема делает тёмный текст видимым.
# Намеренно только --theme без --fps: имя флага зависит от версии agg
# (в старых --fps, в новых --fps-cap). Переопределить можно через AGG_OPTS.
AGG_OPTS="${AGG_OPTS:---theme=solarized-light}"

echo "==> Recording: asciinema rec $cast"
# -c запускает команду внутри псевдо-терминала, чтобы корректно захватывать TTY.
asciinema rec "$cast" --overwrite \
  --cols "${COLS:-100}" --rows "${ROWS:-30}" \
  -c "bash '$demo' -w $timeout"

echo "==> Rendering: agg $cast -> $out"
agg ${AGG_OPTS:-} "$cast" "$out"

if [[ "${KEEP_CAST:-0}" != "1" ]]; then
  rm -f "$cast"
fi

echo "==> Done: $out"
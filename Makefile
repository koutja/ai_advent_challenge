.PHONY: help sync-skills sync install install-tools new-demo record new-day

# ============================================================================
#  record-demo — автоматическая запись демонстрации проекта в MP4
#
#  Единый источник скиллов: .agents/skills/ (универсальный fallback — OpenCode,
#  Claude и др. читают его автоматически). Команда `make sync` создаёт СИМЛИНКИ
#  из этой папки в активную harness-папку — без физических копий, поэтому правки
#  в .agents/skills/ всегда сразу видны во всех harness.
#
#  Переопределяемые переменные:
#    SKILLS_SRC  — откуда линковать (по умолч. .agents/skills)
#    SKILLS_DST  — куда линковать   (по умолч. .codeassistant/skills)
#    HARNESS     — алиас для SKILLS_DST (например claude|roo|cursor)
#    HOME_DIR    — базовый каталог пользователя (по умолч. $(HOME))
# ============================================================================

SKILLS_SRC ?= .agents/skills
HOME_DIR   ?= $(HOME)

# HARNESS позволяет выбрать папку для конкретной harness одной переменной.
# Пример: make sync HARNESS=claude  ->  ~/.claude/skills
ifeq ($(HARNESS),claude)
  SKILLS_DST := $(HOME_DIR)/.claude/skills
else ifeq ($(HARNESS),roo)
  SKILLS_DST := $(HOME_DIR)/.config/roo/skills
else ifeq ($(HARNESS),cursor)
  SKILLS_DST := $(HOME_DIR)/.cursor/skills
else
  # По умолчанию — локально в проекте рядом с .agents/.
  SKILLS_DST ?= .codeassistant/skills
endif

RECORD_SCRIPT = .agents/skills/record-demo/scripts/record-demo.sh
NEWDEMO_SCRIPT = .agents/skills/record-demo/scripts/new-demo.sh
NEWDAY_SCRIPT = .agents/skills/create-day/scripts/new-day.sh

help:
	@echo "Targets:"
	@echo "  make sync [HARNESS=...] [SKILLS_DST=...]  Symlink skills from .agents/ into the active harness folder"
	@echo "  make new-day DAY=day_04                  Scaffold a new day (Go module + llm + demo)"
	@echo "  make new-demo DAY=day_01 [NAME=demo.sh]  Scaffold a demo script for a day"
	@echo "  make record DEMO=day_01/demo/demo.sh [OUT=...]  Record + render demo to video"
	@echo "  make install-tools                        Install asciinema, agg, ffmpeg, pv"

# ---------------------------------------------------------------------------
# Создаёт СИМЛИНКИ на каждый skill из .agents/skills в активную harness-папку.
# Копии не создаются — правки в источнике сразу видны везде. Повторный запуск
# просто пересоздаёт линки (idempotent).
# ---------------------------------------------------------------------------
sync: sync-skills

sync-skills:
	@test -d "$(SKILLS_SRC)" || { echo "Source skills dir not found: $(SKILLS_SRC)"; exit 1; }
	@mkdir -p "$(SKILLS_DST)"
	@# Абсолютный путь источника, чтобы линки работали из любой директории.
	@SRC_ABS="$$(cd "$(SKILLS_SRC)" && pwd)"; \
	for skill in "$$SRC_ABS"/*/; do \
	  [ -e "$$skill" ] || continue; \
	  name="$$(basename "$$skill")"; \
	  rm -rf "$(SKILLS_DST)/$$name"; \
	  ln -sfn "$$skill" "$(SKILLS_DST)/$$name"; \
	done
	@echo "Skills linked: $(SKILLS_SRC) -> $(SKILLS_DST)"
	@ls -1 "$(SKILLS_DST)"

install: sync

# ---------------------------------------------------------------------------
# Установка инструментов для записи видео.
# ---------------------------------------------------------------------------
install-tools:
	@command -v brew >/dev/null 2>&1 || { echo "Homebrew not found. Install it first: https://brew.sh"; exit 1; }
	@echo "Installing asciinema, agg, ffmpeg, pv..."
	brew install asciinema agg ffmpeg pv
	@echo "Done. Tools installed."

# ---------------------------------------------------------------------------
# Генерация каркаса нового дня.
#   make new-day DAY=day_04
# ---------------------------------------------------------------------------
new-day:
	@test -n "$(DAY)" || { echo "Usage: make new-day DAY=day_04"; exit 1; }
	@bash "$(NEWDAY_SCRIPT)" "$(DAY)"

# ---------------------------------------------------------------------------
# Генерация demo-скрипта для дня.
#   make new-demo DAY=day_01
# ---------------------------------------------------------------------------
new-demo:
	@test -n "$(DAY)" || { echo "Usage: make new-demo DAY=day_01 [NAME=demo.sh]"; exit 1; }
	@bash "$(NEWDEMO_SCRIPT)" "$(DAY)" "$(NAME)"

# ---------------------------------------------------------------------------
# Запись видео. DEMO — путь к demo-скрипту, OUT — куда сохранить (по умолч. .mp4).
#   make record DEMO=day_01/demo/demo.sh
#   make record DEMO=day_01/demo/demo.sh OUT=day_01/demo/x.webm
#
# AGG_OPTS — флаги для agg. По умолчанию --theme=solarized-light, чтобы текст
# (у demo-magic это BOLD/GREY без явного цвета) не рендерился чёрным на чёрном
# фоне default-темы agg (иначе видео выглядит чёрным). Переопределяется целиком;
# доступные темы и флаги см. `agg --help` (имя fps-флага зависит от версии:
# --fps или --fps-cap):
#   make record DEMO=day_01/demo/demo.sh AGG_OPTS="--theme=solarized-dark --fps-cap 30"
# ---------------------------------------------------------------------------
AGG_OPTS ?= --theme=solarized-light

record:
	@test -n "$(DEMO)" || { echo "Usage: make record DEMO=day_01/demo/demo.sh [OUT=...]"; exit 1; }
	@AGG_OPTS="$(AGG_OPTS)" bash "$(RECORD_SCRIPT)" "$(DEMO)" "$(OUT)"
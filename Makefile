.PHONY: help sync-skills sync install new-day

# ============================================================================
#  Синхронизация скиллов и создание нового дня
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
#    HOME_DIR    — базовый каталог пользователя (по умолчанию $(HOME))
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

NEWDAY_SCRIPT = .agents/skills/create-day/scripts/new-day.sh

help:
	@echo "Targets:"
	@echo "  make sync [HARNESS=...] [SKILLS_DST=...]  Symlink skills from .agents/ into the active harness folder"
	@echo "  make new-day DAY=day_04                  Scaffold a new day (Go module + llm)"

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
# Генерация каркаса нового дня.
#   make new-day DAY=day_04
# ---------------------------------------------------------------------------
new-day:
	@test -n "$(DAY)" || { echo "Usage: make new-day DAY=day_04"; exit 1; }
	@bash "$(NEWDAY_SCRIPT)" "$(DAY)"
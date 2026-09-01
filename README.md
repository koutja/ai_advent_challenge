# AI Advent Challenge

Репозиторий с ежедневными консольными проектами (Go и др.). Каждый день лежит в
отдельной папке `day_NN/` со своим `Makefile`, кодом и `README.md`.

## 📹 Запись демонстрации работы проекта в видео (MP4)

Скрипт **сам печатает команды** в терминал и записывает прогон в видео — без ручной
записи экрана. Пайплайн: `demo-magic.sh` → `asciinema rec` → `agg` (MP4).

Полная инструкция и функции demo-magic — в skill `record-demo`
([`.agents/skills/record-demo/SKILL.md`](.agents/skills/record-demo/SKILL.md)) и в
[`AGENTS.md`](AGENTS.md). Ниже — только быстрые команды.

### Быстрый старт

```bash
make sync            # симлинк на скиллы из .agents/skills в активную harness (.codeassistant/skills)
make install-tools   # один раз: asciinema, agg, ffmpeg, pv
```

### Записать демо для любого дня

```bash
make new-demo DAY=day_01                     # создать day_01/demo/demo.sh
# (опционально) отредактировать команды в day_01/demo/demo.sh
make record DEMO=day_01/demo/demo.sh         # получить day_01/demo/demo.mp4
open day_01/demo/demo.mp4
```

### Варианты

```bash
make sync HARNESS=claude                      # другая harness: claude | roo | cursor
make record DEMO=day_01/demo/demo.sh OUT=day_01/demo/demo.webm   # другой формат
AGG_OPTS="--theme=tango --fps 30" make record DEMO=day_01/demo/demo.sh  # кастомизация рендера
KEEP_CAST=1 make record DEMO=day_01/demo/demo.sh   # сохранить промежуточный .cast
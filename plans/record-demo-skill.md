# Plan: Skill `record-demo` + root Makefile sync

## Цель
Создать переиспользуемый **skill** в `.agents/skills/record-demo/`, который автоматизирует
запись демонстрации работы консольного проекта (любой `day_NN`) в видео **MP4** —
без ручной записи экрана.

Пайплайн: `demo-magic.sh` (печатает команды с реалистичной задержкой) →
`asciinema rec` (пишет поток терминала в `.cast`) → `agg` (рендерит `.cast` в `.mp4`).

Каталог `.agents/` — универсальный fallback: скиллы из `.agents/skills/` автоматически
читают многие harness (OpenCode, Claude и др.). Плюс root-`Makefile` с командой
`sync-skills`, которая копирует skill из `.agents/` в активную harness-папку
(по умолчанию `.codeassistant/skills/`). Поддерживается override пути через переменные.

## Контекст / допущения
- `.codeassistant` использует формат skill на основе `SKILL.md` (frontmatter `name` + `description`),
  по аналогии со стандартными agent-скиллами. Путь по умолчанию: `.codeassistant/skills/`.
- day_01 (`main.go`) поддерживает два режима: аргумент (`go run . "вопрос"`) и интерактивный
  ввод. Для записи рекомендуется режим с аргументом (проще и детерминированно).
- Инструменты: `asciinema`, `agg`, `ffmpeg` и файл `demo-magic.sh` должны быть установлены.
  Root-Makefile добавляет целевой `install-tools` для установки через `brew`/`go install`.
- Рабочая директория записи: внутри конкретного дня, напр. `day_01/demo/` — чтобы результат
  был привязан к проекту. `.gitignore` каждого дня должен игнорировать `demo/*.cast`.

## Структура новых файлов (создаются в Code mode)

```
ai_advent_challenge/
├── Makefile                                  # корневой (root) Makefile
└── .agents/
    └── skills/
        └── record-demo/
            ├── SKILL.md                      # сам skill (инструкции для агента)
            ├── scripts/
            │   ├── demo-magic.sh             # бандл (демо-магия)
            │   ├── record-demo.sh            # обёртка: asciinema rec + agg
            │   └── new-demo.sh               # генератор demo.sh из шаблона в day_NN
            └── templates/
                └── demo.sh.tpl               # шаблон demo-скрипта
```

## 1. Корневой `Makefile` (в корне проекта)
Переменные с override:
- `SKILLS_SRC  ?= .agents/skills`
- `SKILLS_DST  ?= .codeassistant/skills`   (override под другую harness: `make sync HARNESS=claude` и т.п.)
- `HOME_DIR    ?= $(HOME)`

Цели:
- `sync` / `sync-skills` — `mkdir -p $(SKILLS_DST)` + `rsync -a --delete` из `.agents/skills`
  (копирует **все** скиллы из `.agents/` в активную harness-папку).
- `install` — алиас на `sync`.
- `install-tools` — установка `asciinema`, `ffmpeg` (brew) и `agg` (`go install github.com/asciinema/agg@latest`).
- `record` — подсказка/обёртка: вызов `.agents/skills/record-demo/scripts/record-demo.sh`.

## 2. Skill `.agents/skills/record-demo/SKILL.md`
Frontmatter:
```yaml
---
name: record-demo
description: Автоматически записывает демонстрацию работы консольного проекта (любого day_NN) в видео MP4 через demo-magic.sh + asciinema + agg. Использовать, когда нужно показать готовый прогон кода без ручной записи экрана.
---
```

Тело skill описывает пошаговый процесс:
1. Проверка предпосылок: наличие `asciinema`, `agg`, `ffmpeg`, `demo-magic.sh`.
   Если нет — сообщить пользователю и (опционально) выполнить `make install-tools`.
2. Сгенерировать demo-скрипт в целевом дне: `new-demo.sh <день>`
   (создаёт `day_NN/demo/demo.sh` из шаблона; при желании агент редактирует команды).
3. Наполнить `demo.sh`: подгрузить `demo-magic.sh`, задать `DEMO_PROMPT`,
   указать команды через `pe`, `p`, `wait`, при необходимости `type`.
   Для day_01 рекомендуется аргументный режим: `pe "go run . \"вопрос\""`.
   (Интерактивный ввод — оговорка про `printf ... |` или `type` в подсказках skill.)
4. Запись: `asciinema rec day_NN/demo/output.cast --overwrite -- day_NN/demo/demo.sh`
5. Рендер: `agg day_NN/demo/output.cast day_NN/demo/output.mp4` (можно `.webm`/`.gif`).
6. Гигиена: удалить промежуточный `.cast`, оставить `.mp4`; убедиться, что `.cast`
   не попадает в git (добавить правило в `day_NN/.gitignore`).

Skill содержит встроенные рекомендации по:
- realistic typing (`TYPE_SPEED`, `PROMPT_TIMEOUT`),
- настройке строки приглашения,
- работе с интерактивным вводом,
- очистке терминала (`clear`) между шагами.

## 3. Скрипты
- `scripts/demo-magic.sh` — канонический скрипт демо-магии (содержимое загружается при
  имплементации; либо `curl` в `install-tools`). Функции: `p`, `pe`, `type`, `wait`,
  переменные `DEMO_PROMPT`, `TYPE_SPEED`, `PROMPT_TIMEOUT`.
- `scripts/record-demo.sh` — принимает `DAY` и `OUT` (по умолч. `.mp4`):
  1. `asciinema rec <day>/demo/output.cast --overwrite -- <day>/demo/demo.sh`
  2. `agg <day>/demo/output.cast <out>` и удаление `.cast`.
- `scripts/new-demo.sh` — копирует `templates/demo.sh.tpl` в `day_NN/demo/demo.sh`,
  делает исполняемым, выводит путь для редактирования.

## 4. Шаблон `templates/demo.sh.tpl`
Минимальный работающий каркас demo-скрипта (source demo-magic, DEMO_PROMPT,
`p "..."`, `pe "..."`, `wait`, `clear`) с комментариями.

## 5. Требуемые изменения в существующих файлах
- `day_01/.gitignore` — добавить `demo/*.cast` (и опционально `demo/*.mp4`).
- Возможно добавить раздел в `day_01/README.md` о том, как записать демо (опционально).

## Порядок реализации (Code mode)
1. Создать `.agents/skills/record-demo/SKILL.md`.
2. Добавить `scripts/demo-magic.sh` (получить содержимое), `record-demo.sh`, `new-demo.sh`.
3. Добавить `templates/demo.sh.tpl`.
4. Создать корневой `Makefile` с целями `sync`, `install`, `install-tools`, `record`.
5. Обновить `day_01/.gitignore`.
6. Прогнать `make sync` и проверить, что skill появился в `.codeassistant/skills/`.
7. (Опционально) Записать пробное видео для `day_01`.
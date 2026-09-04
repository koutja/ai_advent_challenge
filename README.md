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
# Рендер по умолчанию уже использует --theme=solarized-light (иначе текст мог
# рендериться чёрным на чёрном -> чёрное видео). Переопределить тему целиком:
make record DEMO=day_01/demo/demo.sh AGG_OPTS="--theme=solarized-dark --fps 30"
KEEP_CAST=1 make record DEMO=day_01/demo/demo.sh   # сохранить промежуточный .cast

## 📂 Дни проекта

| День      | Что делает                                                          | Запуск                                        |
|-----------|---------------------------------------------------------------------|-----------------------------------------------|
| `day_01`  | Минимальный запрос к LLM через API и вывод ответа                    | `cd day_01 && make run`                       |
| `day_02`  | Контроль формата ответа: без ограничений vs формат+длина+стоп        | `cd day_02 && make run-free\|run-limited\|run-all` |
| `day_03`  | Одна задача, 4 способа рассуждения + сравнение                       | `cd day_03 && make run-direct\|run-step\|run-prompt-gen\|run-panel\|run-all` |
| `day_04`  | Влияние temperature (0 / 0.7 / 1.2) на ответ + выводы                | `cd day_04 && make run\|run-all\|run-single` |
| `day_05`  | Один запрос на слабой/средней/сильной модели: время, токены, цена    | `cd day_05 && make run-cheap\|run-weak\|run-medium\|run-strong\|run-all` |

> Общий код подключения к API (чтение `.env`, конфигурация, вызов `/chat/completions`)
> вынесен в переиспользуемый пакет [`llm/`](llm/llm.go:1) и подключается в дни через
> `require aichallenge/llm` + `replace => ../llm`. Каждый день — отдельный Go-модуль
> (только стандартная библиотека).

### `.env` — один файл в корне

Настройки читаются **каскадом** (приоритет сверху вниз):
переменные окружения → локальный `day_NN/.env` → корневой `/.env` → дефолты.

Держите один корневой `/.env` (он в `.gitignore`) — все дни используют его
автоматически, копировать в каждый `day_NN` не нужно. Если конкретному дню нужна
другая модель — просто положите рядом маленький `day_NN/.env`, он перекроет
корневой только для этого дня.
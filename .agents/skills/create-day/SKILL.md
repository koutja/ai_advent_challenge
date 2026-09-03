---
name: create-day
description: "Создание каркаса нового дня day_NN для AI Advent Challenge: Go-модуль с подключением общего пакета llm через replace, Makefile, README и demo.sh. Использовать при старте новой задачи дня, чтобы не дублировать соглашения репозитория."
---

# Skill: create-day

Единый каркас для нового дня репозитория `day_NN`. Позволяет начинать задачу
сразу с готовой, единообразной структуры, не выясняя каждый раз договорённости
проекта (подключение `llm`, `.env`, demo). Это уменьшает число уточняющих
вопросов при старте следующего дня.

## Когда использовать

При каждой новой задаче дня: создать новую папку `day_NN` по образцу `day_01`-`day_03`.

## Соглашения репозитория (важно)

1. **Общий пакет `llm/`** — весь код подключения к API (`LoadEnv`, конфигурация,
   `Chat`) живёт один раз в [`llm/llm.go`](../../../llm/llm.go). Каждый день
   подключает его в `go.mod`:
   ```
   require aichallenge/llm v0.0.0
   replace aichallenge/llm => ../llm
   ```
   В `main.go` используется только `llm.New()` + `client.Chat(...)`.
   Дублировать HTTP/JSON/loadEnv в день **не нужно**.
2. **`.env` читается каскадом** в `llm.New()`:
   окружение → локальный `day_NN/.env` → корневой `/.env` → дефолты.
   Обычно достаточно одного корневого `/.env`; локальный файл — только для
   переопределения настроек конкретного дня.
3. **Флаги режимов**: паттерн `parseArgs` — первый аргумент задаёт режим
   (`--mode`), остальные собираются в пользовательский запрос/задачу. Если день
   сравнивает варианты — добавьте режим `--run-all`.
4. **Makefile**: цели запуска для каждого режима +
   `build: mkdir -p bin && go build -o bin/llm_client .` — бинарники всегда
   собираются в `day_NN/bin/` (папка уже в `.gitignore`), а не в корень дня.
5. **`demo/demo.sh`**: через demo-magic, **без `set -e`**, со ссылкой на skill
   `record-demo`. Видео пишем только по явной просьбе пользователя.
6. **Проверка**: в конце обязателен `go build ./...` и `go vet ./...` в каждом
   затронутом модуле (включая `llm`).

## Быстрый способ

Из корня репозитория:

```bash
make new-day DAY=day_04
```

Это вызывает `scripts/new-day.sh`, который создаёт `day_04/` с `go.mod`,
`Makefile`, `main.go` (скелет на `llm.Client`), `README.md` (заглушку)
и `demo/demo.sh` (шаблон). Затем:

```bash
cd day_04
go build ./... && go vet ./...
```

## Шаг за шагом (вручную)

1. Создать `day_NN/`.
2. `day_NN/go.mod` — из `templates/go.mod.tpl` (module `day_NN` + require/replace).
3. `day_NN/main.go` — логика дня на базе `templates/main.go.tpl`.
4. `day_NN/Makefile` — из `templates/Makefile.tpl`, добавить цели запуска режимов.
5. `day_NN/README.md` — структура папки, переменные, запуск, пример (см. `day_01`).
6. `day_NN/demo/demo.sh` — из `templates/demo.sh.tpl`, вписать команды.
7. Собрать и проверить: `go build`/`go vet` для `day_NN` и `llm`.

## Шаблоны

| Шаблон                | Назначение                                             |
|-----------------------|--------------------------------------------------------|
| `templates/go.mod.tpl`  | go.mod: module `day_NN` + `require`/`replace` на llm  |
| `templates/Makefile.tpl`| Makefile: `run`, `build` (с `-o bin/llm_client`)     |
| `templates/main.go.tpl` | main.go-скелет на `llm.New()` + `client.Chat(...)`     |
| `templates/demo.sh.tpl` | demo-скрипт через demo-magic, без `set -e`            |

## Частые ошибки

- Забыт `replace aichallenge/llm => ../llm` — сборка падает «cannot find package».
- Забыт `.env` — `llm.New()` вернёт ошибку про `LLM_API_KEY`.
- `go build .` без `-o` или без папки `bin/` — появляется лишний бинарник
  в корне `day_NN/` вместо `day_NN/bin/`. В Makefile всегда
  `mkdir -p bin && go build -o bin/llm_client .`.
- `set -e` в demo — одна ошибка обрывает запись (правило AGENTS.md).
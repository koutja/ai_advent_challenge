# agent — недельный агент

Единый модуль (не `day_NN`): гибридный агент, где **ядро** отделено от интерфейса.

## Структура

```
agent/
├── agent.go          # ядро: тип Agent (инкапсулирует LLM-клиент + память + контекст)
├── memory.go         # интерфейс Memory (+ in-memory на Этапе 1, SQLite на Этапе 2)
├── config.go/json    # настройки
├── context.go        # интерфейс ContextStrategy + селектор стратегий
├── window.go         # стратегия SlidingWindow
├── facts.go          # стратегия FactsMemory (извлечение фактов: LLM / хьюристика)
├── branch.go         # стратегия Branching (checkpoint/branch/switch)
├── cmd/
│   ├── cli/main.go   # консольный чат (REPL) + сравнение стратегий
│   └── web/main.go   # опциональный web-чат (HTTP-сервер)
└── web/index.html    # страница web-чата
```

Ядро `agent.Agent` не знает про CLI/web — оба фронтенда вызывают одно и то же
ядро через метод `Say()`.

## Команды

```bash
go run ./cmd/cli                     # консольный чат (REPL)
go run ./cmd/cli --stats             # сравнение токенов: короткий/длинный/переполненный (Этап 3)
go run ./cmd/cli --compare           # сжатие: без сжатия vs со сжатием (Этап 4)
go run ./cmd/cli --compress off      # запуск без сжатия истории
go run ./cmd/cli --compare-strategies # прогон сценария «собираем ТЗ» на всех 3 стратегиях
go run ./cmd/web                     # web-чат: http://127.0.0.1:8080
go run ./cmd/web --strategy branch   # web-чат со стратегией по умолчанию (window|facts|branch)
make build                           # собрать bin/agent_cli и bin/agent_web
make test                            # прогнать тесты
make reset-history                   # удалить agent_history.db (сброс истории)
```

Веб-интерфейс ([`web/index.html`](web/index.html)) показывает селектор стратегии
(`window | facts | branch`, переключение через `POST /strategy`). Прямо в ленте
чата между сообщениями выводятся **живые логи стратегии** (`Reply.Events` от
`Agent.Say()`): что делает стратегия в этом ходе и что произойдёт дальше —
например, `window` сообщает «отбрасываю N старых сообщений» и «скоро история
превысит окно», `facts` — «извлекаю факты», «достигнут лимит ключей». Виды событий
выделяются иконками: 🔎 log, ⚠ warn, ⏳ predict («скоро: …»). Ошибки (чат,
переключение стратегии) показываются всплывающими **toast**-уведомлениями.
CLI-сравнение стратегий (`--compare-strategies`) оставлено как отладочный инструмент.

Команды REPL:

| Команда | Описание |
|---------|----------|
| `/strategy` | показать текущую стратегию |
| `/strategy window\|facts\|branch` | переключить стратегию |
| `/checkpoint <имя>` | сохранить контрольную точку (Branching) |
| `/branch <имя>` | создать ветку-копию и переключиться (Branching) |
| `/switch <имя>` | переключиться между ветками (Branching) |
| `/facts` | показать текущие факты (Facts) |
| `/reset` | очистить историю |
| `/compress` | переключить legacy-сжатие |
| `/exit` `/quit` `/q` | выйти |

## Стратегии управления контекстом

Подробный дизайн — в [`plans/context-management-plan.md`](plans/context-management-plan.md),
чек-лист — в [`plans/context-strategies-checklist.md`](plans/context-strategies-checklist.md).

Агент поддерживает переключаемые стратегии контекста **без сжатия-сводки**
(интерфейс `ContextStrategy`). Память всегда хранит полную историю; каждая
стратегия решает, какие сообщения уходят в LLM и как вести собственное состояние.

| Стратегия | Принцип | Токены | Точность |
|-----------|---------|--------|----------|
| **window** (SlidingWindow) | последние N сообщений | минимум | ранние детали теряются при маленьком N |
| **facts** (FactsMemory) | system-блок «Факты диалога» + последние N сообщений | баланс | держит цель/ограничения/дедлайн, тратит на извлечение |
| **branch** (Branching) | ветки диалога в RAM стратегии, контрольные точки | максимум | максимум стабильности в ветке, гибкий UX |

Выбор по умолчанию задаётся в [`config.json`](config.json) полем `context_strategy`
(пустое значение → legacy-сжатие `compress` или off). Параметры стратегий:
`window_size`, `facts_keep_last`, `facts_key_max`, `facts_extractor`
(`llm` — точное извлечение через отдельный вызов, `heuristic` — регулярки, без LLM).

Пример сравнения всех трёх стратегий на одном сценарии:

```bash
go run ./cmd/cli --compare-strategies
```

После прогона создаётся отчёт [`plans/compare-context.md`](plans/compare-context.md)
со сводной таблицей по качеству, стабильности, расходу токенов и числу ходов.

## Настройка (куда идут запросы и ключи)

Подключение к LLM задаётся **только** через переменные окружения (стандартные
имена, правило 6 в [`../AGENTS.md`](../AGENTS.md)): файл `.env` в папке `agent/`
загружается каскадом из текущей папки и корня репозитория через общий пакет
[`../llm`](../llm):

- `LLM_API_KEY` — API-ключ (обязательно);
- `LLM_BASE_URL` — базовый URL эндпоинта (если провайдер не дефолтный);
- `LLM_MODEL` — модель (если нужна конкретная).

В [`config.json`](config.json) полей для подключения нет — только логика агента.

Пример минимального `.env` в папке `agent/`:

```
LLM_API_KEY=sk-...
# LLM_BASE_URL=https://api.openai.com/v1
# LLM_MODEL=gpt-4o-mini
```

Как загружаются `.env` и каковы дефолты URL/модели — см. общий пакет [`../llm`](../llm).

## Этапы

1. **Первый агент** — ядро + CLI + web, память в RAM.
2. **Сохранение контекста** — история в SQLite через интерфейс `Memory`.
3. **Работа с токенами** — оценка токенов, стоимость из `llm/models.json`, режим `--stats`.
4. **Сжатие истории** — `ContextManager`: summary + последние N сообщений.
5. **Стратегии контекста** — `ContextStrategy`: SlidingWindow / Facts / Branching
   без сжатия-сводки; сравнение на сценарии через `--compare-strategies`.

Подробнее — [`../plans/agent-week-plan.md`](../plans/agent-week-plan.md) и
[`plans/context-management-plan.md`](plans/context-management-plan.md).
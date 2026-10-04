# agent — недельный агент

Единый модуль (не `day_NN`): гибридный агент, где **ядро** отделено от интерфейса.

## Структура

```
agent/
├── agent.go          # ядро: тип Agent (LLM-клиент + многослойная память + контекст)
├── config.go/json    # настройки
├── tokens.go         # оценка токенов и стоимости
├── compare.go        # сравнение стратегий контекста
├── feature/
│   ├── dialog/       # short-term: история текущего диалога (Memory, InMemory, SQLiteMemory)
│   ├── context/      # стратегии контекста (SlidingWindow / FactsMemory / Branching / ContextManager)
│   ├── memory/       # многослойная память: WorkingStore, LongStore, LayeredMemory, роутинг, extract
│   ├── profile/      # персонализация: UserProfile, ProfileStore (SQLite), SystemBlock()
│   └── mcp/          # MCP: stdio-клиент (Connect/ListTools) + сервер демо-инструментов (go-sdk)
├── cmd/
│   ├── cli/main.go       # консольный чат (REPL) + сравнения
│   ├── web/main.go       # web-чат (HTTP-сервер) — обязательный этап после CLI
│   └── mcp-server/main.go # автономный MCP-сервер (отдельный процесс, stdio)
└── web/index.html    # страница web-чата
```

Ядро `agent.Agent` не знает про CLI/web — оба фронтенда вызывают одно и то же
ядро через метод `Say()`.

> **Правило реализации:** web — обязательный шаг, а не опция. Любая CLI-возможность
> доводится до web-интерфейса на том же ядре: сначала CLI, затем обязательно web.

## Команды

```bash
go run ./cmd/cli                     # консольный чат (REPL)
go run ./cmd/cli --stats             # сравнение токенов: короткий/длинный/переполненный (Этап 3)
go run ./cmd/cli --compare           # сжатие: без сжатия vs со сжатием (Этап 4)
go run ./cmd/cli --compress off      # запуск без сжатия истории
go run ./cmd/cli --compare-strategies # прогон сценария «собираем ТЗ» на всех 3 стратегиях
go run ./cmd/cli --compare-memory    # влияние долговременной памяти на ответы (long vs без)
go run ./cmd/cli --compare-profiles  # влияние персонализации (профили terse vs detailed)
go run ./cmd/cli --check-invariants  # демо: конфликт запроса с инвариантом → отказ
go run ./cmd/cli --mcp-tools         # MCP: подключиться к серверу и вывести список инструментов
go run ./cmd/mcp-server              # MCP: запустить сервер как отдельный процесс (stdio)
go run ./cmd/cli --rag "вопрос"      # RAG-ответ: индекс index_service + LLM с источниками
go run ./cmd/cli --rag-eval          # сравнение без RAG vs с RAG на 10 контрольных вопросах
go run ./cmd/cli --rag-eval --judge  # то же + LLM-as-judge
go run ./cmd/web                     # web-чат: http://127.0.0.1:8080
go run ./cmd/web --strategy branch   # web-чат со стратегией по умолчанию (window|facts|branch)
make stop-web                        # остановить ранее запущенный go run ./cmd/web (по порту 8080)
make build                           # собрать bin/agent_cli, bin/agent_web и bin/mcp-server
make build-mcp-server                # собрать только bin/mcp-server
make test                            # прогнать тесты
make reset-history                   # удалить agent_history.db (сброс истории)
```

## MCP (Model Context Protocol)

Подключение MCP реализовано на официальном Go SDK
[`github.com/modelcontextprotocol/go-sdk`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk).
Расклад «сервер отдельным процессом, клиент подключается по stdio» близок к
реальному деплою: [`cmd/mcp-server`](cmd/mcp-server) — автономный сервер
демо-инструментов, а [`feature/mcp`](feature/mcp) — stdio-клиент, который
запускает его как дочерний процесс. Клиент умеет не только перечислять
инструменты (`ListTools`), но и **вызывать** их (`CallTool`).

### Инструменты

Инструменты `get_task` и `create_task` оборачивают **встроенный mock HTTP-API**
([`feature/mcp/api.go`](feature/mcp/api.go)): in-memory хранилище задач с
эндпоинтами `GET /tasks/{id}` и `POST /tasks`. Хендлеры инструмента обращаются
к нему обычным HTTP-клиентом — как к внешнему сервису, но без сети и ключей.

| Инструмент      | Входные параметры                                                        | Результат |
|-----------------|--------------------------------------------------------------------------|-----------|
| `get_task`      | `task_id` (обязательно)                                                  | запись задачи (JSON) |
| `create_task`   | `title` (обязательно), `priority?` (low\|medium\|high), `assignee?`      | созданная задача (JSON) |
| `get_time`      | —                                                                        | текущее время (RFC3339) |
| `echo`          | `message`                                                                | то же сообщение |
| `reminder_add`  | `text` (обязательно), `in_minutes` (0 — сразу)                           | `{id, due_at}` |
| `reminders_status` | —                                                                    | сводка total/pending/fired |
| `collect_start` | `metric`, `interval_seconds`, `iterations` (0 — бесконечно)              | `{job_id, next_run_at}` |
| `collect_status` | `metric?`                                                               | агрегат count/min/max/avg/latest |
| `summary_start` | `interval_seconds`                                                       | `{job_id}` периодических снимков |
| `get_summary`   | —                                                                        | общая сводка + число снимков |
| `search`        | `query` (обязательно), `limit?`                                          | `{query, total, docs[]}` |
| `summarize`     | `text`, `max_words?`                                                     | `{summary, words, sources}` |
| `save_to_file`  | `filename`, `content`                                                    | `{path, bytes}` → `results/pipeline/` |
| `generate_document` | `topic`                                                              | `{id, title, snippet, source: llm\|fallback}` |

Описание входных параметров попадает в JSON-схему инструмента автоматически
(из типов-структур и `jsonschema`-тегов), т.е. регистрация инструмента +
описание параметров + возврат структурированного результата реализованы.

### Планировщик и фоновые задачи

Инструменты `reminder_add`/`reminders_status` (отложенные напоминания),
`collect_start`/`collect_status` (периодический сбор данных) и
`summary_start`/`get_summary` (регулярная сводка) реализуют планировщик:

- **Расписание:** внутри процесса `mcp-server` крутится лёгкий тикер (1 с);
  первое срабатывание задачи — сразу, дальше через `interval_seconds`.
- **Хранение:** JSON-файл с атомарной записью (tmp+rename). Путь — из env
  `MCP_DATA_FILE`, по умолчанию `results/mcp_scheduler_data.json`
  (папка `results/` игнорируется git). Напоминания, точки данных и снимки
  сводки переживают рестарты.
- **24/7:** при старте сервер «догоняет» просроченные задачи (catch-up), а
  лимит `maxPerTick` защищает от долгого навёрстывания после простоя.
- **Агрегация:** `collect_status` и `get_summary` возвращают
  count/min/max/avg/latest.

Демо «агент работает по расписанию» (~5 с): ставит напоминание, запускает
периодический сбор данных и печатает агрегированную сводку.

    make run-mcp-demo

Или вручную:

    go run ./cmd/cli --mcp-call reminder_add --mcp-args '{"text":"Собрать стенд","in_minutes":0}'
    go run ./cmd/cli --mcp-call collect_start --mcp-args '{"metric":"cpu","interval_seconds":1,"iterations":3}'
    go run ./cmd/cli --mcp-call get_summary

### Композиция инструментов

Три инструмента `search`, `summarize` и `save_to_file` образуют пайплайн, где
вывод каждого шага передаётся на вход следующего:

1. `search <query>` — находит документы в локальном корпусе (детерминированно);
2. `summarize <text>` — строит сводку (первые предложения до `max_words`);
3. `save_to_file <filename, content>` — сохраняет результат в `results/pipeline/`
   (env `MCP_OUTPUT_DIR`), имя файла санитизируется от путей.

Автоматическое выполнение цепочки — флагом `--mcp-pipeline` или REPL-командой
`/mcp-pipeline <запрос>`:

    make run-mcp-pipeline QUERY=mcp
    # или вручную:
    go run ./cmd/cli --mcp-pipeline mcp

Пример вывода (каждый шаг виден, данные перетекают между инструментами):

    1) search("mcp"): 3 документов
       • [doc-001] Что такое MCP — Model Context Protocol — открытый стандарт…
       • [doc-002] Go SDK для MCP — Официальный Go SDK позволяет писать…
       • [doc-003] Планировщик в агенте — Планировщик выполняет…
    2) summarize: 30 слов, 3 источников
       Что такое MCP. Model Context Protocol — открытый стандарт для подключения…
    3) save_to_file: results/pipeline/pipeline_mcp.md (130 байт)

Проверка цепочки автоматизирована в `TestPipelineComposition`: сводка строится
по найденным документам, а сохранённый файл в точности равен сводке.

### Дозаполнение корпуса через LLM

Если `search` не находит документы по запросу, пайплайн **спрашивает
пользователя**:

```
   Документ не найден. Сформировать его через LLM и добавить в корпус? [y/N]
```

- `y` → инструмент `generate_document {topic}` вызывает общий пакет [`llm`](../llm)
  (использует те же `LLM_API_KEY` / `LLM_BASE_URL` / `LLM_MODEL` из `.env`, что и
  агент) и добавляет результат в персистентный корпус
  `results/pipeline/corpus.json` (env `MCP_CORPUS_FILE`). Затем цепочка
  продолжается: сводка по новому документу → `save_to_file`.
- `n` → сохраняется короткая заметка, пайплайн не падает.
- Если LLM недоступен (нет ключа/сети), `generate_document` возвращает
  детерминированный fallback (`source: "fallback"`) — цепочка всё равно
  завершается.

Дальнейшие поиски этой темы уже находят документ (корпус персистентный).
Проверка: `TestGenerateDocumentFallback`, `TestGenerateDocumentTool`,
`TestCorpusStoreRoundTrip`.

### Оркестрация нескольких MCP-серверов

Один бинарник [`cmd/mcp-server`](cmd/mcp-server) умеет обслуживать разные
«домены» инструментов через флаг `--server`:

| Домен       | Инструменты |
|-------------|-------------|
| `tasks`     | `get_task`, `create_task` |
| `scheduler` | `reminder_add`, `reminders_status`, `collect_start`, `collect_status`, `summary_start`, `get_summary` |
| `knowledge` | `search`, `summarize`, `save_to_file`, `generate_document` |
| `all`       | демо (`get_time`, `echo`) + все домены (по умолчанию) |

Оркестратор [`feature/mcp/registry.go`](feature/mcp/registry.go) подключается к
нескольким серверам, по `tools/list` строит таблицу «инструмент → сервер»
(дубли имён — ошибка маршрутизации) и направляет вызовы нужному серверу.
Список серверов — в `config.json` (`mcp_servers`), по умолчанию три домена.

Длинный флоу через разные серверы — `--mcp-orchestrate <тема>` (или REPL
`/mcp-orchestrate <тема>`):

```bash
make run-mcp-orchestrate QUERY=mcp
```

Флоу: `search` (knowledge) → `summarize` (knowledge) → `create_task` (tasks) →
`reminder_add`/`collect_start`/`get_summary` (scheduler) → `save_to_file`
(knowledge). Каждый шаг печатает выбранный сервер — видна корректность
маршрутизации и порядка. Проверка: `TestRegistryRouting`,
`TestRegistryDuplicateTools`, `TestOrchestrateFlow`.

Подробное объяснение «как это работает и как пользоваться» — в
[`docs/mcp-orchestrator.md`](docs/mcp-orchestrator.md).

### Вызов инструмента

Вызвать инструмент и получить результат можно флагом `--mcp-call` (без REPL и
без LLM) или в REPL командой `/mcp-call`.

```bash
make build-mcp-server     # собрать bin/mcp-server
make run-mcp-tools        # поднять сервер-процесс, установить соединение,
                          # напечатать tools/list

# вызвать инструмент и напечатать результат:
go run ./cmd/cli --mcp-call create_task --mcp-args '{"title":"Отчёт","priority":"high"}'
go run ./cmd/cli --mcp-call get_task     --mcp-args '{"task_id":"T-001"}'

# или через Makefile:
make run-mcp-call TOOL=create_task ARGS='{"title":"Отчёт","priority":"high"}'
```

В REPL:

```
> /mcp-tools                                    # список инструментов
> /mcp-call create_task {"title":"Написать отчёт","priority":"high"}
{"assignee":"","id":"T-003","priority":"high","status":"open","title":"Написать отчёт"}
```

Пример вывода `make run-mcp-tools`:

```
MCP-соединение установлено (сервер: bin/mcp-server). Инструментов: 14
  • collect_start    Запускает периодический сбор точек данных метрики…
  • collect_status   Агрегат по точкам данных: count/min/max/avg/latest…
  • create_task      Создаёт задачу в mock API и возвращает её полную запись.
  • echo             Возвращает переданное сообщение без изменений.
  • generate_document Формирует документ по теме через LLM…
  • get_summary      Возвращает агрегированную сводку…
  • get_task         Возвращает задачу из mock API по её id.
  • get_time         Возвращает текущее время сервера в формате RFC3339.
  • reminder_add     Ставит отложенное напоминание…
  • reminders_status Агрегированная сводка по напоминаниям…
  • save_to_file     Сохраняет контент в файл и возвращает путь.
  • search           Ищет документы в локальном корпусе…
  • summarize        Строит детерминированную сводку текста…
  • summary_start    Запускает периодические снимки сводки…
```

### В web-интерфейсе

Веб-чат ([`cmd/web`](cmd/web)) тоже умеет вызывать MCP-инструменты: команды
`/mcp-tools` и `/mcp-call <имя> <json>` обрабатываются фронтендом локально и
ходят в эндпоинты `GET /mcp/tools` и `POST /mcp/call`. Запустите web-версию и
введите в поле чата:

```
/mcp-tools
/mcp-call create_task {"title":"Написать отчёт","priority":"high"}
/mcp-call get_task {"task_id":"T-001"}
/mcp-call reminder_add {"text":"Проверить релиз","in_minutes":0}
/mcp-call collect_start {"metric":"cpu","interval_seconds":1,"iterations":3}
/mcp-call get_summary
/mcp-call search {"query":"mcp","limit":2}
/mcp-call summarize {"text":"Что такое MCP. Model Context Protocol…","max_words":20}
/mcp-call save_to_file {"filename":"note.md","content":"Краткая сводка по MCP"}
```

Результат инструмента появится в ленте чата как сообщение ассистента.

Команда запуска сервера задаётся полем `mcp_command` в `config.json`
(по умолчанию `bin/mcp-server`; необязательные аргументы — `mcp_args`).
Проверка автоматизирована в [`feature/mcp/mcp_test.go`](feature/mcp/mcp_test.go):
тестовый бинарник запускается как сервер-помощник, клиент подключается и
сверяет список инструментов (`TestConnectAndListTools`) и вызывает
`create_task`/`get_task`, проверяя возвращённый результат (`TestCallTool`) —
это покрывает требования «соединение устанавливается», «список инструментов
корректно возвращается» и «агент вызывает инструмент и получает результат».
Планировщик покрыт `TestSchedulerTools` и unit-тестами (`store.go`,
`scheduler.go`): персистентность, catch-up, агрегация, лимит догона.

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
| `/facts` `/memory` | показать снапшот слоёв памяти (short/working/long) |
| `/remember <тип> <ключ> <значение>` | явно сохранить в long-память (profile/decision/knowledge/preference) |
| `/newtask` | начать новую задачу: очистить short+working, long сохранить |
| `/profile` | показать активный профиль |
| `/profile new \| use \| set \| list` | персонализация: создать из заготовки / переключить / отредактировать / список |
| `/reset` | очистить короткий и рабочий слои (long сохранить) |
| `/reset-all` | очистить всю память, включая долговременную |
| `/compress` | переключить legacy-сжатие |
| `/mcp-tools` | показать список MCP-инструментов |
| `/mcp-call <имя> <json>` | вызвать MCP-инструмент и напечатать результат |
| `/mcp-pipeline <запрос>` | пайплайн search → summarize → save_to_file |
| `/mcp-orchestrate <тема>` | длинный флоу через несколько MCP-серверов |
| `/invariants` | показать активные инварианты |
| `/invariant add <cat>|<title>|<text>` | добавить правило (архитектура\|техническое решение\|стек\|бизнес-правило) |
| `/invariant rm <id>` `/invariant show <id>` | удалить / показать инвариант |
| `/exit` `/quit` `/q` | выйти |

## RAG (Retrieval-Augmented Generation)

Агент умеет отвечать с опорой на локальный индекс документов
([`../index_service`](../index_service)) — «День 22: первый RAG-запрос»,
«День 23: реранкинг и фильтрация».

**Пайплайн** (`Agent.SayRAG`, две стадии):
1. **Ретрив** — `top_k_fetch` чанков из индекса;
2. **Фильтрация + реранкинг** (этап 2, по конфигу) — отсев по `min_score`,
   пересортировка и обрезка до `top_k_keep`;
3. Системный промпт «контекст с источниками [1]…[n]» → ответ LLM со ссылками.

Без RAG (`Say`) — обычный ответ без контекста.

Ретривер [`feature/rag`](feature/rag) работает в два уровня:
1. **semantic** — микросервис [`../index_service/serve.py`](../index_service/serve.py)
   (модель эмбеддингов и FAISS живут в Python): `POST /search` и `POST /rerank`.
   Запуск: `cd ../index_service && make serve` (по умолчанию порт 8734);
2. **keyword fallback** — если микросервис недоступен, агент сам детерминированно
   ищет по `index_service/index/<strategy>/chunks.jsonl` (движок `keyword`).

**Этап 2 (День 23)** — секция `rag` в [`config.json`](config.json):

| Поле | env | Дефолт | Смысл |
|---|---|---|---|
| `top_k_fetch` | `RAG_TOP_K` | 10 | сколько чанков берём из индекса до этапа 2 |
| `top_k_keep` | `RAG_TOP_K_KEEP` | 4 | сколько остаётся в контексте после этапа 2 |
| `min_score` | `RAG_MIN_SCORE` | 0.30 | порог отсева нерелевантных чанков (0..1) |
| `filter_enabled` | — | true | применять ли фильтр по порогу |
| `rerank` | `RAG_RERANK` | heuristic | `off` / `heuristic` / `cross_encoder` |
| `rerank_model` | — | cross-encoder/mmarco-mMiniLMv2-L12-H384-v1 | модель `POST /rerank` в serve.py |
| `rewrite.enabled` | — | false | query rewrite через LLM перед поиском |
| `rewrite.max_tokens` | — | 80 | лимит токенов при переписывании |

- `heuristic` — гибридный скор в Go: `0.7×cosine + 0.3×лексическое пересечение`
  (работает и для sidecar, и для keyword-движка, т.к. скоры нормализованы в 0..1);
- `cross_encoder` — отдельная модель в index_service (`POST /rerank`), ленивая
  загрузка, при недоступности — graceful fallback на `heuristic`.

Прочие поля: `sidecar_url`, `index_dir`, `strategy`, `top_k` (legacy-алиас
`top_k_fetch`), `max_context_chars`, `timeout_seconds`. Для автономного mcp-server —
env-переменные `RAG_SIDECAR_URL` / `RAG_INDEX_DIR` / `RAG_STRATEGY` / `RAG_TOP_K`.

```bash
make run-rag QUERY="как подключить пакет llm"   # RAG-ответ с источниками
make run-rag-eval                                # 11 контрольных вопросов: с RAG и без
make run-rag-eval JUDGE=1                        # + LLM-as-judge
make run-rag-modes                               # сравнение режимов base/filter/full
# в REPL: /rag <вопрос>
```

Контрольные вопросы — [`rag_questions.json`](rag_questions.json) (11 шт.): для
каждого вопроса заданы **ожидаемые факты** (что должно быть в ответе) и
**ожидаемые источники** (какие файлы должен найти ретривер). Отчёты пишутся в
`results/rag_comparison.md` (с/без RAG) и `results/rag_modes_comparison.md`
(режимы этапа 2).

Текущий результат на корпусе index_service (147 документов, ≈306 стр.):

| Показатель | без RAG | с RAG (filter) |
|---|---|---|
| Покрытие ожидаемых фактов | 10/25 (40%) | **24/25 (96%)** |
| Ожидаемые источники найдены | — | 9/11 |

Сравнение режимов этапа 2 (`make run-rag-modes`, 11 вопросов):

| Режим | Этап 2 | Факты | Источники | fetched→kept |
|---|---|---|---|---|
| `base` | выкл (только top_k_keep) | 23/25 | 8/11 | 10→4 |
| `filter` | min_score + heuristic-реранк | **24/25** | 8/11 | 10→4 |
| `full` | + query rewrite | 21/25 | 7/11 | 10→4 |

**Честный вывод:** фильтрация/реранкинг дают небольшой прирост (24/25 > 23/25);
query rewrite на этом корпусе **вредит** (21/25 < 24/25) — вопросы уже лаконичны,
а переписывание размывает запрос. По умолчанию включён режим `filter`
(rewrite выключен).

MCP: инструменты `rag_search` (найти чанки) и `rag_answer` (RAG-ответ со
ссылками) доступны в домене `knowledge` того же `bin/mcp-server`.

### День 24: обязательные цитаты, источники и анти-галлюцинации

Строгий формат ответа: модель обязана вернуть три секции — **Ответ**,
**Источники**, **Цитаты** — с дословными фрагментами из чанков. Если контекст
слабый — детерминированный отказ «Я не знаю» без вызова LLM.

**Жёсткий гейт «не знаю»** (`answer.go`): если после этапа 2 не осталось чанков
(`Kept==0`) или top-1 score ниже `unknown_below` → ответ `UnknownText` без
обращения к LLM. Мягкий гейт (`looksUnknown`) — эвристика по ответу модели.

**Анти-галлюцинационная валидация** (`citation.go`):
- `ValidateSources` — путь источника из ответа совпадает с путём найденного чанка;
- `ValidateQuotes` — текст цитаты дословно (с точностью до пробелов/регистра)
  встречается в хотя бы одном чанке; проверка по всем чанкам, а не по номеру [n]
  (нумерация модели ненадёжна).

Новые поля секции `rag` в [`config.json`](config.json):

| Поле | env | Дефолт | Смысл |
|---|---|---|---|
| `citations` | `RAG_CITATIONS_OFF` | true | строгий формат (3 секции) |
| `unknown_below` | `RAG_UNKNOWN_BELOW` | 0.40 | порог top-1 score для жёсткого «не знаю» |

```bash
make run-rag-citations               # 11 вопросов + 3 внекорпусных
make run-rag-citations JUDGE=1       # + LLM-as-judge (смысловое соответствие)
```

Внекорпусные вопросы — [`rag_unknown_questions.json`](rag_unknown_questions.json)
(3 шт.): борщ, квантовая запутанность, столица Новой Зеландии. Отчёт:
`results/rag_citations.md`.

Результат (`make run-rag-citations JUDGE=1`, 11 + 3 вопросов):

| Показатель | Значение |
|---|---|
| Формат соблюдён (3 секции) | **11/11** |
| Источники есть | **11/11** |
| Цитаты есть | **11/11** |
| Источники достоверны | 15/20 (5 галлюцинаций) |
| Цитаты дословны | 17/20 (3 галлюцинации) |
| Покрытие фактов | **25/25** |
| Judge: yes / partial / no | 8 / 1 / 0 |
| «Не знаю» на внекорпусных | **3/3** |

**Честный вывод:** строгий формат + дословная проверка цитат ловят галлюцинации
(3/20 цитат и 5/20 источников не прошли валидацию). Жёсткий гейт «не знаю»
срабатывает на всех внекорпусных вопросах (3/3) без ложных срабатываний на
корпусных. LLM-as-judge подтверждает смысловое соответствие в 8/11 случаев.

### День 25: мини-чат с RAG + памятью задачи (production-like)

Многоходовый диалог, в котором агент на каждый вопрос ищет контекст через RAG,
отвечает с источниками и не теряет цель на протяжении 10–15 реплик.

**Гибридный метод [`SayChat()`](agent/agent.go:505)** объединяет возможности
[`Say()`](agent/agent.go:394) (история диалога + многослойная память + FSM +
инварианты) и [`SayRAG()`](agent/agent.go:410) (ретрив + строгий формат Дня 24
с источниками и цитатами):

1. **Авто-цель**: первое сообщение пользователя → `Working().Set("goal", …)`
   (обрезка до 200 рун). Переопределить — `/goal` в CLI или поле в web.
2. **Сборка системных блоков** [`extraSystemBlocks()`](agent/agent.go:477):
   инварианты → профиль → FSM → long → working — инъекция **до** RAG-контекста.
3. **RAG с историей** [`AnswerRAGWithHistory()`](agent/feature/rag/chat.go:1):
   ретрив → hard-гейт «не знаю» → сборка `[extraSystem → system+context →
   history → query]` → LLM → ParseCitation + ValidateSources/Quotes + soft-гейт.
4. **Роутинг фактов** [`RouteUserTurn()`](agent/feature/memory/extract.go:1):
   LLM-экстрактор извлекает ограничения, термины, уточнения → `Working()`.
5. **Reply** с `Sources`, `Citation`, `Unknown`, `FormatOK`, `SessionFacts`.

**Память задачи** — лёгкий session-слой поверх `Working()` (без новой БД, без
FSM): ключ `goal` + авто-извлечённые факты (constraint/term/clarification).
Рендерится как системный блок через [`WorkingSystem()`](agent/feature/memory/layered.go:126).

**Сценарии** [`chat_scenarios.json`](agent/chat_scenarios.json:1) — 2 сценария
по 10–11 реплик, каждый с `goal`, `goal_keywords` и ожидаемыми фактами/
источниками на ход. Автопроверка: источники есть, формат OK, факт-покрытие,
«цель не потеряна» (majority-match `goalKeywordsHit` — устойчив к русской
морфологии: «день» ≠ «дня»). Отчёт: `results/chat_scenarios.md`.

```bash
make run-chat            # CLI REPL: /goal, /facts, /reset, /exit
make run-chat-scenarios  # 2 сценария → results/chat_scenarios.md
make run-web             # web: RAG-тумблер + блок источников + панель «Память задачи»
```

В web-интерфейсе: чекбокс **RAG** в форме → `POST /chat {rag:true}` (без SSE) →
ответ с `sources`/`engine`/`unknown`/`format_ok`/`session_facts`/`goal`.
Панель «Память задачи» (`/session`) показывает цель и факты сессии, поле
«Задать цель» → `POST /goal`.

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

## Модель памяти (memory layers)

Память агента разделена на три независимо хранимых слоя (см. [`feature/memory`](feature/memory) и план [`plans/memory-model-plan.md`](plans/memory-model-plan.md)):

| Слой | Хранит | Где хранится | Жизненный цикл |
|------|--------|--------------|----------------|
| **short** — краткосрочная | история текущего диалога | [`feature/dialog`](feature/dialog): SQLite (`history_file`) или RAM | очищается при `/reset`, `/newtask` |
| **working** — рабочая | данные ТЕКУЩЕЙ задачи: цель, ограничения, дедлайн, решения-в-процессе | [`feature/memory`](feature/memory): RAM (ключ → значение) | очищается при `/newtask`, `/reset` |
| **long** — долговременная | профиль, принятые решения, знания, предпочтения | [`feature/memory`](feature/memory): SQLite (`long_memory_file`) | переживает перезапуск и `/reset` |

Выбор «что и куда сохраняется» — явный:
- **short**: каждый ход `user`+`assistant` пишется в `Say()`.
- **working**: после хода `RouteUserTurn()` извлекает ключевые факты реплики (`цель:`, `ограничение:`, …) и кладёт их в рабочий слой.
- **long**: только явно — командой `/remember` / эндпоинтом `/remember` (или политикой классификации профиль/решение/знание).

На ответы агента слои влияют через сборку запроса: перед историей диалога вставляются system-блоки долговременной и рабочей памяти ([`LayeredMemory.Prepend`](feature/memory/layered.go)). Проверить влияние:

```bash
go run ./cmd/cli --compare-memory   # агент с long-памятью vs без неё, оценка recall
```

В веб-интерфейсе (`web/index.html`) есть панель «Память агента (3 слоя)»: она в реальном
времени показывает содержимое short/working/long после каждого хода, а также позволяет
записать запись в long-память (`POST /remember`) и начать новую задачу (`POST /newtask`,
очищает short+working, сохраняя long) — наглядная демонстрация процесса управления памятью.

Полезные команды REPL: `/memory` (снапшот трёх слоёв), `/remember profile имя Анна`, `/newtask` (новая задача).

## Персонализация

Поверх многослойной памяти работает персонализация ([`feature/profile`](feature/profile)):
структурированный **профиль пользователя** подключается **первым system-блоком** к каждому запросу,
поэтому ассистент автоматически адаптирует стиль, формат и ограничения под пользователя.

Профиль содержит: `id`, `name`, `role`, `language`, `style`, `format`, `constraints`, `expertise`.
Порядок сборки запроса: **[профиль] → [long-память] → [working-память] → история → ввод**.

Хранение — SQLite (`profile_file`, по умолчанию `agent_profiles.db`), переживает перезапуск.
Активный профиль задаётся в конфиге (`active_profile`) или командой.

Заготовки профилей (для быстрой демонстрации и сравнения): `kutyakin` (Flutter/Dart-разработчик,
пример из анкеты), `terse` (максимально кратко), `detailed` (развёрнуто).

Команды REPL:
- `/profile` / `/profile show` — показать активный профиль;
- `/profile list` — список сохранённых;
- `/profile new <template>` — создать и активировать из заготовки (`kutyakin|terse|detailed`);
- `/profile use <id>` — переключить активный;
- `/profile set <поле> <значение>` — отредактировать (поля: `style|format|name|role|language|constraint|expertise`).

Web: персонализация — **экран внутри единого SPA** ([`web/index.html`](web/index.html)) по
hash-маршруту `#/profiles` (кнопка «Профили» в тулбаре). Чат и профили переключаются без
перезагрузки страницы. Создание из заготовки, переключение активного профиля, редактор полей.
API: `GET/PUT /profile`, `POST /profile/use`, `POST /profile/template`.

Проверка влияния разных профилей на ответы:

```bash
go run ./cmd/cli --compare-profiles   # один вопрос, профили terse vs detailed
```

## Контролируемые переходы состояния задачи (FSM)

Состояние задачи — конечный автомат ([`feature/task`](feature/task)) с **явной
таблицей допустимых переходов** и предусловиями (guard). Ассистент не может
«перепрыгнуть» этап:

| Переход | Из → В | Предусловие |
|---------|--------|-------------|
| `begin` | любое → `planning` | непустая цель |
| `approve_plan` | `planning` → `planning` | активна, не пауза |
| `to_execution` | `planning` → `execution` | **план утверждён** |
| `to_validation` | `execution` → `validation` | активна, не пауза |
| `finalize` | `validation` → `done` | только через `accept` |
| `rework` | `validation` → `execution` | активна, не пауза |

Ключевые правила:
- **реализация только после утверждённого плана** — переход `planning→execution`
  требует `ApprovePlan` (`/approve`); иначе структурированный отказ с подсказкой;
- **финал только после валидации** — `done` достижим только через `Accept`
  (`/accept`), голый `next` из `validation` отклоняется.

**Авто-генерация плана через LLM.** При старте задачи (`/begin <цель>`) агент
вызывает нейросеть, чтобы составить пошаговый план реализации, и сохраняет его в
состояние задачи (`State.Plan`); команда `/plan` перегенерирует план. План
выводится в ленту чата как сообщение («📋 План реализации»). Если LLM недоступен —
задача не ломается: план просто не создан, `/approve → /next` остаются доступны.

**Реальное исполнение плана.** При переходе на этап «исполнение» (`/approve`
затем `→ Этап`) агент **выполняет шаги плана через LLM**: для каждого шага вызывает
нейросеть с целью и текстом шага, результат появляется в чате как сообщение
«Шаг N/M: …», затем автоматически переходит к следующему шагу (`ExecuteCurrentStep`
+ `Advance`). Результаты шагов накапливаются в `State.Work`. В CLI можно выполнять
шаг вручную командой `/run`.

**Валидация, доработка и сводка через LLM.** На этапе «валидация» агент
**проверяет результат через LLM** ([`ValidateWork`](validate.go)): вердикт
`OK`/`REWORK` и оценка появляются в чате. При `REWORK` доработка выполняется через
LLM ([`ReworkWithLLM`](validate.go)) — указания по исправлению сохраняются в работу,
задача возвращается на исполнение. После `accept` агент собирает **итоговую сводку**
([`SummarizeDone`](validate.go)). В CLI — команды `/validate` и `/finalize`.

При недопустимом переходе возвращается `task.TransitionError` с объяснением
(какой переход, почему нельзя, что сделать вместо). На паузе все переходы
заблокированы; `Resume` продолжает с того же шага. Состояние (включая флаги
`план утверждён`/`валидация` и черновик плана) переживает перезапуск (SQLite)
и попадает в system-блок запроса, чтобы ассистент учитывал ограничения в
рассуждении.

Команды CLI: `/begin <цель>` (авто-план через LLM), `/plan`, `/approve`
(утвердить план), `/next` (planning→execution после `/approve`, execution→validation),
`/run` (выполнить текущий шаг плана через LLM), `/accept` (принять после валидации),
`/rework`, `/pause`, `/resume`. В web-чате те же действия — кнопки «✔ План», «→ Этап»
и команды; отказ показывается toast-сообщением с объяснением, а во время LLM-вызова
(план, ответ, шаги плана) в ленте чата отображается индикатор «Агент пишет/выполняет…».

## Инварианты и ограничения состояния

Инварианты ([`feature/invariants`](feature/invariants)) — жёсткие правила, которые
ассистент **не имеет права нарушать**: выбранная архитектура, принятые технические
решения, ограничения стека, бизнес-правила. Их свойства:

- **Хранятся отдельно от диалога** — персистентное хранилище SQLite
  (`invariants_file`, по умолчанию `agent_invariants.db`). Не попадают в short/working
  слои памяти и переживают `/reset`, `/newtask` и перезапуск.
- **Явно учитываются в рассуждении** — system-блок со всеми активными инвариантами
  инжектируется **первым** сообщением каждого запроса (выше профиля и состояния задачи).
- **Отказ при нарушении** — двухуровневая защита:
  1. **Детерминированный pre-check** в `Say()`: если запрос содержит ключевое слово
     `hard`-инварианта (`Keywords`), ассистент возвращает структурированный отказ
     **без обращения к LLM** (экономия токенов и гарантированный отказ);
  2. **System-блок**: даже семантически сложный конфликт модель обязана распознать
     сама и отказаться, объяснив, какой инвариант и почему нарушается.

Категории инвариантов: `архитектура`, `техническое решение`, `стек`, `бизнес-правило`.
Строгость: `hard` (обязательный отказ) и `soft` (предупреждение, ответ допустим).
При инициализации пустое хранилище засеивается предзаданными примерами, поэтому
агент «работает в рамках инвариантов» из коробки.

Проверка поведения при конфликте:

```bash
go run ./cmd/cli --check-invariants   # конфликтующий запрос → текст отказа
```

REPL-команды управления: `/invariants` (список), `/invariant add <cat>|<title>|<text>`,
`/invariant rm <id>`, `/invariant show <id>`.

**Web**: инварианты выводятся в правой панели чата отдельным блоком «🛡️ Инварианты»
(жёсткие правила) с формой добавления и удалением. API: `GET /invariants` (список),
`POST /invariants` (добавить), `POST /invariants/delete` (удалить по `id`). Дизайн — в
[`plans/invariants-plan.md`](plans/invariants-plan.md).

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
6. **RAG** — ретрив + этап 2 (фильтр/реранк) + строгий формат с цитатами и
   анти-галлюцинационной валидацией; жёсткий гейт «не знаю» на внекорпусные
   вопросы (`--rag-citations`).

Подробнее — [`../plans/agent-week-plan.md`](../plans/agent-week-plan.md) и
[`plans/context-management-plan.md`](plans/context-management-plan.md).
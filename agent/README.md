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
│   └── profile/      # персонализация: UserProfile, ProfileStore (SQLite), SystemBlock()
├── cmd/
│   ├── cli/main.go   # консольный чат (REPL) + сравнения
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
go run ./cmd/cli --compare-memory    # влияние долговременной памяти на ответы (long vs без)
go run ./cmd/cli --compare-profiles  # влияние персонализации (профили terse vs detailed)
go run ./cmd/web                     # web-чат: http://127.0.0.1:8080
go run ./cmd/web --strategy branch   # web-чат со стратегией по умолчанию (window|facts|branch)
make stop-web                        # остановить ранее запущенный go run ./cmd/web (по порту 8080)
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
| `/facts` `/memory` | показать снапшот слоёв памяти (short/working/long) |
| `/remember <тип> <ключ> <значение>` | явно сохранить в long-память (profile/decision/knowledge/preference) |
| `/newtask` | начать новую задачу: очистить short+working, long сохранить |
| `/profile` | показать активный профиль |
| `/profile new \| use \| set \| list` | персонализация: создать из заготовки / переключить / отредактировать / список |
| `/reset` | очистить короткий и рабочий слои (long сохранить) |
| `/reset-all` | очистить всю память, включая долговременную |
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
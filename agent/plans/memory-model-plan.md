# Модель памяти агента (memory layers)

Задание: описать и реализовать явную модель памяти агента с минимум тремя типами
памяти, которые хранятся отдельно, и явным выбором «что и куда сохраняется».

## Итоговая структура (рефакторинг под feature/)

Код разнесён по фичам в `feature/`; ядро `Agent` и команды остаются в пакете `agent`:

```
agent/
├── agent.go           # ядро Agent (LLM-клиент + многослойная память + контекст)
├── config.go / tokens.go / compare.go
├── feature/
│   ├── dialog/        # short-term: Memory, InMemory, SQLiteMemory (+ memory_test.go)
│   ├── context/       # ContextStrategy + SlidingWindow / FactsMemory / Branching /
│   │                  # ContextManager + StrategyEvent (+ tests)
│   └── memory/        # extract.go, working.go, longterm.go, layered.go (+ tests)
├── cmd/cli, cmd/web
└── web/index.html
```

Зависимости (без циклов): `feature/dialog` ← `feature/memory` ← `feature/context`
← пакет `agent`.

## Три слоя памяти

| Слой | Назначение | Реализация | Хранение | Очистка |
|------|-----------|------------|----------|---------|
| **short** | история текущего диалога | `feature/dialog.Memory` | SQLite (`history_file`) или RAM | `/reset`, `/newtask`, `ResetSession()` |
| **working** | данные текущей задачи (ключ→значение) | `feature/memory.WorkingStore` | RAM | `/newtask`, `ResetSession()` |
| **long** | профиль, решения, знания | `feature/memory.LongStore` | SQLite (`long_memory_file`) | `/reset-all`, `ResetAll()` |

Слои инкапсулированы в `feature/memory.LayeredMemory` с доступами `Short()`,
`Working()`, `Long()`.

## Явный выбор «что и куда сохраняется»

1. **short** — автоматически: в `Agent.Say()` после каждого хода пишутся
   `user`+`assistant`.
2. **working** — после хода `LayeredMemory.RouteUserTurn(text, extractor)` извлекает
   факты реплики (`цель:`, `ограничение:`, …) и кладёт их в рабочий слой.
   Извлечение — `feature/memory.Extractor` (heuristic регулярками или LLM).
3. **long** — только явно: `LayeredMemory.Remember(kind, key, value)` / команда
   `/remember` / эндпоинт `/remember`. Типы: `profile | decision | knowledge | preference`.

Это демонстрируется в [`cmd/cli/main.go`](../cmd/cli/main.go) (команды `/memory`,
`/remember`, `/newtask`, `/reset-all`).

## Как слои влияют на ответы

Перед историей диалога вставляются два system-блока ([`LayeredMemory.Prepend`](../feature/memory/layered.go)):

```
[system: долговременная память о пользователе]
[system: данные текущей задачи]
[история диалога (short, через стратегию контекста)]
[ввод пользователя]
```

Проверка влияния — `go run ./cmd/cli --compare-memory`: два агента (с long-памятью
и без), в long заранее положены профиль/решение/знание; задаётся вопрос, требующий
их вспомнить; recall оценивается эвристически по ключевым терминам.

## Что попадает в каждый слой (проверка)

Автотесты в `feature/memory/*_test.go` и `strategy_test.go` проверяют:

- **Раздельность слоёв** — запись в один слой не влияет на другие (`TestLayersStoredSeparately`).
- **Явный роутинг** — извлечённые факты попадают в working; повторные — не дублируются (`TestRouteUserTurn`).
- **Персистентность long** — запись переживает перезапуск SQLite (`TestSQLiteLongPersistence`).
- **Композиция prompt** — Prepend ставит long+working перед историей (`TestPrependComposition`).
- **Сброс сессии** — `ResetSession` чистит short+working, но НЕ long (`TestResetContextKeepsLong`).
- **Извлечение** — heuristic-экстрактор детерминирован (`TestHeuristicExtractFacts`).

Статус: реализовано и покрыто тестами; `go build ./...`, `go vet ./...`,
`go test ./...` — зелёные.
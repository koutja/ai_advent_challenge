# Управление контекстом: 3 стратегии (без summary)

Задача — дать агенту переключаемые стратегии управления контекстом **без сжатия-сводки**
(существующий `ContextManager` из [`compress.go`](compress.go:9) — summary-подход Этапа 4,
его в новую модель не вводим, но код оставляем для обратной совместимости).

## Цель

Агент с 3 стратегиями контекста + сравнение их на одном сценарии («собираем ТЗ»):
Sliding Window / Facts / Branching. Сравнение по качеству ответа, стабильности,
расходу токенов и удобству пользователя.

---

## Архитектура

Вводим интерфейс стратегии; `Agent` вместо единственного поля `compress` держит
активную стратегию. Каждая стратегия получает полную историю из `Memory`, но решает,
какие сообщения отправить в LLM и как вести собственное состояние.

### Интерфейс `ContextStrategy` (новый файл `context.go`)

```go
type ContextStrategy interface {
    Name() string
    // Build готовит сообщения запроса: история + новое сообщение пользователя.
    Build(hist []llm.Message, input string) []llm.Message
    // Observe вызывается после сохранения хода (user+assistant) в память,
    // чтобы стратегия могла обновить внутреннее состояние (например, факты).
    Observe(hist []llm.Message) error
    // Reset сбрасывает внутреннее состояние стратегии.
    Reset() error
    // State возвращает снапшот состояния стратегии (для отладки/сравнения).
    State() string
}
```

Метод `Say()` в [`agent.go`](agent.go:73):
1. грузит полную историю `hist := a.memory.Load()`;
2. вызывает `a.strategy.Build(hist, input)` → `msgs`;
3. отправляет `msgs` в LLM;
4. сохраняет ход в память;
5. вызывает `a.strategy.Observe(полная история после сохранения)`.

### Селектор и замена поля

- В `Agent` поле `compress *ContextManager` заменяем/дополняем полем
  `strategy ContextStrategy`.
- Добавляем методы `SetStrategy(name string) error` и `StrategyName() string`.
- Для обратной совместимости: если `cfg.Compress == true` и стратегия не задана —
  включаем существующий `ContextManager` (легаси). Новые стратегии выбираются через
  `Config.ContextStrategy` или команды CLI.

### Конфиг ([`config.go`](config.go:19))

Добавляем поле:

```go
ContextStrategy string `json:"context_strategy"` // window | facts | branch (пусто = off/legacy)
```

Параметры стратегий (с дефолтами): `window_size`, `facts_key_max` (лимит ключей).

---

## Стратегия 1: Sliding Window (`window.go`)

Хранит только последние N сообщений, всё старше — отбрасывается на уровне запроса
(память продолжает хранить полную историю; стратегия просто режет слайс).

```go
type SlidingWindow struct { size int }
func (w *SlidingWindow) Build(hist []llm.Message, input string) []llm.Message {
    n := len(hist)
    if n > w.size { hist = hist[n-w.size:] }
    return append(append([]llm.Message{}, hist...), llm.Message{Role:"user", Content:input})
}
```

- Самый дешёвый по токенам, но ранние детали (цель/дедлайн) теряются при N < длины.
- `Observe`/`Reset` — no-op.

---

## Стратегия 2: Facts / Key-Value Memory (`facts.go`)

Отдельный блок «facts» (ключ-значение): `map[string]string`. В запрос уходит
`facts (как system) + последние N сообщений`.

Извлечение фактов из каждого сообщения пользователя — два режима:
- **LLM-режим** (по умолчанию, точнее): отдельный вызов LLM с промптом
  «извлеки ключ-значение: цель, ограничения, предпочтения, решения, договорённости».
  Стоит дополнительных токенов (учитываем в сравнении).
- **Хьюристический режим** (запасной/для тестов): регулярки по маркерам
  (`цель:`, `ограничение:`, `дедлайн:`, `предпочтение:`, ...).

```go
type FactsMemory struct {
    client    *llm.Client
    facts     map[string]string
    keepLast  int
    extractor Extractor // llm или heuristic
}
func (f *FactsMemory) Build(hist []llm.Message, input string) []llm.Message {
    msgs := []llm.Message{}
    if len(f.facts) > 0 {
        msgs = append(msgs, llm.Message{Role:"system", Content:"Факты диалога:\n"+f.render()})
    }
    if n := len(hist); n > f.keepLast { hist = hist[n-f.keepLast:] }
    msgs = append(msgs, hist...)
    msgs = append(msgs, llm.Message{Role:"user", Content:input})
    return msgs
}
func (f *FactsMemory) Observe(hist []llm.Message) error {
    // извлекаем факты из последнего user-сообщения и мержим в f.facts
}
```

- Держит важные детали дольше Sliding Window при низком расходе на вывод.
- Расход = факты-блок + N сообщений + стоимость извлечения (LLM-режим).
- `State()` возвращает текущие факты.

---

## Стратегия 3: Branching (`branch.go`)

Checkpoint-и и ветки диалога. Стратегия владеет собственными копиями истории по веткам:

```go
type Branching struct {
    branches map[string][]llm.Message // id ветки -> история
    active   string                   // активная ветка
    nextID   int
}
// API (через CLI-команды и методы стратегии):
//   Checkpoint(name string)      — сохранить снапшот активной ветки (имя=контрольная точка)
//   Branch(from string)          — создать новую ветку-копию от checkpoint, переключиться на неё
//   Switch(branch string)        — переключить активную ветку
//   Active() string / Branches() []string
```

Особенности интеграции:
- Поскольку ветки живут в стратегии, а не в общей `Memory`, в интерфейс добавляем
  метод `History(mem Memory) []llm.Message`, который для Branching возвращает
  `branches[active]`, а для остальных — `mem.Load()`. `Say()` использует его.
- Сохранение в общую `Memory` при активной ветке опционально (по умолчанию ветки
  держим в RAM стратегии, чтобы не смешивать с основной историей).
- `Observe` аппендит ход в `branches[active]`.
- Полная история ветки → наибольшая точность и стабильность, но самый высокий расход токенов.

---

## Интеграция в CLI ([`cmd/cli/main.go`](cmd/cli/main.go))

Новые команды REPL (по образцу существующих `/compress`, `/reset`):
- `/strategy` — показать текущую; `/strategy window|facts|branch` — переключить.
- `/checkpoint <имя>` — создать контрольную точку в Branching.
- `/branch <имя>` — создать ветку от последнего checkpoint и переключиться.
- `/switch <имя>` — переключиться между ветками.
- `/facts` — показать текущие факты (для Facts).

Новый флаг для сравнения:
```
go run ./cmd/cli --compare-strategies   # прогнать сценарий на всех 3 стратегиях
```

---

## Сравнительный прогон (harness)

Реализовать в CLI функцию `runCompareStrategies(ag *Agent)`:

1. **Сценарий** — фиксированный скрипт ~12 ходов «собираем ТЗ»: цель, аудитория,
   фичи, ограничения, анти-фичи, дедлайн, предпочтения по стеку, договорённости.
   Плюс финальный «проверочный» вопрос: «Перечисли цель, ограничения и дедлайн».
2. Для каждой стратегии создаём **свежий** агент, прогоняем тот же сценарий,
   собираем ответы и метрики.
3. Метрики:
   - **Качество**: балл = сколько ключевых фактов сценария верно воспроизведено в
     проверочном ответе. Оценивается LLM-судьёй (отдельный промпт) либо вручную.
   - **Стабильность**: доля важных деталей (цель/ограничения/дедлайн), не потерянных
     к концу диалога (по судейству/эвристике по ключевым словам).
   - **Расход токенов**: сумма `request+completion` по всем ходам стратегии
     (берём из `Usage`, локальная оценка — запасной вариант через `MessagesTokens`).
   - **UX**: число действий/команд пользователя, средняя длина ответа.
4. Вывод: таблица-сравнение в консоль + markdown-отчёт `plans/compare-context.md`.

Ожидаемые тенденции (для проверки):
- Sliding Window — минимум токенов, но риск потери ранних фактов при маленьком N.
- Facts — баланс: мало токенов на вывод, детали держит, но тратит на извлечение.
- Branching — максимум токенов и стабильности в ветке, гибкий UX, но сложнее управление.

---

## Файлы

| Файл | Действие |
|------|----------|
| `context.go` | новый: интерфейс `ContextStrategy` + селектор |
| `window.go` | новый: `SlidingWindow` |
| `facts.go` | новый: `FactsMemory` + извлечение фактов |
| `branch.go` | новый: `Branching` + checkpoint/branch/switch |
| `agent.go` | правка: `Say()` через стратегию, `SetStrategy`, `StrategyName` |
| `config.go` | правка: `ContextStrategy` + параметры |
| `config.json` | правка: `context_strategy` |
| `cmd/cli/main.go` | правка: команды + `--compare-strategies` |
| `context_test.go`, `window_test.go`, `facts_test.go`, `branch_test.go` | новые тесты |
| `README.md` | правка: документация стратегий и команд |

## Тесты

- `window_test.go`: режет историю до N, сохраняет input, no-op Observe.
- `facts_test.go`: хьюристическое извлечение (детерминировано, без LLM), мерж фактов,
  сборка запроса (facts system + N сообщений).
- `branch_test.go`: Checkpoint → Branch → Switch → независимые истории веток.
- `context_test.go`: селектор `SetStrategy` валидными/невалидными именами, `Say`
  использует выбранную стратегию.

---

## Порядок реализации

1. Интерфейс `ContextStrategy` + селектор.
2. `SlidingWindow`.
3. `FactsMemory` (LLM + хьюристика).
4. `Branching` (checkpoint/branch/switch, `History`).
5. Интеграция в `Agent.Say` + `Config`.
6. CLI-команды и флаг сравнения.
7. Юнит-тесты.
8. Harness сравнения + отчёт.
9. README.
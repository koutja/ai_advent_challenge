# Состояние задачи: конечный автомат

Цель — дать агенту **формализованное состояние задачи** как конечный автомат:
этап задачи, текущий шаг, ожидаемое действие. Поддержать паузу на любом этапе и
продолжение **без повторных объяснений** (состояние инжектируется в каждый запрос).

## Модель состояний

Этапы (строго по порядку):

```
planning → execution → validation → done
```

Ключевые поля состояния:

- `Goal` — цель задачи (ставится при старте).
- `Stage` — текущий этап (`planning | execution | validation | done`).
- `Step` — номер текущего шага внутри этапа (1-based).
- `Expected` — ожидаемое действие на текущем шаге.
- `Paused` — признак паузы.
- `Log` — краткие итоги выполненных шагов (компактный контекст для resume).

### Таблица переходов

| Из | Событие | В | Условие |
|----|---------|---|---------|
| – (новое) | `Begin(goal)` | planning | всегда |
| planning | `Advance()` | planning (шаг+1) | всегда |
| execution | `Advance()` | execution (шаг+1) | всегда |
| planning | `NextStage()` | execution | всегда |
| execution | `NextStage()` | validation | всегда |
| validation | `NextStage()` | done | всегда |
| done | любой шаг | – | запрещено |
| validation | `Rework(reason)` | execution | причина обязательна |
| любой не-done | `Pause()` | тот же (paused) | всегда |
| paused | `Resume()` | тот же (не paused) | сохраняет Step/Expected |

Пауза разрешена на planning/execution/validation; done — терминальный, пауза там
не имеет смысла. `Resume` обязан сохранить текущий `Step` и `Expected`, чтобы
продолжение шло с того же места без повторных объяснений.

## Архитектура

Новый feature-пакет `agent/feature/task/` по образцу `feature/memory` /
`feature/dialog`: интерфейс + in-memory + SQLite реализации + тесты.

### Интерфейс `Machine` (файл `state.go`)

```go
const (
    StagePlanning   = "planning"
    StageExecution  = "execution"
    StageValidation = "validation"
    StageDone       = "done"
)

// State — снапшот состояния задачи для отображения/инжекции.
type State struct {
    Goal     string
    Stage    string
    Step     int
    Expected string
    Paused   bool
    Log      []string
}

type Machine interface {
    Begin(goal string) error
    SetExpected(action string) error
    Advance(summary string) error     // шаг+1, summary в Log
    NextStage() error                  // planning→execution→validation→done
    Rework(reason string) error        // validation→execution
    Pause() error
    Resume() error
    Snapshot() State
    SystemBlock() string               // system-блок для инжекции в запрос
    Reset() error
}
```

`SystemBlock` собирает компактный текст: цель, этап, текущий шаг, ожидаемое
действие и последние итоги из `Log` — именно он обеспечивает «продолжение без
повторных объяснений».

### Хранение (`store.go`)

- `Store` — интерфейс: `Save(State) error`, `Load() (State, error)`, `Clear() error`.
- `InMemoryStore` — для тестов/лёгких окружений.
- `SQLiteStore` — таблица `task_state` (одна активная задача), поле `log` в JSON.
  Пауза и шаг переживают перезапуск.

### Интеграция в `Agent`

- `Config`: поле `TaskFile string json:"task_file"` (путь к SQLite; пусто — off).
- Поле `tasks task.Machine` в [`agent.go`](agent/agent.go:23); создаётся в `New()`.
- Геттеры/обёртки на агенте: `Task() task.Machine`, `BeginTask(goal)` и т.п.
- **Инжекция в промпт**: в [`prepareMessages`](agent/agent.go:326) после блока
  профиля перед памятью вставляем `task.SystemBlock()` (если есть активная задача).
  Так на каждом ходу LLM видит текущий этап/шаг/ожидаемое действие — возобновление
  идёт сразу с нужного места.
- `ResetContext` также сбрасывает состояние задачи (новая задача).

### CLI ([`cmd/cli/main.go`](agent/cmd/cli/main.go:26))

Команды: `/task` (показать состояние), `/begin <цель>`, `/expected <действие>`,
`/step <итог>` (Advance), `/next` (NextStage), `/rework <причина>`, `/accept`
(validation→done), `/pause`, `/resume`.

## Файлы

- `agent/feature/task/state.go` — автомат (типы, переходы, SystemBlock).
- `agent/feature/task/store.go` — Store + InMemory + SQLite.
- `agent/feature/task/state_test.go` — легальные/нелегальные переходы, пауза/resume.
- `agent/feature/task/store_test.go` — персистентность (пауза и шаг после перезагрузки).
- `agent/config.go` — поле `TaskFile`.
- `agent/agent.go` / `agent/task.go` — поле `tasks`, обёртки, инжекция в prepareMessages.
- `agent/cmd/cli/main.go` — команды.
- `agent/config.json` — включить `task_file`.

## Критерии приёмки

1. Автомат проводит `planning → execution → validation → done`, нелегальные
   переходы возвращают ошибку.
2. `Pause` возможен на любом этапе (кроме done); `Resume` сохраняет `Step`/`Expected`.
3. После `Pause`/`Resume` (в т.ч. через перезапуск процесса) агент продолжает с того же
   шага: `SystemBlock` несёт текущий этап/шаг/ожидаемое действие + итоги из `Log`.
4. Состояние инжектируется в каждый запрос `Say()` (проверка в `prepareMessages`).
5. Все тесты (`go test ./...` в `agent/`) зелёные.
# План: контролируемые переходы состояний задачи

## Цель

Реализовать **явные, контролируемые переходы** между состояниями задачи: у задачи есть
набор допустимых состояний и таблица разрешённых переходов с предусловиями (guard).
Ассистент не может «перепрыгнуть» этап: реализация — только после утверждённого плана,
финал — только после валидации. При попытке недопустимого перехода ассистент
возвращает структурированный отказ с объяснением и подсказкой, что сделать вместо этого.

## Текущее состояние (`feature/task`)

Уже есть конечный автомат ([`state.go`](feature/task/state.go)):
- Этапы `planning → execution → validation → done`, последовательный обход через
  `NextStage()` (пропуск этапа невозможен по индексу `StageOrder`).
- `Rework()` validation→execution; `Pause()`/`Resume()` сохраняют `Step`/`Expected`;
  guard'ы на «нет активной задачи / done / пауза».
- Точки входа: ядро [`agent/task.go`](task.go) (`BeginTask`, `TaskState`), CLI
  [`handleTaskCmd`](cmd/cli/main.go:401) (`begin/expected/step/next/accept/rework/pause/resume`),
  web [`handleTask`](cmd/web/main.go:591) (те же команды через `POST /task`).

### Пробелы относительно задания

1. **Нет декларативной таблицы переходов** — правила размазаны по методам с
   разрозненными проверками; нет единого, тестируемого источника истины.
2. **Нет guard-условий для двух ключевых примеров:**
   - `planning → execution` выполняется безусловно, хотя по заданию нужна
     «реализация только после утверждённого плана»;
   - `validation → done` — `accept` проверяет только `stage == validation`, но нет
     понятия «валидация действительно пройдена» → финал возможен «без валидации».
3. **Реакция/отказ неструктурированы** — CLI выводит сырую ошибку (`ошибка: ...`),
   web — текст ошибки; нет объяснения «почему нельзя» и «что сделать вместо».
4. Продолжение после паузы уже сохраняет шаг; требуется добавить явную проверку, что
   **все** stage-переходы заблокированы на паузе, и покрыть тестами.

## Дизайн

### 1. Новый файл `feature/task/transitions.go` — декларативная таблица

```go
// TransitionID — идентификатор перехода.
type TransitionID string

const (
    TrBegin        TransitionID = "begin"         // → planning (из любого состояния)
    TrApprovePlan  TransitionID = "approve_plan"  // planning → planning (утвердить план)
    TrToExecution  TransitionID = "to_execution"  // planning → execution
    TrToValidation TransitionID = "to_validation" // execution → validation
    TrFinalize     TransitionID = "finalize"      // validation → done
    TrRework       TransitionID = "rework"        // validation → execution
)

// Transition — один разрешённый переход: from → to с предусловием и мутацией.
type Transition struct {
    ID    TransitionID
    From  string            // "" = любое
    To    string
    Guard func(st State) error // nil = без предусловия
    Apply func(st *State)
}

// validTransitions — таблица допустимых переходов (единый источник истины).
var validTransitions []Transition
```

**Таблица:**
| ID | From | To | Guard (предусловие) | Apply |
|----|------|----|--------------------|-------|
| `begin` | любое | `planning` | цель не пустая | сброс: goal/stage=planning/step=1, PlanApproved=false |
| `approve_plan` | `planning` | `planning` | активна, не пауза | `PlanApproved=true`, лог «план утверждён» |
| `to_execution` | `planning` | `execution` | активна, не пауза, **`PlanApproved`** | stage=execution, step=1 |
| `to_validation` | `execution` | `validation` | активна, не пауза | stage=validation, step=1 |
| `finalize` | `validation` | `done` | активна, не пауза | `Validated=true`, лог «принято», stage=done |
| `rework` | `validation` | `execution` | активна, не пауза | `Validated=false`, stage=execution, step=1 |

Общие guard'ы (проверяются всегда): состояние активно (кроме `begin`), не пауза (кроме
`begin`/`resume`), переход не из терминального `done` (кроме `begin`).

### 2. `State` — два новых флага (store, `state.go`)

```go
PlanApproved bool `json:"plan_approved"` // true — план утверждён (пускает в execution)
Validated     bool `json:"validated"`    // true — валидация пройдена (доступен финал)
```

Обновляются DDL SQLite (`task_state`), `State`/`SystemBlock()` (строка «План утверждён:
да/нет», «Валидация: да/нет»), JSON-сериализация.

### 3. Структурированный отказ `TransitionError`

```go
// TransitionError — отказ на недопустимый переход.
type TransitionError struct {
    Transition TransitionID
    From, To   string
    Reason     string // какое предусловие нарушено
    Hint       string // что сделать вместо этого
}

func (e *TransitionError) Error() string       // краткое: "переход X из A в B невозможен: <Reason>"
func (e *TransitionError) Explanation() string // развёрнутое для пользователя: состояние, причина, подсказка
func IsTransitionError(err error) (*TransitionError, bool)
```

Пример объяснения:
```
Недопустимый переход: to_execution (planning → execution).
Причина: план не утверждён.
Сейчас: этап planning, шаг 1.
Что сделать: сначала утвердите план (/approve), затем повторно переходите к реализации.
```

### 4. Методы `Machine` переписываются поверх таблицы

Единый внутренний `apply(id TransitionID) error`:
1. находим переход в `validTransitions`;
2. проверяем `From` против текущего этапа (пропуск этапа → `TransitionError`);
3. запускаем `Guard` — если вернул ошибку, превращаем в `TransitionError{Reason, Hint}`;
4. `Apply` + `persist`.

Публичный API (совместимо, но строже):
- `Begin(goal)` → `TrBegin` (allowed из любого, включая done).
- `ApprovePlan()` → `TrApprovePlan` — **новый** метод.
- `NextStage()` → роутинг по этапу: из `planning` → `TrToExecution`, из `execution` →
  `TrToValidation`, из `validation` → **запрещено** (нужен `Accept`) — это и делает
  «нельзя финал без валидации».
- `Accept()` → `TrFinalize` — **новый** метод (финализация с пометкой `Validated=true`).
- `Rework(reason)` → `TrRework`.
- `Pause()`/`Resume()` — как есть (флаги, не stage-переходы).

### 5. Точки входа

- Ядро [`agent/task.go`](task.go): добавить `ApproveTask()` и `AcceptTask()` обёртки
  (аналогично `BeginTask`); при желании пробрасывать `TransitionError` до `Say()`.
- CLI [`handleTaskCmd`](cmd/cli/main.go:401): команды `/approve` (утвердить план) и
  `/accept` (финализация, теперь вызывает `Accept()`), `next` из validation отклоняется.
  Вывод: `TransitionError.Explanation()` вместо сырой ошибки — структурированный отказ.
- Web [`handleTask`](cmd/web/main.go:591): команды `approve`, `accept`; при ошибке —
  JSON `{transition, reason, hint, error}` → toast с объяснением.
- README REPL-таблица команд и веб-панель задачи — дополнить (`/approve`, описание guard).

### 6. SystemBlock — прозрачность для ассистента

Добавить в [`SystemBlock()`](feature/task/state.go:234) строки о `PlanApproved` и
`Validated`, чтобы модель «знала», что не может перескочить неутверждённый план или
непройденную валидацию (явный учёт ограничений в рассуждении).

## Тесты

`feature/task/transitions_test.go` + обновление `state_test.go`:
- **Таблица корректна**: `From`/`To` — валидные этапы или `""`; нет дублей; у
  `to_execution`/`finalize` обязательные guard'ы; переход из/в неизвестный этап невозможен.
- **Реализация без плана** (`TestPlanningRequiresApproval`): `Begin` → `NextStage` →
  `IsTransitionError` c `Hint` про `approve_plan`; после `ApprovePlan` → переход ок.
- **Финал без валидации** (`TestNoFinalWithoutValidation`): `NextStage` из `validation`
  отклонён; только `Accept()` → `done` и `Validated=true`.
- **Пропуск этапа**: прямой запрос недопустимого `From` (например, попытка
  `to_execution` из `execution`) → отказ.
- **Пауза блокирует все stage-переходы**: на паузе `NextStage`, `ApprovePlan`, `Accept`,
  `Rework` возвращают паузный `TransitionError`; `Resume` → продолжение с тем же `Step`
  (`TestPauseResumeKeepsStepAndExpected` остаётся, дополняется).
- **Explanation** содержит: id перехода, `from→to`, причину, подсказку.
- Обновить `TestFullLifecycle`: третий `NextStage` (validation→done) заменить на `Accept`.

## Критерии приёмки («Результат» из задания)

- У задачи есть допустимые состояния и явная таблица разрешённых переходов с guard'ами.
- Нельзя перескочить этап: реализация требует утверждённого плана, финал — валидации.
- Попытка недопустимого перехода → структурированный отказ с объяснением и подсказкой.
- Продолжение после паузы корректно (шаг/ожидание сохраняются), все переходы на паузе
  заблокированы.
- Покрыто тестами (таблица, guard'ы, отказ, пауза/резюм).

## Порядок реализации

1. `feature/task/transitions.go` — `TransitionID`, `Transition`, таблица `validTransitions`.
2. `state.go` — флаги `PlanApproved`/`Validated`, `TransitionError`, методы через `apply`, новые `ApprovePlan`/`Accept`.
3. `store.go` — DDL + сериализация новых полей.
4. `SystemBlock` — строки о плане/валидации.
5. Тесты: `transitions_test.go`, обновление `state_test.go`.
6. Ядро `agent/task.go` — `ApproveTask`/`AcceptTask`.
7. CLI `/approve`, `/accept`, вывод `Explanation()`; web `approve`/`accept` + JSON-отказ.
8. README + `make test` / `go vet ./...`.
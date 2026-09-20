# План: инварианты и ограничения состояния ассистента

## Цель

Научить ассистента работать **в рамках заданных инвариантов** (архитектура, принятые
технические решения, ограничения стека, бизнес-правила) и **отказываться** предлагать
решения, которые их нарушают. Инварианты должны:

- храниться **отдельно от диалога** (персистентное хранилище, переживает `/reset` и `/newtask`);
- явно учитываться в рассуждениях (инжектироваться в каждый запрос как обязательный system-блок);
- вызывать **структурированный отказ** при конфликте запроса с инвариантом.

## Предпосылки из существующего кода

Изученная архитектура (`agent/agent.go`, `feature/profile`, `feature/task`,
`feature/memory`, `cmd/cli/main.go`):

- Сборка запроса в [`prepareMessages()`](../agent.go:348): порядок
  **[профиль] → [task FSM] → [long] → [working] → история → ввод**. Инварианты — жёсткие
  правила, поэтому их блок должен идти **самым первым** (выше профиля и задачи).
- Существующие feature-пакеты следуют единому паттерну: интерфейс `Store` +
  in-memory реализация (для тестов/CLI) + SQLite-реализация (персистентность), плюс
  `SystemBlock()`. Лучший референс — [`feature/profile`](../feature/profile): структура
  `Invariant` ↔ `Profile`, `Store` ↔ `Store`, `SystemBlock()` ↔ `SystemBlock()`.
- Конфиг ([`config.go`](../config.go)) добавляет путь к БД через `json:"..."` поле.
- CLI ([`cmd/cli/main.go`](../cmd/cli/main.go)) добавляет флаги `--...` и REPL-команды `/...`.

## Новый feature-пакет: `agent/feature/invariants/`

### `invariant.go` — модель правила

```go
// Severity — строгость инварианта.
const (
    SeverityHard = "hard" // нарушение → обязательный отказ без ответа LLM
    SeveritySoft = "soft" // нарушение → предупреждение, но ответ допустим
)

// Category — типы инвариантов (из примера задания).
const (
    CatArchitecture = "архитектура"
    CatDecision     = "техническое решение"
    CatStack        = "стек"
    CatBusiness     = "бизнес-правило"
)

type Invariant struct {
    ID       string   `json:"id"`
    Title    string   `json:"title"`                // краткое имя (для вывода)
    Category string   `json:"category"`             // из категорий выше
    Text     string   `json:"text"`                 // формулировка правила
    Keywords []string `json:"keywords,omitempty"`   // эвристики для детекции конфликта
    Severity string   `json:"severity"`             // hard | soft
    Enabled  bool     `json:"enabled"`
}

func (i Invariant) Empty() bool
func (i Invariant) Hard() bool                       // Severity == SeverityHard && Enabled
```

### `store.go` — отдельное хранилище (отдельно от диалога)

Паттерн копирует [`feature/profile/store.go`](../feature/profile/store.go):

```go
type Store interface {
    Get(id string) (*Invariant, error)
    Set(i Invariant) error
    List() ([]Invariant, error)          // Enabled
    All() ([]Invariant, error)           // все, включая disabled
    Delete(id string) error
    Reset() error
    Close() error
}
```

- `InMemoryStore` — для тестов и CLI без диска.
- `SQLiteStore` — таблица `invariants`, файл из конфига (`invariants_file`). Переживает
  `/reset` и `/newtask` (не попадает в short/working, не удаляется при сбросе сессии).

### `manager.go` — рассуждение и отказ

```go
type Manager struct { store Store }

// SystemBlock рендерит обязательный system-блок со ВСЕМИ активными инвариантами.
// Инструкция ассистенту: правила НЕ обсуждаются и НЕ могут быть нарушены; при
// конфликте — отказаться и объяснить, какой инвариант и почему нарушается.
func (m *Manager) SystemBlock() string

// Active возвращает активные (Enabled) инварианты.
func (m *Manager) Active() []Invariant

// Add / Update / Delete / List / All / Close — делегируют в store.

// CheckConflict — эвристическая детекция конфликта запроса с инвариантами
// (по Keywords, case-insensitive). Возвращает найденные конфликты.
func (m *Manager) CheckConflict(input string) []Conflict

// Conflict — результат проверки: инвариант + причина + готовый текст отказа.
type Conflict struct {
    Invariant Invariant
    Reason    string // что именно в запросе противоречит правилу
}

// RefusalText формирует структурированное объяснение отказа.
// Например: «Не могу выполнить запрос: он нарушает инвариант [стек]
// "Только Dart/Flutter". Запрос предлагает <причина>. Предложите решение,
// не выходящее за рамки инварианта.»
func (m *Manager) RefusalText(c Conflict) string
```

**Двухуровневая защита:**

1. **Детерминированный pre-check** в [`Say()`](../agent.go:279): перед вызовом LLM
   `CheckConflict(input)`. Если есть **hard**-конфликт — сразу возвращаем
   `Reply{Text: RefusalText(...)}` **без обращения к LLM** (экономия токенов,
   полностью тестируемо). Soft-конфликт → предупреждение в событие, но ответ уходит.
2. **System-блок** на каждом запросе: даже если конфликт не пойман эвристикой,
   модель сама обязана отказаться от нарушения (покрывает семантически сложные случаи).

## Интеграция в ядро

### [`agent/config.go`](../config.go)

- Поле: `InvariantsFile string \`json:"invariants_file"\``.
- В `DefaultConfig()`: `InvariantsFile: "agent_invariants.db"`.

### [`agent/config.json`](../config.json)

- Добавить `"invariants_file": "agent_invariants.db"`.

### [`agent/agent.go`](../agent.go)

- Поле структуры `Agent`: `invariants *invariants.Manager`.
- В `New()`: если `cfg.InvariantsFile != ""` — открыть `invariants.NewSQLiteStore` и создать
  `Manager`; если store пуст — заполнить **предзаданными инвариантами-примерами**
  (архитектура / решение / стек / бизнес). Это даёт «работает из коробки».
- Геттеры `Invariants() *invariants.Manager`.
- В `prepareMessages()`: **первым** (до профиля) инжектировать `invariants.SystemBlock()`.
- В `Say()`: до `ChatResult` вызвать `CheckConflict(input)`; при hard-конфликте вернуть
  `Reply{Text: RefusalText, Events: [сообщение об отказе]}` без вызова LLM.

### [`agent/cmd/cli/main.go`](../cmd/cli/main.go)

- Флаг `--check-invariants`: прогнать демо-сценарий «запрос конфликтует с инвариантом»
  и показать текст отказа (проверка поведения при конфликте).
- REPL-команды: `/invariants` (список активных), `/invariant add <категория>|<title>|<text>`,
  `/invariant rm <id>`, `/invariant show <id>`.

### [`agent/web/main.go`](../cmd/web/main.go) — реализовано

- `GET /invariants` (список), `POST /invariants` (add), `POST /invariants/delete` (удалить по id).
- Блок «🛡️ Инварианты» в правой панели чата (`web/index.html`): список правил +
  форма добавления + удаление; обновляется при загрузке и после каждого хода.

## Тесты

- `feature/invariants/invariant_test.go` — рендер `SystemBlock`, severity/hard-логика,
  детекция конфликта по keywords.
- `feature/invariants/store_test.go` — InMemory + SQLite CRUD, сохранение между сессиями.
- `feature/invariants/refusal_test.go` — **конфликт запроса и инварианта**: hard →
  отказ с объяснением (какой инвариант, почему); soft → предупреждение без отказа;
  отсутствие конфликта → нет отказа.
- `agent/agent_test.go`-стиль интеграционный тест: `Say()` с конфликтующим вводом
  возвращает `Reply.Text == RefusalText` и **не вызывает LLM** (fake-клиент, счётчик вызовов).

## Критерии приёмки («Результат» из задания)

- Инварианты лежат в отдельном персистентном хранилище, не смешиваются с историей диалога.
- Каждый запрос получает system-блок с инвариантами (явный учёт в рассуждении).
- При hard-конфликте ассистент отказывается и объясняет отказ (какой инвариант + причина).
- Есть тесты, доказывающие поведение при конфликте и формат объяснения отказа.
- Работает из коробки с предзаданными примерами (архитектура/стек/бизнес-правило).

## Порядок реализации

1. `feature/invariants/invariant.go` — модель + категории/severity + шаблоны примеров.
2. `feature/invariants/store.go` — Store, InMemory, SQLite.
3. `feature/invariants/manager.go` — SystemBlock, CheckConflict, RefusalText.
4. Тесты пакета invariants (модель/хранилище/отказ).
5. `config.go` + `config.json` — `invariants_file`.
6. `agent.go` — инициализация Manager, seed примеров, инжекция блока, pre-check в `Say()`.
7. Интеграционный тест «отказ без вызова LLM».
8. CLI: `/invariants`, `/invariant`, флаг `--check-invariants`.
9. README-раздел + запуск тестов (`make test`).
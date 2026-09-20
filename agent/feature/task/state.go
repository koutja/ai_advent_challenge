// Package task — формализованное состояние задачи как конечный автомат.
//
// Отдельный feature-пакет, отвечающий за жизненный цикл одной активной задачи:
// этап (planning → execution → validation → done), текущий шаг внутри этапа и
// ожидаемое действие. Поддерживает паузу на любом этапе и продолжение без
// повторных объяснений: состояние (цель, этап, шаг, ожидаемое действие, итоги)
// сериализуется в компактный system-блок, который агент инжектирует в каждый
// запрос к LLM.
//
// Код ядра работает с интерфейсом Machine, поэтому среда исполнения не зависит
// от конкретного хранилища (in-memory для тестов, SQLite для персистентности).
package task

import (
	"errors"
	"fmt"
	"strings"
)

// Названия этапов конечного автомата.
const (
	StagePlanning   = "planning"
	StageExecution  = "execution"
	StageValidation = "validation"
	StageDone       = "done"
)

// StageOrder — канонический порядок этапов (для валидации переходов и UI).
var StageOrder = []string{StagePlanning, StageExecution, StageValidation, StageDone}

// Названия этапов на человекочитаемом русском (для SystemBlock/отладки).
var stageLabels = map[string]string{
	StagePlanning:   "планирование",
	StageExecution:  "исполнение",
	StageValidation: "валидация",
	StageDone:       "выполнено",
}

// stageIndex возвращает позицию этапа в StageOrder (-1 — неизвестный).
func stageIndex(stage string) int {
	for i, s := range StageOrder {
		if s == stage {
			return i
		}
	}
	return -1
}

// StageLabel возвращает человекочитаемое название этапа (для UI/логов).
// Неизвестный этап возвращается как есть.
func StageLabel(stage string) string {
	if l, ok := stageLabels[stage]; ok {
		return l
	}
	return stage
}

// State — снапшот состояния задачи для отображения, хранения и инжекции в промпт.
type State struct {
	Goal         string   // цель задачи (ставится при Begin)
	Stage        string   // текущий этап: planning | execution | validation | done
	Step         int      // номер текущего шага внутри этапа (1-based)
	Expected     string   // ожидаемое действие на текущем шаге
	Paused       bool     // признак паузы
	PlanApproved bool     // true — план утверждён (guard для перехода в исполнение)
	Validated    bool     // true — валидация пройдена (guard для финала)
	Log          []string // краткие итоги выполненных шагов (компактный контекст для resume)
}

// Done сообщает, завершена ли задача.
func (s State) Done() bool { return s.Stage == StageDone }

// IsActive сообщает, есть ли активная (начатая, но не завершённая) задача.
func (s State) IsActive() bool { return s.Stage != "" && s.Stage != StageDone }

// Machine — конечный автомат состояния задачи.
type Machine interface {
	// Begin открывает новую задачу с целью (переход в planning, шаг 1).
	Begin(goal string) error
	// SetExpected задаёт ожидаемое действие на текущем шаге.
	SetExpected(action string) error
	// Advance отмечает текущий шаг выполненным (итог в Log) и переходит к
	// следующему шагу того же этапа. Когда этап исчерпан, переход на следующий
	// этап делается явно через NextStage.
	Advance(summary string) error
	// NextStage переводит на следующий этап: planning→execution→validation.
	// Из validation к финалу идёт только через Accept (финал требует валидации).
	NextStage() error
	// ApprovePlan утверждает план на этапе планирования (guard для NextStage→execution).
	ApprovePlan() error
	// Accept финализирует задачу из validation→done (прохождение валидации).
	Accept() error
	// Rework возвращает из validation обратно в execution (шаг не принят).
	Rework(reason string) error
	// Pause приостанавливает задачу на текущем этапе (кроме done).
	Pause() error
	// Resume продолжает задачу с того же шага (сохраняет Step/Expected).
	Resume() error
	// Snapshot возвращает копию текущего состояния.
	Snapshot() State
	// SystemBlock собирает компактный system-блок состояния для инжекции в запрос.
	SystemBlock() string
	// Reset очищает состояние (новая задача/сессия).
	Reset() error
}

// machine — реализация Machine. Инкапсулирует текущее состояние и хранилище,
// в которое каждый мутирующий метод пишет снапшот.
type machine struct {
	st    State
	store Store
}

// New создаёт автомат поверх хранилища. Если в хранилище уже лежит состояние
// (например, после перезапуска), оно восстанавливается.
func New(store Store) (Machine, error) {
	if store == nil {
		return nil, errors.New("хранилище состояния задачи не задано (store)")
	}
	st, err := store.Load()
	if err != nil {
		return nil, err
	}
	return &machine{st: st, store: store}, nil
}

// persist сохраняет текущее состояние в хранилище.
func (m *machine) persist() error {
	return m.store.Save(m.st)
}

// apply — единый маршрутизатор переходов через таблицу validTransitions:
// проверяет исходный этап, запускает Guard (предусловия) и применяет Apply.
// Недопустимый переход/нарушенное предусловие возвращают структурированный
// *TransitionError с объяснением и подсказкой.
func (m *machine) apply(id TransitionID) error {
	tr := findTransition(id)
	if tr == nil {
		return fmt.Errorf("неизвестный переход %q", id)
	}
	if tr.From != "" && tr.From != m.st.Stage {
		return &TransitionError{
			Transition: id, From: tr.From, To: tr.To,
			Reason: fmt.Sprintf("переход возможен только из этапа %q, сейчас %q", tr.From, m.st.Stage),
			Hint:   "используйте переход, разрешённый для текущего этапа",
		}
	}
	if tr.Guard != nil {
		if err := tr.Guard(m.st); err != nil {
			if te, ok := IsTransitionError(err); ok {
				// Дозаполняем поля перехода, чтобы отказ был самодостаточным.
				te.Transition = id
				if te.From == "" {
					te.From = tr.From
				}
				if te.To == "" {
					te.To = tr.To
				}
				return te
			}
			return err
		}
	}
	tr.Apply(&m.st)
	return m.persist()
}

// Begin открывает новую задачу. Допустимо из любого состояния (в т.ч. done —
// начать следующую задачу); сбрасывает лог, шаг и флаги плана/валидации.
func (m *machine) Begin(goal string) error {
	if strings.TrimSpace(goal) == "" {
		return errors.New("цель задачи пустая")
	}
	m.st.Goal = strings.TrimSpace(goal)
	return m.apply(TrBegin)
}

// SetExpected задаёт ожидаемое действие текущего шага.
func (m *machine) SetExpected(action string) error {
	if !m.st.IsActive() {
		return errors.New("нет активной задачи (сначала Begin)")
	}
	m.st.Expected = action
	return m.persist()
}

// Advance завершает текущий шаг, кладёт его итог в Log и переходит к следующему
// шагу того же этапа. Запрещён в done и на паузе.
func (m *machine) Advance(summary string) error {
	if m.st.Done() {
		return errors.New("задача уже выполнена (done)")
	}
	if m.st.Paused {
		return errors.New("задача на паузе: сначала Resume")
	}
	if summary = strings.TrimSpace(summary); summary != "" {
		m.st.Log = append(m.st.Log, fmt.Sprintf("%s шаг %d: %s", stageLabels[m.st.Stage], m.st.Step, summary))
	}
	m.st.Step++
	m.st.Expected = ""
	return m.persist()
}

// NextStage переводит на следующий этап. Роутинг по текущему этапу:
//
//	planning → execution (guard: план утверждён);
//	execution → validation;
//	validation → финал запрещён — только через Accept (валидация обязательна).
func (m *machine) NextStage() error {
	switch m.st.Stage {
	case StagePlanning:
		return m.apply(TrToExecution)
	case StageExecution:
		return m.apply(TrToValidation)
	case StageValidation:
		return &TransitionError{
			Transition: TrFinalize, From: StageValidation, To: StageDone,
			Reason: "переход к финалу требует прохождения валидации",
			Hint:   "сначала примите задачу после валидации (Accept / /accept) — финал без валидации запрещён",
		}
	default:
		return &TransitionError{
			From:   m.st.Stage,
			Reason: "нельзя перейти на следующий этап из текущего состояния",
			Hint:   "проверьте этап задачи или начните новую (Begin)",
		}
	}
}

// ApprovePlan утверждает план на этапе планирования. Без этого перейти в
// исполнение нельзя (guardPlanApproved): «реализация — только после утверждённого плана».
func (m *machine) ApprovePlan() error { return m.apply(TrApprovePlan) }

// Accept финализирует задачу из validation в done, помечая валидацию пройденной.
// Единственный путь к done: «финал — только после валидации».
func (m *machine) Accept() error { return m.apply(TrFinalize) }

// Rework возвращает задачу из validation обратно в execution (переработка),
// сбрасывая флаг валидации.
func (m *machine) Rework(reason string) error {
	if reason = strings.TrimSpace(reason); reason != "" {
		m.st.Log = append(m.st.Log, "возврат на доработку: "+reason)
	}
	return m.apply(TrRework)
}

// Pause приостанавливает задачу на любом этапе, кроме терминального done.
// Текущий Step и Expected сохраняются.
func (m *machine) Pause() error {
	if m.st.Done() {
		return errors.New("нельзя поставить на паузу выполненную задачу (done)")
	}
	if m.st.Paused {
		return errors.New("задача уже на паузе")
	}
	m.st.Paused = true
	return m.persist()
}

// Resume продолжает задачу с того же шага. Step и Expected не сбрасываются,
// поэтому продолжение идёт без повторных объяснений.
func (m *machine) Resume() error {
	if !m.st.Paused {
		return errors.New("задача не на паузе")
	}
	m.st.Paused = false
	return m.persist()
}

// Snapshot возвращает копию текущего состояния.
func (m *machine) Snapshot() State {
	return m.st
}

// Reset очищает состояние задачи (новая задача/сессия).
func (m *machine) Reset() error {
	m.st = State{}
	return m.store.Clear()
}

// SystemBlock собирает компактный system-блок состояния для инжекции в запрос.
// Несёт цель, этап, текущий шаг, ожидаемое действие и итоги из Log, чтобы
// возобновление шло сразу с нужного места — без повторного объяснения задачи.
// Пустой, если активной задачи нет.
func (m *machine) SystemBlock() string {
	st := m.st
	if !st.IsActive() {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Состояние текущей задачи:\n")
	sb.WriteString("- Цель: " + st.Goal + "\n")
	sb.WriteString("- Этап: " + stageLabels[st.Stage] + " (" + st.Stage + ")\n")
	sb.WriteString(fmt.Sprintf("- Текущий шаг: %d\n", st.Step))
	if st.Paused {
		sb.WriteString("- Статус: ПАУЗА\n")
	} else {
		sb.WriteString("- Статус: в работе\n")
	}
	sb.WriteString(fmt.Sprintf("- План утверждён: %s\n", yesNo(st.PlanApproved)))
	if st.Stage == StageValidation || st.Done() {
		sb.WriteString(fmt.Sprintf("- Валидация пройдена: %s\n", yesNo(st.Validated)))
	}
	sb.WriteString("- Переходы контролируются: реализация требует утверждённого плана, финал — валидации.\n")
	if st.Expected != "" {
		sb.WriteString("- Ожидаемое действие: " + st.Expected + "\n")
	}
	if len(st.Log) > 0 {
		sb.WriteString("Итоги выполненных шагов:\n")
		for _, line := range st.Log {
			sb.WriteString("- " + line + "\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// yesNo рендерит булево значение для SystemBlock.
func yesNo(b bool) string {
	if b {
		return "да"
	}
	return "нет"
}

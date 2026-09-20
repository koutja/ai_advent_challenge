package task

import (
	"errors"
	"fmt"
	"strings"
)

// TransitionID — идентификатор перехода между состояниями задачи.
type TransitionID string

// Идентификаторы всех разрешённых переходов.
const (
	// TrBegin — начать новую задачу (из любого состояния, включая done).
	TrBegin TransitionID = "begin"
	// TrApprovePlan — утвердить план (planning → planning, помечает PlanApproved).
	TrApprovePlan TransitionID = "approve_plan"
	// TrToExecution — планирование → исполнение (требует утверждённого плана).
	TrToExecution TransitionID = "to_execution"
	// TrToValidation — исполнение → валидация.
	TrToValidation TransitionID = "to_validation"
	// TrFinalize — валидация → done (финализация после прохождения валидации).
	TrFinalize TransitionID = "finalize"
	// TrRework — валидация → исполнение (переработка).
	TrRework TransitionID = "rework"
)

// Transition — один разрешённый переход from → to с предусловием (Guard) и
// мутацией состояния (Apply). Является единым источником истины о том, какие
// переходы допустимы и при каких условиях: ассистент не может «перепрыгнуть» этап.
type Transition struct {
	ID    TransitionID
	From  string               // "" = любое состояние
	To    string               // целевое состояние (этап)
	Guard func(st State) error // предусловие; nil — без ограничений
	Apply func(st *State)      // мутация состояния после успешного прохождения Guard
}

// Guard-хелперы: возвращают *TransitionError (поля From/To заполняет apply()).

// guardActive — переходить можно только при активной задаче.
func guardActive(st State) error {
	if !st.IsActive() {
		return &TransitionError{Reason: "нет активной задачи", Hint: "сначала начните новую задачу (Begin / /begin)"}
	}
	return nil
}

// guardNotPaused — переходы запрещены, пока задача на паузе.
func guardNotPaused(st State) error {
	if st.Paused {
		return &TransitionError{Reason: "задача на паузе", Hint: "сначала продолжайте задачу (Resume / /resume)"}
	}
	return nil
}

// guardNotDone — нельзя переходить из завершённого состояния.
func guardNotDone(st State) error {
	if st.Done() {
		return &TransitionError{Reason: "задача уже выполнена (done)", Hint: "начните новую задачу (Begin / /begin)"}
	}
	return nil
}

// guardPlanApproved — реализация допустима только после утверждённого плана.
func guardPlanApproved(st State) error {
	if !st.PlanApproved {
		return &TransitionError{Reason: "план не утверждён",
			Hint: "сначала утвердите план (ApprovePlan / /approve) — реализация до утверждённого плана запрещена"}
	}
	return nil
}

// combine последовательно применяет несколько guard'ов (первая ошибка побеждает).
func combine(gs ...func(State) error) func(State) error {
	return func(st State) error {
		for _, g := range gs {
			if err := g(st); err != nil {
				return err
			}
		}
		return nil
	}
}

// validTransitions — таблица допустимых переходов. Единый источник истины,
// используемый машиной состояний (machine.apply) и тестами.
var validTransitions = []Transition{
	{
		ID: TrBegin, From: "", To: StagePlanning,
		Apply: func(st *State) {
			st.Stage = StagePlanning
			st.Step = 1
			st.Expected = ""
			st.Paused = false
			st.PlanApproved = false
			st.Validated = false
			st.Log = nil
		},
	},
	{
		ID: TrApprovePlan, From: StagePlanning, To: StagePlanning,
		Guard: combine(guardActive, guardNotPaused),
		Apply: func(st *State) {
			st.PlanApproved = true
			st.Log = append(st.Log, "план утверждён")
		},
	},
	{
		ID: TrToExecution, From: StagePlanning, To: StageExecution,
		Guard: combine(guardActive, guardNotPaused, guardPlanApproved),
		Apply: func(st *State) {
			st.Stage = StageExecution
			st.Step = 1
			st.Expected = ""
		},
	},
	{
		ID: TrToValidation, From: StageExecution, To: StageValidation,
		Guard: combine(guardActive, guardNotPaused, guardNotDone),
		Apply: func(st *State) {
			st.Stage = StageValidation
			st.Step = 1
			st.Expected = ""
		},
	},
	{
		ID: TrFinalize, From: StageValidation, To: StageDone,
		Guard: combine(guardActive, guardNotPaused, guardNotDone),
		Apply: func(st *State) {
			st.Validated = true
			st.Stage = StageDone
			st.Expected = ""
			st.Log = append(st.Log, "задача принята (валидация пройдена)")
		},
	},
	{
		ID: TrRework, From: StageValidation, To: StageExecution,
		Guard: combine(guardActive, guardNotPaused),
		Apply: func(st *State) {
			st.Validated = false
			st.Stage = StageExecution
			st.Step = 1
			st.Expected = ""
		},
	},
}

// TransitionError — структурированный отказ на недопустимый переход.
// Используется и CLI, и web, и ядром для понятного объяснения «почему нельзя»
// и подсказки «что сделать вместо этого».
type TransitionError struct {
	Transition TransitionID
	From       string // этап, из которого переход невозможен ("" — любое)
	To         string // целевой этап ("" — неизвестен)
	Reason     string // нарушенное предусловие
	Hint       string // что сделать вместо этого
}

// Error — краткое описание (для логов).
func (e *TransitionError) Error() string {
	return fmt.Sprintf("недопустимый переход %s (%s → %s): %s", e.Transition, anyLabel(e.From), anyLabel(e.To), e.Reason)
}

// Explanation — развёрнутое объяснение для пользователя/ассистента.
func (e *TransitionError) Explanation() string {
	var sb strings.Builder
	sb.WriteString("Недопустимый переход: ")
	fmt.Fprintf(&sb, "%s (%s → %s).\n", e.Transition, anyLabel(e.From), anyLabel(e.To))
	sb.WriteString("Причина: " + e.Reason + "\n")
	if e.Hint != "" {
		sb.WriteString("Что сделать: " + e.Hint + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// IsTransitionError извлекает *TransitionError из ошибки.
func IsTransitionError(err error) (*TransitionError, bool) {
	var te *TransitionError
	if errors.As(err, &te) {
		return te, true
	}
	return nil, false
}

// anyLabel отображает пустой этап как «любое состояние».
func anyLabel(s string) string {
	if s == "" {
		return "любое состояние"
	}
	return s
}

// findTransition возвращает переход по ID (nil, если неизвестен).
func findTransition(id TransitionID) *Transition {
	for i := range validTransitions {
		if validTransitions[i].ID == id {
			return &validTransitions[i]
		}
	}
	return nil
}

// ValidTransitions возвращает копию таблицы допустимых переходов (для UI/тестов).
func ValidTransitions() []Transition {
	return append([]Transition(nil), validTransitions...)
}

// Stages возвращает копию канонического списка этапов.
func Stages() []string { return append([]string(nil), StageOrder...) }

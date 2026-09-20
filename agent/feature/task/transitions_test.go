package task

import (
	"strings"
	"testing"
)

// validStage сообщает, является ли строка допустимым этапом.
func validStage(s string) bool {
	for _, st := range StageOrder {
		if s == st {
			return true
		}
	}
	return false
}

// TestTransitionTableIsWellFormed проверяет декларативную таблицу переходов:
// From/To — валидные этапы (или пустое «любое»), нет дублей ID, обязательные
// guard'ы у to_execution (план) и finalize (валидация).
func TestTransitionTableIsWellFormed(t *testing.T) {
	seen := map[TransitionID]bool{}
	for _, tr := range ValidTransitions() {
		if seen[tr.ID] {
			t.Fatalf("дублирующийся ID перехода %q", tr.ID)
		}
		seen[tr.ID] = true

		if tr.From != "" && !validStage(tr.From) {
			t.Fatalf("переход %s: неизвестный From %q", tr.ID, tr.From)
		}
		if !validStage(tr.To) {
			t.Fatalf("переход %s: неизвестный To %q", tr.ID, tr.To)
		}
		if tr.Apply == nil {
			t.Fatalf("переход %s: нет мутации Apply", tr.ID)
		}
	}

	// Guard-условия обязательны для переходов с требованиями к плану/валидации.
	for _, tr := range ValidTransitions() {
		switch tr.ID {
		case TrToExecution:
			if tr.Guard == nil {
				t.Fatal("to_execution должен иметь guard (план утверждён)")
			}
		case TrFinalize:
			if tr.Guard == nil {
				t.Fatal("finalize должен иметь guard (активность/пауза/done)")
			}
		}
	}
}

// TestSkipStageRejected — попытка «перепрыгнуть» этап (не из planning) отклоняется.
func TestSkipStageRejected(t *testing.T) {
	m := newTestMachine(t)
	reachExecution(t, m) // теперь execution

	// Попытка перейти в исполнение, находясь уже в исполнении, — недопустимый From.
	err := m.(*machine).apply(TrToExecution)
	if err == nil {
		t.Fatal("to_execution из execution должен быть отклонён")
	}
	te, ok := IsTransitionError(err)
	if !ok {
		t.Fatalf("ожидали *TransitionError, получили %T", err)
	}
	if te.From != StagePlanning {
		t.Fatalf("отказ должен указывать источник From=%q, получили %q", StagePlanning, te.From)
	}
}

// TestPausedBlocksAllStageTransitions — на паузе все stage-переходы заблокированы.
// Каждый переход проверяется на том этапе, из которого он допустим.
func TestPausedBlocksAllStageTransitions(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(Machine)
		act     func(Machine) error
	}{
		{"NextStage(planning→execution)", func(m Machine) { must(t, m.Begin("g")) }, func(m Machine) error { return m.NextStage() }},
		{"ApprovePlan", func(m Machine) { must(t, m.Begin("g")) }, func(m Machine) error { return m.ApprovePlan() }},
		{"Accept", func(m Machine) { reachValidation(t, m) }, func(m Machine) error { return m.Accept() }},
		{"Rework", func(m Machine) { reachValidation(t, m) }, func(m Machine) error { return m.Rework("x") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newTestMachine(t)
			c.prepare(m)
			must(t, m.Pause())
			err := c.act(m)
			if err == nil {
				t.Fatal("переход на паузе должен быть запрещён")
			}
			if !strings.Contains(err.Error(), "паузе") {
				t.Fatalf("отказ должен упоминать паузу, получили: %v", err)
			}
		})
	}
}

// TestResumePreservesPositionAndUnblocks — после Resume продолжение идёт с того же
// шага, и переходы снова доступны.
func TestResumePreservesPositionAndUnblocks(t *testing.T) {
	m := newTestMachine(t)
	reachExecution(t, m)
	must(t, m.SetExpected("реализовать модуль"))

	must(t, m.Pause())
	must(t, m.Resume())

	st := m.Snapshot()
	if st.Stage != StageExecution || st.Step != 1 || st.Expected != "реализовать модуль" {
		t.Fatalf("после Resume состояние потеряно: %+v", st)
	}
	// После Resume переход к валидации снова возможен.
	must(t, m.NextStage())
	if st := m.Snapshot(); st.Stage != StageValidation {
		t.Fatalf("после Resume NextStage не сработал: %s", st.Stage)
	}
}

// TestTransitionErrorExplanation — объяснение отказа содержит id перехода,
// from→to, причину и подсказку.
func TestTransitionErrorExplanation(t *testing.T) {
	m := newTestMachine(t)
	must(t, m.Begin("g"))
	err := m.NextStage() // план не утверждён
	te, ok := IsTransitionError(err)
	if !ok {
		t.Fatalf("ожидали *TransitionError, получили %T", err)
	}
	exp := te.Explanation()
	for _, want := range []string{"Недопустимый переход", string(TrToExecution), StagePlanning, StageExecution, "план не утверждён", "Что сделать"} {
		if !strings.Contains(exp, want) {
			t.Errorf("Explanation должен содержать %q:\n%s", want, exp)
		}
	}
}

// TestAcceptOnlyFromValidation — Accept допустим только на этапе валидации.
func TestAcceptOnlyFromValidation(t *testing.T) {
	m := newTestMachine(t)
	reachExecution(t, m) // execution
	if err := m.Accept(); err == nil {
		t.Fatal("Accept не из validation должен быть отклонён")
	}
	if st := m.Snapshot(); st.Done() {
		t.Fatal("Accept не из validation не должен завершать задачу")
	}
}

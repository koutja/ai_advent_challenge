package task

import (
	"strings"
	"testing"
)

func newTestMachine(t *testing.T) Machine {
	t.Helper()
	m, err := New(NewInMemoryStore())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
}

// reachExecution переводит задачу в исполнение через утверждение плана.
func reachExecution(t *testing.T, m Machine) {
	t.Helper()
	must(t, m.Begin("g"))
	must(t, m.ApprovePlan())
	must(t, m.NextStage())
}

// reachValidation переводит задачу на этап валидации.
func reachValidation(t *testing.T, m Machine) {
	t.Helper()
	reachExecution(t, m)
	must(t, m.NextStage())
}

// finishTask завершает задачу через Accept (единственный путь к done).
func finishTask(t *testing.T, m Machine) {
	t.Helper()
	reachValidation(t, m)
	must(t, m.Accept())
}

func TestFullLifecycle(t *testing.T) {
	m := newTestMachine(t)

	must(t, m.Begin("собрать отчёт"))
	st := m.Snapshot()
	if st.Stage != StagePlanning || st.Step != 1 {
		t.Fatalf("после Begin ожидали planning/1, получили %s/%d", st.Stage, st.Step)
	}

	must(t, m.SetExpected("составить план шагов"))
	must(t, m.Advance("план готов"))
	if st = m.Snapshot(); st.Step != 2 {
		t.Fatalf("Advance должен увеличить шаг до 2, получили %d", st.Step)
	}

	// Реализация только после утверждённого плана.
	must(t, m.ApprovePlan())
	must(t, m.NextStage())
	if st = m.Snapshot(); st.Stage != StageExecution || st.Step != 1 {
		t.Fatalf("ожидали execution/1, получили %s/%d", st.Stage, st.Step)
	}

	must(t, m.NextStage())
	if st = m.Snapshot(); st.Stage != StageValidation || st.Step != 1 {
		t.Fatalf("ожидали validation/1, получили %s/%d", st.Stage, st.Step)
	}

	// Финал — только через Accept (валидация).
	must(t, m.Accept())
	if st = m.Snapshot(); !st.Done() || !st.Validated {
		t.Fatalf("ожидали done+validated, получили %s validated=%v", st.Stage, st.Validated)
	}
}

func TestPlanningRequiresApproval(t *testing.T) {
	m := newTestMachine(t)
	must(t, m.Begin("g"))
	err := m.NextStage()
	if err == nil {
		t.Fatal("реализация без утверждённого плана должна быть отклонена")
	}
	te, ok := IsTransitionError(err)
	if !ok {
		t.Fatalf("ожидали *TransitionError, получили %T", err)
	}
	if te.Transition != TrToExecution || !strings.Contains(te.Hint, "approve") {
		t.Fatalf("отказ не описывает причину/подсказку: %+v", te)
	}
	if !strings.Contains(te.Explanation(), "план не утверждён") {
		t.Fatalf("Explanation не объясняет причину:\n%s", te.Explanation())
	}

	// После утверждения плана переход разрешён.
	must(t, m.ApprovePlan())
	must(t, m.NextStage())
	if st := m.Snapshot(); st.Stage != StageExecution {
		t.Fatalf("после утверждения плана ожидали execution, получили %s", st.Stage)
	}
}

func TestNoFinalWithoutValidation(t *testing.T) {
	m := newTestMachine(t)
	reachValidation(t, m)

	// Попытка «перескочить» финал через NextStage отклоняется.
	err := m.NextStage()
	if err == nil {
		t.Fatal("финал через NextStage должен быть запрещён")
	}
	if _, ok := IsTransitionError(err); !ok {
		t.Fatalf("ожидали *TransitionError, получили %T", err)
	}
	if st := m.Snapshot(); st.Stage != StageValidation || st.Done() {
		t.Fatalf("состояние не должно измениться: %+v", st)
	}

	// Только Accept завершает задачу и помечает валидацию.
	must(t, m.Accept())
	if st := m.Snapshot(); !st.Done() || !st.Validated {
		t.Fatalf("Accept должен привести к done+validated, получили %+v", st)
	}
}

func TestPauseResumeKeepsStepAndExpected(t *testing.T) {
	m := newTestMachine(t)
	reachExecution(t, m)
	must(t, m.SetExpected("реализовать модуль"))

	must(t, m.Pause())
	st := m.Snapshot()
	if !st.Paused {
		t.Fatal("Pause не установил флаг")
	}
	if st.Step != 1 || st.Expected != "реализовать модуль" {
		t.Fatalf("пауза изменила Step/Expected: step=%d expected=%q", st.Step, st.Expected)
	}

	must(t, m.Resume())
	st = m.Snapshot()
	if st.Paused {
		t.Fatal("Resume не снял флаг паузы")
	}
	if st.Stage != StageExecution || st.Step != 1 || st.Expected != "реализовать модуль" {
		t.Fatalf("resume потерял состояние: stage=%s step=%d expected=%q", st.Stage, st.Step, st.Expected)
	}
}

func TestPauseAllowedOnEveryNonDoneStage(t *testing.T) {
	scenarios := []struct {
		name     string
		prepare  func(m Machine)
		expectOK bool
	}{
		{"planning", func(m Machine) { must(t, m.Begin("g")) }, true},
		{"execution", func(m Machine) { reachExecution(t, m) }, true},
		{"validation", func(m Machine) { reachValidation(t, m) }, true},
		{"done", func(m Machine) { finishTask(t, m) }, false},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			m := newTestMachine(t)
			sc.prepare(m)
			err := m.Pause()
			if sc.expectOK && err != nil {
				t.Fatalf("Pause на %s не разрешён: %v", sc.name, err)
			}
			if !sc.expectOK && err == nil {
				t.Fatalf("Pause на %s должен был завершиться ошибкой", sc.name)
			}
		})
	}
}

func TestIllegalTransitions(t *testing.T) {
	t.Run("advance in done", func(t *testing.T) {
		m := newTestMachine(t)
		finishTask(t, m)
		if err := m.Advance("x"); err == nil {
			t.Fatal("Advance из done должен падать")
		}
	})
	t.Run("nextstage in done", func(t *testing.T) {
		m := newTestMachine(t)
		finishTask(t, m)
		if err := m.NextStage(); err == nil {
			t.Fatal("NextStage из done должен падать")
		}
	})
	t.Run("rework not from validation", func(t *testing.T) {
		m := newTestMachine(t)
		must(t, m.Begin("g"))
		if err := m.Rework("причина"); err == nil {
			t.Fatal("Rework не из validation должен падать")
		}
	})
	t.Run("actions while paused blocked", func(t *testing.T) {
		m := newTestMachine(t)
		must(t, m.Begin("g"))
		must(t, m.Pause())
		if err := m.Advance("x"); err == nil {
			t.Fatal("Advance на паузе должен падать")
		}
		if err := m.NextStage(); err == nil {
			t.Fatal("NextStage на паузе должен падать")
		}
		if err := m.ApprovePlan(); err == nil {
			t.Fatal("ApprovePlan на паузе должен падать")
		}
	})
}

func TestReworkReturnsToExecution(t *testing.T) {
	m := newTestMachine(t)
	reachValidation(t, m)
	must(t, m.Rework("тест упал"))
	st := m.Snapshot()
	if st.Stage != StageExecution || st.Step != 1 {
		t.Fatalf("Rework ожидали execution/1, получили %s/%d", st.Stage, st.Step)
	}
	if st.Validated {
		t.Fatal("Rework должен сбросить флаг валидации")
	}
	if len(st.Log) == 0 || !strings.Contains(st.Log[len(st.Log)-1], "возврат на доработку") {
		t.Fatalf("лог должен содержать причину возврата, лог=%v", st.Log)
	}
}

func TestSystemBlockCarriesResumeContext(t *testing.T) {
	m := newTestMachine(t)
	must(t, m.Begin("рефакторинг модуля"))
	must(t, m.SetExpected("выделить интерфейс"))
	must(t, m.Advance("интерфейс выделен"))
	must(t, m.ApprovePlan())
	must(t, m.NextStage())
	must(t, m.Pause())

	block := m.SystemBlock()
	for _, want := range []string{"рефакторинг модуля", "execution", "ПАУЗА", "интерфейс выделен", "План утверждён: да"} {
		if !strings.Contains(block, want) {
			t.Errorf("SystemBlock должен содержать %q, блок:\n%s", want, block)
		}
	}
}

func TestSystemBlockEmptyWithoutActiveTask(t *testing.T) {
	m := newTestMachine(t)
	if got := m.SystemBlock(); got != "" {
		t.Fatalf("без задачи SystemBlock должен быть пустым, получили %q", got)
	}
}

func TestBeginClearsPreviousState(t *testing.T) {
	m := newTestMachine(t)
	must(t, m.Begin("первая"))
	must(t, m.ApprovePlan())
	must(t, m.NextStage())
	must(t, m.Advance("x"))
	must(t, m.Begin("вторая"))
	st := m.Snapshot()
	if st.Goal != "вторая" || st.Stage != StagePlanning || st.Step != 1 {
		t.Fatalf("Begin не сбросил состояние: %+v", st)
	}
	if st.PlanApproved {
		t.Fatal("Begin должен сбросить флаг утверждённого плана")
	}
	if len(st.Log) != 0 {
		t.Fatalf("Begin должен очистить лог, получили %v", st.Log)
	}
}

func TestResetClearsState(t *testing.T) {
	m := newTestMachine(t)
	must(t, m.Begin("g"))
	must(t, m.Pause())
	must(t, m.Reset())
	st := m.Snapshot()
	if st.IsActive() || st.Paused {
		t.Fatalf("Reset должен очистить состояние, получили %+v", st)
	}
}

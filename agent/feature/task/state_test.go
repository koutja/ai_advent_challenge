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

	must(t, m.NextStage())
	if st = m.Snapshot(); st.Stage != StageExecution || st.Step != 1 {
		t.Fatalf("ожидали execution/1, получили %s/%d", st.Stage, st.Step)
	}

	must(t, m.NextStage())
	if st = m.Snapshot(); st.Stage != StageValidation || st.Step != 1 {
		t.Fatalf("ожидали validation/1, получили %s/%d", st.Stage, st.Step)
	}

	must(t, m.NextStage())
	if st = m.Snapshot(); !st.Done() {
		t.Fatalf("ожидали done, получили %s", st.Stage)
	}
}

func TestPauseResumeKeepsStepAndExpected(t *testing.T) {
	m := newTestMachine(t)
	must(t, m.Begin("задача"))
	must(t, m.SetExpected("написать код"))
	must(t, m.Advance("шаг выполнен"))
	// теперь execution? Нет — Advance остаётся в planning. Перейдём в execution.
	must(t, m.NextStage())
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
		{"execution", func(m Machine) {
			must(t, m.Begin("g"))
			must(t, m.NextStage())
		}, true},
		{"validation", func(m Machine) {
			must(t, m.Begin("g"))
			must(t, m.NextStage())
			must(t, m.NextStage())
		}, true},
		{"done", func(m Machine) {
			must(t, m.Begin("g"))
			must(t, m.NextStage())
			must(t, m.NextStage())
			must(t, m.NextStage())
		}, false},
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
		must(t, m.Begin("g"))
		must(t, m.NextStage())
		must(t, m.NextStage())
		must(t, m.NextStage())
		if err := m.Advance("x"); err == nil {
			t.Fatal("Advance из done должен падать")
		}
	})
	t.Run("nextstage in done", func(t *testing.T) {
		m := newTestMachine(t)
		must(t, m.Begin("g"))
		must(t, m.NextStage())
		must(t, m.NextStage())
		must(t, m.NextStage())
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
	})
}

func TestReworkReturnsToExecution(t *testing.T) {
	m := newTestMachine(t)
	must(t, m.Begin("g"))
	must(t, m.NextStage())
	must(t, m.NextStage()) // validation
	must(t, m.Rework("тест упал"))
	st := m.Snapshot()
	if st.Stage != StageExecution || st.Step != 1 {
		t.Fatalf("Rework ожидали execution/1, получили %s/%d", st.Stage, st.Step)
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
	must(t, m.NextStage())
	must(t, m.Pause())

	block := m.SystemBlock()
	for _, want := range []string{"рефакторинг модуля", "execution", "ПАУЗА", "интерфейс выделен"} {
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
	must(t, m.NextStage())
	must(t, m.Advance("x"))
	must(t, m.Begin("вторая"))
	st := m.Snapshot()
	if st.Goal != "вторая" || st.Stage != StagePlanning || st.Step != 1 {
		t.Fatalf("Begin не сбросил состояние: %+v", st)
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

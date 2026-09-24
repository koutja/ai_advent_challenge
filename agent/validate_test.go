package agent

import (
	"strings"
	"testing"
)

func TestParseVerdict(t *testing.T) {
	cases := []struct {
		in   string
		want Verdict
		ok   bool
	}{
		{"ВЕРДИКТ: OK\nвыглядит хорошо", VerdictOK, true},
		{"ВЕРДИКТ: REWORK\nесть недочёты", VerdictRework, true},
		{"непонятный ответ", "", false},
	}
	for _, c := range cases {
		got, _, err := parseVerdict(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Fatalf("parseVerdict(%q) = %v, %v; want %v ok", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Fatalf("parseVerdict(%q) не должен был пройти", c.in)
		}
	}
}

// TestValidateWorkNoClient — без LLM-клиента валидация невозможна.
func TestValidateWorkNoClient(t *testing.T) {
	a := newAgentWithTask(t) // client == nil
	reachExecutionInAgent(t, a)
	if err := a.tasks.SetPlan("1. сделать"); err != nil {
		t.Fatal(err)
	}
	// Валидация вызывается на этапе execution тут не строго, но клиента нет —
	// ждём ошибку про клиент (Work пуст тоже дал бы ошибку, проверяем приоритет).
	_, _, err := a.ValidateWork()
	if err == nil {
		t.Fatal("ожидали ошибку")
	}
}

// TestValidateWorkNoResult — без результата исполнения валидация отклоняется.
func TestValidateWorkNoResult(t *testing.T) {
	a := newAgentWithTask(t)
	a.client = mustUnroutableClient(t)
	reachExecutionInAgent(t, a)
	if err := a.tasks.SetPlan("1. сделать"); err != nil {
		t.Fatal(err)
	}
	_, _, err := a.ValidateWork() // Work пуст
	if err == nil || !strings.Contains(err.Error(), "Work") {
		t.Fatalf("ожидали ошибку про отсутствие результата, получили: %v", err)
	}
}

// TestReworkRequiresValidation — доработка возможна только на этапе валидации.
func TestReworkRequiresValidation(t *testing.T) {
	a := newAgentWithTask(t)
	reachExecutionInAgent(t, a) // execution, не validation
	if err := a.ReworkWithLLM("тест"); err == nil || !strings.Contains(err.Error(), "валидац") {
		t.Fatalf("ожидали ошибку про этап валидации, получили: %v", err)
	}
}

// TestAppendWorkAccumulates — результаты шагов накапливаются в State.Work.
func TestAppendWorkAccumulates(t *testing.T) {
	a := newAgentWithTask(t)
	if err := a.BeginTask("g"); err != nil {
		t.Fatal(err)
	}
	if err := a.tasks.AppendWork("Шаг 1: код"); err != nil {
		t.Fatal(err)
	}
	if err := a.tasks.AppendWork("Шаг 2: тесты"); err != nil {
		t.Fatal(err)
	}
	st := a.TaskState()
	if !strings.Contains(st.Work, "Шаг 1: код") || !strings.Contains(st.Work, "Шаг 2: тесты") {
		t.Fatalf("Work не накопил результаты: %q", st.Work)
	}
}

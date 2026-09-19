package agent

import (
	"strings"
	"testing"

	"aichallenge/llm"

	"agent/feature/memory"
	"agent/feature/task"
)

func newAgentWithTask(t *testing.T) *Agent {
	t.Helper()
	m, err := task.New(task.NewInMemoryStore())
	if err != nil {
		t.Fatalf("task.New: %v", err)
	}
	return &Agent{memory: memory.NewLayeredRAM(), cfg: DefaultConfig(), tasks: m}
}

// TestTaskSystemBlockInjectedInRequest проверяет, что состояние задачи
// инжектируется в запрос: на каждом ходу агент видит цель, этап и ожидаемое
// действие — продолжение без повторных объяснений.
func TestTaskSystemBlockInjectedInRequest(t *testing.T) {
	a := newAgentWithTask(t)
	if err := a.BeginTask("рефакторинг модуля"); err != nil {
		t.Fatalf("BeginTask: %v", err)
	}
	if err := a.tasks.SetExpected("выделить интерфейс"); err != nil {
		t.Fatalf("SetExpected: %v", err)
	}

	msgs := a.prepareMessages(nil, "привет")
	var joined string
	for _, msg := range msgs {
		joined += msg.Content + "\n"
	}
	for _, want := range []string{"рефакторинг модуля", "planning", "выделить интерфейс"} {
		if !strings.Contains(joined, want) {
			t.Errorf("запрос должен содержать %q, получили:\n%s", want, joined)
		}
	}
}

// TestTaskDisabledNoInjection проверяет, что без настроенного task_file состояние
// не инжектируется.
func TestTaskDisabledNoInjection(t *testing.T) {
	a := &Agent{memory: memory.NewLayeredRAM(), cfg: DefaultConfig()} // tasks nil
	if a.TaskEnabled() {
		t.Fatal("TaskEnabled должен быть false при tasks == nil")
	}
	msgs := a.prepareMessages(nil, "привет")
	for _, msg := range msgs {
		if msg.Role == "system" && strings.Contains(msg.Content, "Состояние текущей задачи") {
			t.Fatalf("не должно быть инжекции состояния без активной задачи: %+v", msg)
		}
	}
}

// TestBeginTaskClearsOnResetContext проверяет, что ResetContext сбрасывает
// состояние задачи вместе с памятью.
func TestBeginTaskClearsOnResetContext(t *testing.T) {
	a := newAgentWithTask(t)
	if err := a.BeginTask("задача"); err != nil {
		t.Fatalf("BeginTask: %v", err)
	}
	_ = a.tasks.Pause()
	if err := a.ResetContext(); err != nil {
		t.Fatalf("ResetContext: %v", err)
	}
	if st := a.TaskState(); st.IsActive() || st.Paused {
		t.Fatalf("ResetContext должен очистить состояние задачи, получили %+v", st)
	}
}

// TestBeginTaskErrorsWhenDisabled — команда /begin без настроенного task_file.
func TestBeginTaskErrorsWhenDisabled(t *testing.T) {
	a := &Agent{memory: memory.NewLayeredRAM(), cfg: DefaultConfig()}
	if err := a.BeginTask("задача"); err == nil {
		t.Fatal("BeginTask без tasks должен вернуть ошибку")
	}
}

// ensure llm import used (prepareMessages returns []llm.Message).
var _ = llm.Message{}

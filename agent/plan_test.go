package agent

import (
	"strings"
	"testing"

	"aichallenge/llm"

	"agent/feature/memory"
	"agent/feature/task"
)

// TestGeneratePlanNoActiveTask — генерация плана без активной задачи отклоняется.
func TestGeneratePlanNoActiveTask(t *testing.T) {
	a := newAgentWithTask(t)
	err := a.GeneratePlan()
	if err == nil || !strings.Contains(err.Error(), "нет активной задачи") {
		t.Fatalf("ожидали ошибку про отсутствие задачи, получили: %v", err)
	}
}

// TestGeneratePlanNoClient — если LLM-клиент не настроен, план не генерируется,
// но задача остаётся активной (можно вернуться к началу/перегенерировать позже).
func TestGeneratePlanNoClient(t *testing.T) {
	a := newAgentWithTask(t)
	if err := a.BeginTask("собрать отчёт"); err != nil {
		t.Fatal(err)
	}
	// a.client == nil (новый агент из хелпера) — клиент не настроен.
	err := a.GeneratePlan()
	if err == nil || !strings.Contains(err.Error(), "клиент LLM не настроен") {
		t.Fatalf("ожидали ошибку про клиент, получили: %v", err)
	}
	if st := a.TaskState(); st.Stage != task.StagePlanning {
		t.Fatalf("ошибка генерации не должна менять состояние задачи: %+v", st)
	}
}

// TestSetPlanStoresAndClearsExpectedHint — SetPlan сохраняет черновик плана и
// подсказывает следующий шаг (утверждение плана).
func TestSetPlanStoresAndClearsExpectedHint(t *testing.T) {
	a := newAgentWithTask(t)
	if err := a.BeginTask("задача"); err != nil {
		t.Fatal(err)
	}
	if err := a.tasks.SetPlan("1. изучить\n2. реализовать"); err != nil {
		t.Fatal(err)
	}
	st := a.TaskState()
	if !strings.Contains(st.Plan, "изучить") {
		t.Fatalf("план не сохранён: %q", st.Plan)
	}
	if !strings.Contains(st.Expected, "утвердите план") {
		t.Fatalf("ожидалась подсказка про утверждение плана, получили: %q", st.Expected)
	}
	// План попадает в system-блок (ассистент видит его в рассуждении).
	if block := a.tasks.SystemBlock(); !strings.Contains(block, "изучить") {
		t.Fatalf("план не попал в SystemBlock:\n%s", block)
	}
}

// TestPlanGenerationFull — полный сценарий авто-генерации с настоящим (но
// недоступным) клиентом: BeginTask → GeneratePlan пытается вызвать LLM и
// возвращает ошибку сети, НО не ломает машину состояний (возможен /approve → /next).
func TestPlanGenerationFailureKeepsMachineUsable(t *testing.T) {
	client, err := llm.NewWithConfig("http://127.0.0.1:1", "k", "m") // недоступный адрес
	if err != nil {
		t.Fatal(err)
	}
	m, _ := task.New(task.NewInMemoryStore())
	a := &Agent{memory: memory.NewLayeredRAM(), cfg: DefaultConfig(), tasks: m, client: client}
	if err := a.BeginTask("g"); err != nil {
		t.Fatal(err)
	}
	if err := a.GeneratePlan(); err == nil {
		t.Fatal("GeneratePlan с недоступным LLM должен вернуть ошибку")
	}
	// Машина по-прежнему рабочая: план можно утвердить и перейти к реализации.
	if err := a.ApproveTask(); err != nil {
		t.Fatalf("после сбоя генерации ApproveTask сломан: %v", err)
	}
	if err := a.tasks.NextStage(); err != nil {
		t.Fatalf("после сбоя генерации NextStage сломан: %v", err)
	}
}

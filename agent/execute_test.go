package agent

import (
	"strings"
	"testing"

	"agent/feature/task"
)

// reachExecutionInAgent доводит задачу до этапа исполнения.
func reachExecutionInAgent(t *testing.T, a *Agent) {
	t.Helper()
	if err := a.BeginTask("g"); err != nil {
		t.Fatal(err)
	}
	if err := a.ApproveTask(); err != nil {
		t.Fatal(err)
	}
	if err := a.tasks.NextStage(); err != nil {
		t.Fatal(err)
	}
}

// TestExecuteStepRequiresExecution — выполнение шага возможно только на этапе execution.
func TestExecuteStepRequiresExecution(t *testing.T) {
	a := newAgentWithTask(t)
	if err := a.BeginTask("g"); err != nil {
		t.Fatal(err)
	}
	_, err := a.ExecuteCurrentStep() // этап planning
	if err == nil || !strings.Contains(err.Error(), "execution") {
		t.Fatalf("ожидали ошибку про этап execution, получили: %v", err)
	}
}

// TestExecuteStepNoClient — без LLM-клиента шаг не выполняется, но состояние не ломается.
func TestExecuteStepNoClient(t *testing.T) {
	a := newAgentWithTask(t) // client == nil
	reachExecutionInAgent(t, a)
	_, err := a.ExecuteCurrentStep()
	if err == nil || !strings.Contains(err.Error(), "клиент LLM") {
		t.Fatalf("ожидали ошибку про клиент, получили: %v", err)
	}
	if st := a.TaskState(); st.Stage != task.StageExecution {
		t.Fatalf("ошибка не должна менять этап: %+v", st)
	}
}

// TestExecuteStepAdvancesPlan — при рабочем клиенте (недоступный адрес) генерация
// шага падает с сетевой ошибкой, но машина остаётся на execution и доступна /run.
func TestExecuteStepKeepsMachineUsableOnFailure(t *testing.T) {
	a := newAgentWithTask(t)
	client := mustUnroutableClient(t)
	a.client = client
	reachExecutionInAgent(t, a)
	if err := a.tasks.SetPlan("1. изучить\n2. реализовать"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ExecuteCurrentStep(); err == nil {
		t.Fatal("ExecuteCurrentStep с недоступным LLM должен вернуть ошибку")
	}
	if st := a.TaskState(); st.Stage != task.StageExecution {
		t.Fatalf("сбой выполнения не должен менять этап: %+v", st)
	}
}

// TestPlanStepsSplitsLines — PlanSteps разбивает план на непустые строки.
func TestPlanStepsSplitsLines(t *testing.T) {
	got := task.PlanSteps("1. изучить\n\n2. реализовать\n   ")
	if len(got) != 2 || got[0] != "1. изучить" || got[1] != "2. реализовать" {
		t.Fatalf("PlanSteps некорректен: %#v", got)
	}
	if n := len(task.PlanSteps("")); n != 0 {
		t.Fatalf("PlanSteps пустого плана = %d, want 0", n)
	}
}

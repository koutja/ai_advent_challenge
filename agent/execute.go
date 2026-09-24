package agent

import (
	"errors"
	"fmt"

	"aichallenge/llm"

	"agent/feature/task"
)

// ExecuteCurrentStep выполняет текущий шаг плана реализации через LLM и по успеху
// автоматически переходит к следующему шагу (Advance). Возвращает текст результата
// шага для вывода в чат. Требует активной задачи на этапе execution.
func (a *Agent) ExecuteCurrentStep() (string, error) {
	if a.tasks == nil {
		return "", errTaskDisabled
	}
	st := a.tasks.Snapshot()
	if !st.IsActive() {
		return "", errors.New("нет активной задачи")
	}
	if st.Stage != task.StageExecution {
		return "", fmt.Errorf("выполнение шага возможно только на этапе execution, сейчас %q", st.Stage)
	}
	if st.Paused {
		return "", errors.New("задача на паузе: сначала Resume")
	}
	if a.client == nil {
		return "", errors.New("клиент LLM не настроен — не могу выполнить шаг")
	}

	steps := task.PlanSteps(st.Plan)
	stepDesc := "завершающий шаг плана"
	if st.Step <= len(steps) {
		stepDesc = steps[st.Step-1]
	}

	prompt := fmt.Sprintf(
		"Ты исполняешь план реализации задачи.\nЦель: %s\nПлан (всего шагов: %d).\nВыполни шаг %d из %d: %s\n"+
			"Дай конкретный результат этого шага (код / текст / действие), без лишних вступлений.",
		st.Goal, len(steps), st.Step, len(steps), stepDesc)

	res, err := a.client.ChatResult(
		[]llm.Message{
			{Role: "system", Content: "Ты — исполнитель плана. Выполняешь только текущий шаг, отвечаешь результатом шага."},
			{Role: "user", Content: prompt},
		},
		&llm.Options{MaxTokens: a.cfg.MaxTokens},
	)
	if err != nil {
		return "", fmt.Errorf("не удалось выполнить шаг: %w", err)
	}

	// Сохраняем результат шага как материал для валидации/сводки.
	if err := a.tasks.AppendWork(fmt.Sprintf("Шаг %d: %s", st.Step, res.Text)); err != nil {
		return "", err
	}
	// Шаг выполнен — переход к следующему шагу плана.
	if err := a.tasks.Advance(fmt.Sprintf("шаг %d: выполнено", st.Step)); err != nil {
		return "", err
	}
	return res.Text, nil
}

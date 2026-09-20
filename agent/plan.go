package agent

import (
	"errors"
	"fmt"

	"aichallenge/llm"
)

// GeneratePlan вызывает LLM, чтобы составить план реализации для активной задачи,
// и сохраняет результат в состояние задачи (State.Plan). Возвращает ошибку, если
// feature задачи выключен, активной задачи нет или LLM недоступен.
//
// Используется автоматически при старте задачи (/begin) и по явной команде /plan,
// чтобы ассистент не просто переключал этап FSM, а реально генерировал план.
func (a *Agent) GeneratePlan() error {
	if a.tasks == nil {
		return errTaskDisabled
	}
	st := a.tasks.Snapshot()
	if !st.IsActive() {
		return errors.New("нет активной задачи: сначала Begin (/begin <цель>)")
	}
	if a.client == nil {
		return errors.New("клиент LLM не настроен — не могу сгенерировать план")
	}

	prompt := "Составь план реализации для следующей цели задачи.\n" +
		"Ответ — нумерованный список шагов от 1 до N, каждый шаг одной строкой, без воды.\n" +
		"Цель: " + st.Goal

	res, err := a.client.ChatResult(
		[]llm.Message{
			{Role: "system", Content: "Ты — планировщик реализации. Выдаёшь только конкретный пошаговый план."},
			{Role: "user", Content: prompt},
		},
		&llm.Options{MaxTokens: a.cfg.MaxTokens},
	)
	if err != nil {
		return fmt.Errorf("не удалось сгенерировать план: %w", err)
	}

	if err := a.tasks.SetPlan(res.Text); err != nil {
		return err
	}
	return nil
}

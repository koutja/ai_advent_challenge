package agent

import (
	"errors"
	"fmt"
	"strings"

	"aichallenge/llm"

	"agent/feature/task"
)

// Verdict — результат валидации работы.
type Verdict string

const (
	// VerdictOK — работа соответствует цели и плану (можно финализировать).
	VerdictOK = Verdict("ok")
	// VerdictRework — нужна доработка.
	VerdictRework = Verdict("rework")
)

// ValidateWork вызывает LLM, чтобы проверить результат исполнения (State.Work)
// против цели и плана, и возвращает вердикт + краткую оценку. Требует активной
// задачи; обычно выполняется на этапе validation.
func (a *Agent) ValidateWork() (Verdict, string, error) {
	if a.tasks == nil {
		return "", "", errTaskDisabled
	}
	st := a.tasks.Snapshot()
	if !st.IsActive() {
		return "", "", errors.New("нет активной задачи")
	}
	if a.client == nil {
		return "", "", errors.New("клиент LLM не настроен — не могу проверить работу")
	}
	if st.Work == "" {
		return "", "", errors.New("нет результата исполнения (Work пуст) — выполните шаги плана")
	}

	prompt := fmt.Sprintf(
		"Проверь результат выполнения задачи.\nЦель: %s\nПлан:\n%s\n\nРезультат исполнения:\n%s\n\n"+
			"Дай вердикт строго одной строкой в формате: ВЕРДИКТ: OK или ВЕРДИКТ: REWORK, затем с новой строки краткая оценка.",
		st.Goal, orPlaceholder(st.Plan, "—"), st.Work)

	res, err := a.client.ChatResult(
		[]llm.Message{
			{Role: "system", Content: "Ты — валидатор. Оцениваешь, достиг ли результат цели и соответствует ли плану. Ответ начинается со строки «ВЕРДИКТ: OK» или «ВЕРДИКТ: REWORK»."},
			{Role: "user", Content: prompt},
		},
		&llm.Options{MaxTokens: a.cfg.MaxTokens},
	)
	if err != nil {
		return "", "", fmt.Errorf("не удалось проверить работу: %w", err)
	}
	return parseVerdict(res.Text)
}

// parseVerdict извлекает вердикт и оценку из ответа модели.
func parseVerdict(text string) (Verdict, string, error) {
	t := strings.TrimSpace(text)
	upper := strings.ToUpper(t)
	switch {
	case strings.Contains(upper, "ВЕРДИКТ: OK") || strings.HasPrefix(upper, "OK"):
		return VerdictOK, t, nil
	case strings.Contains(upper, "ВЕРДИКТ: REWORK") || strings.HasPrefix(upper, "REWORK"):
		return VerdictRework, t, nil
	default:
		return "", "", fmt.Errorf("не удалось распознать вердикт модели: %s", firstLine(text))
	}
}

// ReworkWithLLM выполняет доработку: спрашивает LLM, что исправить по причине
// и результату работы, сохраняет замечания в работу и переводит задачу обратно
// на этап исполнения (сбрасывая флаг валидации).
func (a *Agent) ReworkWithLLM(reason string) error {
	if a.tasks == nil {
		return errTaskDisabled
	}
	st := a.tasks.Snapshot()
	if !st.IsActive() {
		return errors.New("нет активной задачи")
	}
	if st.Stage != task.StageValidation {
		return fmt.Errorf("доработка возможна только на этапе валидации, сейчас %q", st.Stage)
	}
	if a.client == nil {
		return errors.New("клиент LLM не настроен — не могу выполнить доработку")
	}
	if reason == "" {
		reason = "результат не принят валидацией"
	}

	prompt := fmt.Sprintf(
		"Задача требует доработки.\nЦель: %s\nПричина: %s\nРезультат исполнения:\n%s\n\n"+
			"Составь конкретные указания по исправлению (что поправить и как). Ответ списком.",
		st.Goal, reason, st.Work)
	res, err := a.client.ChatResult(
		[]llm.Message{
			{Role: "system", Content: "Ты — аналитик. Даёшь конкретные указания по исправлению результата."},
			{Role: "user", Content: prompt},
		},
		&llm.Options{MaxTokens: a.cfg.MaxTokens},
	)
	if err != nil {
		return fmt.Errorf("не удалось подготовить доработку: %w", err)
	}

	// Фиксируем замечания в работу (контекст для повторного исполнения).
	if err := a.tasks.AppendWork("Доработка по причине: " + reason + "\n" + res.Text); err != nil {
		return err
	}
	// Переход обратно на исполнение (сбрасывает Validated).
	return a.tasks.Rework(reason)
}

// SummarizeDone формирует финальную сводку по задаче (итог) через LLM.
// Вызывается на этапе done для вывода итогового сообщения в чат.
func (a *Agent) SummarizeDone() (string, error) {
	if a.tasks == nil {
		return "", errTaskDisabled
	}
	st := a.tasks.Snapshot()
	if !st.IsActive() && !st.Done() {
		return "", errors.New("нет завершённой задачи")
	}
	if a.client == nil {
		return "", errors.New("клиент LLM не настроен — не могу собрать сводку")
	}
	prompt := fmt.Sprintf(
		"Составь краткую финальную сводку по выполненной задаче.\nЦель: %s\nПлан:\n%s\nРезультат исполнения:\n%s",
		st.Goal, orPlaceholder(st.Plan, "—"), st.Work)
	res, err := a.client.ChatResult(
		[]llm.Message{
			{Role: "system", Content: "Ты — составитель итогов. Кратко и по делу: что сделано и результат."},
			{Role: "user", Content: prompt},
		},
		&llm.Options{MaxTokens: a.cfg.MaxTokens},
	)
	if err != nil {
		return "", fmt.Errorf("не удалось собрать сводку: %w", err)
	}
	return res.Text, nil
}

func orPlaceholder(s, ph string) string {
	if strings.TrimSpace(s) == "" {
		return ph
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 80 {
		return s[:80]
	}
	return s
}

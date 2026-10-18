package main

import (
	"fmt"
	"strings"

	"aichallenge/llm"
)

// explainPrompt — AI объясняет ответ и «ловушку» задачи.
const explainPrompt = `Ты — преподаватель количественной грамотности. Объясни пользователю, почему правильный ответ верный (и верен ли его ответ) и в чём ловушка/манипуляция в этой задаче. Опирайся ТОЛЬКО на предоставленную задачу и её объяснение — не выдумывай числа. Коротко, 3-6 предложений, на русском.`

// GenerateExplanation возвращает AI-объяснение по задаче и ответу пользователя;
// при сбое/отсутствии клиента — сохранённое в базе объяснение задачи (t.Explain).
func GenerateExplanation(client *llm.Client, t *Task, userAnswer string, correct bool) string {
	if client != nil {
		if exp, err := aiExplanation(client, t, userAnswer, correct); err == nil && strings.TrimSpace(exp) != "" {
			return strings.TrimSpace(exp)
		}
	}
	return t.Explain
}

// aiExplanation вызывает LLM для объяснения.
func aiExplanation(client *llm.Client, t *Task, userAnswer string, correct bool) (string, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Вопрос: %s\n", t.Question)
	fmt.Fprintf(&sb, "Правильный ответ: %s\n", fmtExpected(t))
	fmt.Fprintf(&sb, "Ответ пользователя: %q (верен: %v)\n", userAnswer, correct)
	fmt.Fprintf(&sb, "Решение/объяснение задачи: %s\n", t.Explain)
	msgs := []llm.Message{
		{Role: "system", Content: explainPrompt},
		{Role: "user", Content: sb.String()},
	}
	res, err := client.ChatResult(msgs, &llm.Options{MaxTokens: 250})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Text), nil
}

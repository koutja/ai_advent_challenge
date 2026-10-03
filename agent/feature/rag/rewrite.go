package rag

import (
	"strings"

	"aichallenge/llm"
)

// rewriteSystemPrompt — инструкция для переписывания запроса перед поиском.
const rewriteSystemPrompt = `Ты — помощник, улучшающий поисковые запросы к базе знаний.
Перепиши запрос пользователя для поиска по технической документации проекта
(Go-агент, README дней, планы, конфиги). Требования:
1. Сохрани исходный смысл вопроса.
2. Добавь 2–4 ключевых термина или синонима, которые могли бы встречаться в таких документах.
3. Верни ТОЛЬКО переписанный запрос одним предложением, без пояснений, кавычек и переносов.`

// RewriteQuery переписывает запрос через LLM перед поиском (query rewrite).
//
// Возвращает (переписанный запрос, true) или (оригинал, false) при любой ошибке
// (нет ключа/сети, пустой ответ, слишком длинный ответ) — поиск никогда не
// ломается из-за переписывания.
func RewriteQuery(client *llm.Client, query string, maxTokens int) (string, bool) {
	if client == nil || strings.TrimSpace(query) == "" {
		return query, false
	}
	out, err := client.Chat(
		[]llm.Message{
			{Role: "system", Content: rewriteSystemPrompt},
			{Role: "user", Content: query},
		},
		&llm.Options{MaxTokens: maxTokens},
	)
	if err != nil {
		return query, false
	}
	out = strings.TrimSpace(out)
	out = strings.Trim(out, `"'«»`)
	if out == "" || len(out) > 500 {
		return query, false
	}
	return out, true
}

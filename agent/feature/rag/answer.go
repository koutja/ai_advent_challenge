package rag

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aichallenge/llm"
)

// Answer — ответ LLM: текст + usage API + источники (для RAG-режима).
type Answer struct {
	Text    string
	Usage   *llm.Result
	Sources []Chunk // найденные чанки; nil — режим без RAG
	Engine  string  // "sidecar" | "keyword"; пусто — без RAG
}

// ragSystemPrompt — инструкция для RAG-режима (источники обязательны).
const ragSystemPrompt = `Ты — ассистент, отвечающий на вопрос строго по предоставленному контексту из документов проекта. Правила:
1. Отвечай на русском, кратко и по делу.
2. Опирайся ТОЛЬКО на контекст ниже. Если в контексте нет ответа — так прямо и скажи («в контексте этого нет»), не додумывай.
3. После каждого факта указывай источник в квадратных скобках: [1], [2] и т.д.`

// BuildContext собирает текст контекста из чанков с нумерацией [1]…[n]
// (source + секция в шапке), обрезая по maxChars. Чистая функция — тестируется.
func BuildContext(chunks []Chunk, maxChars int) string {
	var b strings.Builder
	for i, c := range chunks {
		src := c.Source
		if c.Section != "" && c.Section != "—" {
			src += fmt.Sprintf(" (секция: %s)", c.Section)
		}
		fmt.Fprintf(&b, "[%d] %s\n%s\n\n", i+1, src, strings.TrimSpace(c.Text))
		if b.Len() >= maxChars {
			break
		}
	}
	out := b.String()
	if len(out) > maxChars {
		out = out[:maxChars] + "\n… (контекст обрезан)"
	}
	return out
}

// chatWithRetry выполняет запрос к LLM с повторными попытками при транзиентных
// ошибках (rate limit / таймаут), чтобы прогон из 20 запросов не сыпался.
func chatWithRetry(client *llm.Client, msgs []llm.Message, opts *llm.Options, attempts int) (*llm.Result, error) {
	var lastErr error
	for i := 1; i <= attempts; i++ {
		res, err := client.ChatResult(msgs, opts)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if i < attempts {
			time.Sleep(time.Duration(i) * 2 * time.Second)
		}
	}
	return nil, lastErr
}

// AnswerPlain — обычный ответ без RAG: только вопрос к LLM.
func AnswerPlain(client *llm.Client, query string, maxTokens int) (*Answer, error) {
	res, err := chatWithRetry(client,
		[]llm.Message{{Role: "user", Content: query}},
		&llm.Options{MaxTokens: maxTokens}, 3)
	if err != nil {
		return nil, err
	}
	return &Answer{Text: res.Text, Usage: res}, nil
}

// AnswerRAG — «первый RAG-запрос»: ретрив top-k чанков → системный промпт
// «контекст с источниками» → вопрос → ответ LLM со ссылками [n].
func AnswerRAG(ctx context.Context, client *llm.Client, retriever *Retriever, query string, maxTokens int) (*Answer, error) {
	cfg := retriever.Config()

	result, err := retriever.Retrieve(ctx, query)
	if err != nil {
		return nil, err
	}
	contextText := BuildContext(result.Chunks, cfg.MaxContextChars)
	if strings.TrimSpace(contextText) == "" {
		return nil, fmt.Errorf("retrieve вернул пустой контекст (индекс пуст?)")
	}

	msgs := []llm.Message{
		{Role: "system", Content: ragSystemPrompt + "\n\nКонтекст:\n" + contextText},
		{Role: "user", Content: query},
	}
	res, err := chatWithRetry(client, msgs, &llm.Options{MaxTokens: maxTokens}, 3)
	if err != nil {
		return nil, err
	}
	return &Answer{
		Text:    res.Text,
		Usage:   res,
		Sources: result.Chunks,
		Engine:  result.Engine,
	}, nil
}

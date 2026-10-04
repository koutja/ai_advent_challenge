package rag

import (
	"context"
	"fmt"
	"strings"

	"aichallenge/llm"
)

// AnswerRAGWithHistory — RAG-ответ с учётом истории диалога (День 25: мини-чат).
//
// В отличие от AnswerRAGWithMode (stateless — только вопрос + контекст), здесь
// в запрос к LLM подмешивается история диалога: модель видит предыдущие реплики
// и может опираться на них. При этом каждый ход заново ищет контекст по текущему
// вопросу и отвечает в строгом формате Дня 24 (Ответ/Источники/Цитаты).
//
// Поток:
//  1. ретрив (режим filter — фильтр по min_score + heuristic-реранк);
//  2. жёсткий гейт «не знаю» (Kept==0 || top1 < unknown_below → отказ без LLM);
//  3. сборка сообщений: [system: строгий промпт + контекст] → история → вопрос;
//  4. LLM → ParseCitation + ValidateSources/ValidateQuotes + мягкий гейт.
//
// history — предыдущие реплики диалога (user/assistant); может быть пустой.
// extraSystem — дополнительные system-блоки (память задачи, инварианты, профиль),
// вставляемые ПЕРЕД RAG-контекстом (приоритет жёстких правил над контекстом).
func AnswerRAGWithHistory(
	ctx context.Context,
	client *llm.Client,
	retriever *Retriever,
	history []llm.Message,
	extraSystem []llm.Message,
	query string,
	maxTokens int,
) (*Answer, error) {
	cfg := retriever.Config()
	q := strings.TrimSpace(query)
	ans := &Answer{}

	// Ретрив: полный пайплайн (filter — продакшен-режим по умолчанию).
	result, err := retriever.Retrieve(ctx, q)
	if err != nil {
		return nil, err
	}

	// --- Жёсткий гейт «не знаю» (День 24) ---
	topScore := 0.0
	if len(result.Chunks) > 0 {
		topScore = result.Chunks[0].Score
	}
	if result.Stages.Kept == 0 || topScore < cfg.UnknownBelow {
		ans.Text = UnknownText
		ans.Unknown = true
		ans.Sources = result.Chunks
		ans.Engine = result.Engine
		ans.Stages = result.Stages
		return ans, nil
	}

	contextText := BuildContext(result.Chunks, cfg.MaxContextChars)
	if strings.TrimSpace(contextText) == "" {
		return nil, fmt.Errorf("retrieve вернул пустой контекст (индекс пуст?)")
	}

	// Сборка сообщений: extraSystem (память/инварианты) → RAG system (промпт+контекст)
	// → история диалога → текущий вопрос.
	system := ragSystemPrompt
	if cfg.CitationsRequired {
		system = citeSystemPrompt
	}
	msgs := make([]llm.Message, 0, len(extraSystem)+len(history)+2)
	msgs = append(msgs, extraSystem...)
	msgs = append(msgs, llm.Message{Role: "system", Content: system + "\n\nКонтекст:\n" + contextText})
	msgs = append(msgs, history...)
	msgs = append(msgs, llm.Message{Role: "user", Content: query})

	res, err := chatWithRetry(client, msgs, &llm.Options{MaxTokens: maxTokens}, 3)
	if err != nil {
		return nil, err
	}

	ans.Text = res.Text
	ans.Usage = res
	ans.Sources = result.Chunks
	ans.Engine = result.Engine
	ans.Stages = result.Stages

	// Разбор строгого формата + мягкий гейт «не знаю».
	if cfg.CitationsRequired {
		cit := ParseCitation(res.Text)
		ans.Citation = &cit
		ans.FormatOK = cit.FormatOK
		if !cit.FormatOK && looksUnknown(res.Text) {
			ans.Unknown = true
		}
	}
	return ans, nil
}

package agent

import (
	stdctx "context"
	"errors"
	"strings"

	"aichallenge/llm"

	"agent/feature/rag"
)

// supportSystemPrompt — системный промпт ассистента поддержки.
const supportSystemPrompt = `Ты — ассистент поддержки пользователей продукта «AI Advent Challenge агент». Отвечай на вопросы о продукте строго по предоставленному контексту (FAQ, документация). Учитывай данные тикета и пользователя, если они переданы. Если ответа в контексте нет — так и скажи и предложи обратиться в поддержку, не выдумывай. Отвечай на русском, кратко и по делу.`

// SupportAnswer — ответ ассистента поддержки (команда --support).
//
// Вопрос + контекст тикета (тикет/пользователь из JSON-CRM, собранный
// фронтендом через MCP-инструменты домена support) → RAG по FAQ/документации
// (индекс index_service) → строгий ответ с источниками. Ход не сохраняется
// в память (stateless).
func (a *Agent) SupportAnswer(question, ticketContext string) (*Reply, error) {
	if a.rag == nil {
		return nil, errors.New("RAG не настроен: добавьте секцию rag в config.json")
	}
	q := strings.TrimSpace(question)
	if q == "" {
		return nil, errors.New("пустой вопрос")
	}

	// System-блоки памяти/инвариантов/профиля/FSM + промпт поддержки +
	// контекст тикета — ПЕРЕД RAG-контекстом.
	extra := a.extraSystemBlocks()
	extra = append(extra, llm.Message{Role: "system", Content: supportSystemPrompt})
	if tc := strings.TrimSpace(ticketContext); tc != "" {
		extra = append(extra, llm.Message{Role: "system", Content: "Контекст тикета:\n" + tc})
	}

	ans, err := rag.AnswerRAGWithHistory(stdctx.Background(), a.client, a.rag, nil, extra, q, a.cfg.MaxTokens)
	if err != nil {
		return nil, err
	}

	// Статистика токенов — только если клиент доступен (в тестах/гейте «не знаю»
	// клиент может отсутствовать, тогда Stats остаётся nil).
	var st *TokenStats
	if a.client != nil {
		st = &TokenStats{Model: a.client.Model(), ContextWindow: a.cfg.ContextWindow}
		if ans.Usage != nil {
			if ans.Usage.PromptTokens >= 0 {
				st.RequestTokens = ans.Usage.PromptTokens
			}
			if ans.Usage.CompletionTokens >= 0 {
				st.ResponseTokens = ans.Usage.CompletionTokens
			}
			if ans.Usage.TotalTokens >= 0 {
				st.TotalTokens = ans.Usage.TotalTokens
			}
			if p, ok := a.prices[ans.Usage.Model]; ok {
				st.CostUSD = Cost(p, ans.Usage.PromptTokens, ans.Usage.CompletionTokens)
				st.CostKnown = true
			}
		}
	}

	return &Reply{
		Text:     ans.Text,
		Usage:    ans.Usage,
		Stats:    st,
		Sources:  ans.Sources,
		Engine:   ans.Engine,
		Unknown:  ans.Unknown,
		Citation: ans.Citation,
		FormatOK: ans.FormatOK,
	}, nil
}

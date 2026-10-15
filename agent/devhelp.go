package agent

import (
	stdctx "context"
	"errors"
	"strings"

	"aichallenge/llm"

	"agent/feature/rag"
)

// SayHelp — ответ ассистента разработчика на вопрос о проекте (команда /help).
//
// В отличие от SayChat, ход НЕ сохраняется в историю диалога (stateless):
// каждый /help — самостоятельный вопрос. В extraSystem (перед RAG-контекстом)
// добавляется git-контекст (ветка/статус/diff), собранный фронтендом через
// MCP-инструменты домена git. Ретрив идёт по индексу index_service
// (README, docs, код) — как в SayChat, со строгим форматом и источниками.
//
// gitContext — текстовая сводка git-состояния проекта (может быть пустой);
// попадает в системный промпт как «Git-контекст проекта».
func (a *Agent) SayHelp(query, gitContext string) (*Reply, error) {
	if a.rag == nil {
		return nil, errors.New("RAG не настроен: добавьте секцию rag в config.json")
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("пустой вопрос")
	}

	// System-блоки памяти/инвариантов/профиля/FSM + git-контекст — ПЕРЕД RAG-контекстом.
	extra := a.extraSystemBlocks()
	if gc := strings.TrimSpace(gitContext); gc != "" {
		extra = append(extra, llm.Message{Role: "system", Content: "Git-контекст проекта:\n" + gc})
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

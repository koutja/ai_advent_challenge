package agent

import (
	"strings"

	"aichallenge/llm"
)

// ContextManager — управление контекстом через сжатие истории (Этап 4).
//
// Принцип: последние KeepLast сообщений отправляются в запрос «как есть»,
// а более старые свёртываются в отдельную сводку (summary) через саму LLM.
// Сводка подставляется в начало запроса системным сообщением вместо полной
// старой истории — так экономятся токены и не теряется суть диалога.
type ContextManager struct {
	client         *llm.Client
	keepLast       int // сколько последних сообщений оставлять сырыми
	summarizeEvery int // сколько новых «старых» сообщений копить перед свёрткой
	summary        string
	summarized     int // сколько сообщений уже свёрнуто в summary (водяной знак)
}

// NewContextManager создаёт менеджер сжатия. client нужен для суммаризации.
func NewContextManager(client *llm.Client, keepLast, summarizeEvery int) *ContextManager {
	if keepLast <= 0 {
		keepLast = 10
	}
	if summarizeEvery <= 0 {
		summarizeEvery = 10
	}
	return &ContextManager{client: client, keepLast: keepLast, summarizeEvery: summarizeEvery}
}

// Build формирует сообщения запроса: сводка (если есть) + последние сообщения
// + новое сообщение пользователя. Память при этом не трогается — она хранит
// полную историю, сжатие происходит только на уровне запроса.
func (cm *ContextManager) Build(hist []llm.Message, input string) []llm.Message {
	if cm == nil {
		out := make([]llm.Message, 0, len(hist)+1)
		return append(out, hist...)
	}

	// Сколько сообщений должно быть свёрнуто (всё старше последних keepLast).
	oldN := len(hist) - cm.keepLast
	if oldN < 0 {
		oldN = 0
	}
	// Свёртываем новые «старые» сообщения батчами не реже summarizeEvery.
	if oldN > cm.summarized && oldN-cm.summarized >= cm.summarizeEvery {
		cm.summary = cm.summarize(cm.summary, hist[cm.summarized:oldN])
		cm.summarized = oldN
	}

	msgs := make([]llm.Message, 0, len(hist)-cm.summarized+2)
	if cm.summary != "" {
		msgs = append(msgs, llm.Message{
			Role:    "system",
			Content: "Сводка предыдущего диалога:\n" + cm.summary,
		})
	}
	msgs = append(msgs, hist[cm.summarized:]...)
	msgs = append(msgs, llm.Message{Role: "user", Content: input})
	return msgs
}

// Summary возвращает текущую сводку (полезно для отладки/сравнения).
func (cm *ContextManager) Summary() string {
	if cm == nil {
		return ""
	}
	return cm.summary
}

// summarize сворачивает новые сообщения в сводку через LLM. При ошибке
// возвращает прежнюю сводку, чтобы диалог не «потерялся».
func (cm *ContextManager) summarize(prev string, msgs []llm.Message) string {
	var sb strings.Builder
	if prev != "" {
		sb.WriteString("Предыдущая сводка:\n" + prev + "\n\n")
	}
	sb.WriteString("Новые сообщения диалога:\n")
	for _, m := range msgs {
		sb.WriteString(m.Role + ": " + m.Content + "\n")
	}

	prompt := []llm.Message{
		{Role: "system", Content: "Ты сжимаешь длинный диалог в краткую сводку ключевых фактов, решений и тем. Отвечай по-русски, коротко, сохраняя важные детали."},
		{Role: "user", Content: sb.String()},
	}
	out, err := cm.client.Chat(prompt, &llm.Options{MaxTokens: 200})
	if err != nil {
		return prev
	}
	return strings.TrimSpace(out)
}

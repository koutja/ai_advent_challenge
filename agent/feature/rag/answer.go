package rag

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aichallenge/llm"
)

// RAGMode — режим RAG-пайплайна (для сравнения качества в eval).
type RAGMode string

// Режимы пайплайна (День 23: реранкинг и фильтрация).
const (
	ModeBase   RAGMode = "base"   // поиск как есть, без этапа 2 и без rewrite
	ModeFilter RAGMode = "filter" // этап 2 (фильтр по MinScore + реранк), без rewrite
	ModeFull   RAGMode = "full"   // query rewrite + этап 2
)

// Answer — ответ LLM: текст + usage API + источники (для RAG-режима).
type Answer struct {
	Text           string
	Usage          *llm.Result
	Sources        []Chunk // найденные чанки; nil — режим без RAG
	Engine         string  // "sidecar" | "keyword"; пусто — без RAG
	Stages         PipelineStages
	Rewritten      bool   // запрос был переписан перед поиском
	RewrittenQuery string // переписанный запрос (если применялся)

	// --- День 24: цитаты и анти-галлюцинации ---
	Unknown  bool      // режим «не знаю»: контекст слабый, ответ без вызова LLM
	Citation *Citation // разобранные секции Ответ/Источники/Цитаты (nil — без строгого формата)
	FormatOK bool      // модель соблюла строгий формат (все 3 секции)
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
//
// Режим выбирается по конфигу: если rewrite.enabled — ModeFull, иначе ModeFilter.
func AnswerRAG(ctx context.Context, client *llm.Client, retriever *Retriever, query string, maxTokens int) (*Answer, error) {
	mode := ModeFilter
	if retriever.Config().Rewrite.Enabled {
		mode = ModeFull
	}
	return AnswerRAGWithMode(ctx, client, retriever, query, maxTokens, mode)
}

// AnswerRAGWithMode выполняет RAG-ответ в заданном режиме пайплайна
// (base | filter | full) — используется для сравнения качества в eval.
func AnswerRAGWithMode(ctx context.Context, client *llm.Client, retriever *Retriever, query string, maxTokens int, mode RAGMode) (*Answer, error) {
	cfg := retriever.Config()
	q := strings.TrimSpace(query)
	ans := &Answer{}

	// Query rewrite (режим full — всегда; конфиг rewrite.enabled управляет
	// только дефолтным AnswerRAG/SayRAG).
	if mode == ModeFull {
		if rewritten, ok := RewriteQuery(client, q, cfg.Rewrite.MaxTokens); ok && rewritten != q {
			q = rewritten
			ans.Rewritten = true
			ans.RewrittenQuery = rewritten
		}
	}

	// Ретрив: baseline (base) или полный пайплайн (filter/full).
	var result *Result
	var err error
	if mode == ModeBase {
		result, err = retriever.RetrieveRaw(ctx, q)
	} else {
		result, err = retriever.Retrieve(ctx, q)
	}
	if err != nil {
		return nil, err
	}

	// --- Жёсткий гейт «не знаю» (День 24) ---
	// Если после этапа 2 не осталось чанков либо top-1 score ниже порога —
	// возвращаем детерминированный отказ без вызова LLM (анти-галлюцинация).
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

	// Промпт: строгий формат с цитатами (День 24) либо базовый (День 22/23).
	system := ragSystemPrompt
	if cfg.CitationsRequired {
		system = citeSystemPrompt
	}
	msgs := []llm.Message{
		{Role: "system", Content: system + "\n\nКонтекст:\n" + contextText},
		{Role: "user", Content: query},
	}
	res, err := chatWithRetry(client, msgs, &llm.Options{MaxTokens: maxTokens}, 3)
	if err != nil {
		return nil, err
	}

	ans.Text = res.Text
	ans.Usage = res
	ans.Sources = result.Chunks
	ans.Engine = result.Engine
	ans.Stages = result.Stages

	// Разбор строгого формата + мягкий гейт «не знаю» (модель сама отказалась).
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

// looksUnknown — эвристика: модель ответила «не знаю» без секций (мягкий гейт).
func looksUnknown(text string) bool {
	low := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(low, "не знаю") ||
		strings.Contains(low, "нет информации") ||
		strings.Contains(low, "нет ответа")
}

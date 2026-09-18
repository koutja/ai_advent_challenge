package memory

import (
	"regexp"
	"strings"

	"aichallenge/llm"
)

// Режимы извлечения фактов (см. NewExtractor).
const (
	// ExtractorLLM — извлечение фактов отдельным вызовом LLM (точнее, дороже).
	ExtractorLLM = "llm"
	// ExtractorHeuristic — регулярки по маркерам («цель:», «ограничение:», …).
	ExtractorHeuristic = "heuristic"
)

// Extractor — интерфейс извлечения фактов «ключ-значение» из текста пользователя.
type Extractor interface {
	Extract(text string) map[string]string
}

// NewExtractor создаёт извлекатель по режиму ("llm" | "heuristic").
// Для LLM-режима нужен client; heuristic — детерминированный, без LLM.
func NewExtractor(client *llm.Client, mode string) Extractor {
	switch mode {
	case ExtractorLLM:
		return &LLMExtractor{client: client}
	default:
		return &HeuristicExtractor{}
	}
}

// HeuristicExtractor — извлечение фактов регулярками по маркерам. Детерминировано,
// без обращения к LLM (используется в тестах и как запасной режим).
type HeuristicExtractor struct{}

var factMarkerRe = regexp.MustCompile(`(?i)(цель|задача|ограничение|дедлайн|срок|предпочтение|решение|договорённость|договоренность|антифича|аудитория|стек|требование)\s*[:：]`)

// Extract находит пары «ключ: значение» по маркерам. Значение берётся до следующего
// маркера или до конца строки, поэтому несколько фактов на одной строке разбиваются
// корректно.
func (h *HeuristicExtractor) Extract(text string) map[string]string {
	out := map[string]string{}
	idx := factMarkerRe.FindAllStringSubmatchIndex(text, -1)
	for i, m := range idx {
		key := strings.ToLower(strings.TrimSpace(text[m[2]:m[3]]))
		valStart := m[1] // конец полного совпадения (после двоеточия)
		end := len(text)
		if i+1 < len(idx) {
			end = idx[i+1][0] // до начала следующего маркера
		} else if nl := strings.IndexByte(text[valStart:], '\n'); nl >= 0 {
			end = valStart + nl
		}
		val := strings.TrimSpace(text[valStart:end])
		if key != "" && val != "" {
			out[key] = val
		}
	}
	return out
}

// LLMExtractor — извлечение фактов отдельным вызовом LLM (точнее, но дороже).
type LLMExtractor struct {
	client *llm.Client
}

// llmExtractPrompt — инструкция для извлечения ключ-значение из реплики пользователя.
const llmExtractPrompt = `Ты извлекаешь факты из реплики пользователя в формате «ключ: значение».
Выделяй: цель, ограничения, дедлайн/срок, предпочтения, решения, договорённости,
аудиторию, стек/требования. Каждый факт на отдельной строке вида «ключ: значение».
Ключи — одним словом или коротким словосочетанием. Если фактов нет — верни пустой ответ.`

var kvLineRe = regexp.MustCompile(`(?i)^\s*([а-яёa-z][а-яёa-z0-9 _\-]{0,40})\s*[:：]\s*(.+?)\s*$`)

// Extract отправляет текст в LLM и разбирает ответ на пары ключ-значение.
func (e *LLMExtractor) Extract(text string) map[string]string {
	if e == nil || e.client == nil {
		return map[string]string{}
	}
	temp := 0.2
	out, err := e.client.Chat(
		[]llm.Message{
			{Role: "system", Content: llmExtractPrompt},
			{Role: "user", Content: text},
		},
		&llm.Options{MaxTokens: 200, Temperature: &temp},
	)
	if err != nil {
		return map[string]string{}
	}
	return parseKV(out)
}

// parseKV разбирает текст «ключ: значение» построчно в карту.
func parseKV(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		m := kvLineRe.FindStringSubmatch(line)
		if len(m) >= 3 {
			key := strings.ToLower(strings.TrimSpace(m[1]))
			val := strings.TrimSpace(m[2])
			if key != "" && val != "" {
				out[key] = val
			}
		}
	}
	return out
}

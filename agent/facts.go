package agent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"aichallenge/llm"
)

// Режимы извлечения фактов (см. FactsMemory).
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

// FactsMemory — стратегия контекста «Facts / Key-Value Memory» (Стратегия 2).
//
// В запрос уходит отдельный system-блок «Факты диалога» + последние N сообщений.
// Факты извлекаются из каждого user-сообщения и мержатся в map[string]string,
// поэтому важные детали (цель/ограничения/дедлайн) держатся дольше, чем при
// простом скользящем окне. Держит состояние в RAM стратегии.
type FactsMemory struct {
	client    *llm.Client // нужен только для LLM-режима извлечения
	facts     map[string]string
	keepLast  int // сколько последних сообщений отправлять вместе с фактами
	keyMax    int // лимит числа ключей фактов
	extractor Extractor
	sink      EventSinkFunc // приёмник событий стратегии (nil — события не шлются)
}

// NewFactsMemory создаёт стратегию фактов. mode: "llm" | "heuristic".
func NewFactsMemory(client *llm.Client, keepLast, keyMax int, mode string) *FactsMemory {
	if keepLast <= 0 {
		keepLast = 10
	}
	if keyMax <= 0 {
		keyMax = 50
	}
	f := &FactsMemory{
		client:   client,
		facts:    map[string]string{},
		keepLast: keepLast,
		keyMax:   keyMax,
	}
	switch mode {
	case ExtractorLLM:
		f.extractor = &LLMExtractor{client: client}
	default:
		f.extractor = &HeuristicExtractor{}
	}
	return f
}

// Name возвращает имя стратегии.
func (f *FactsMemory) Name() string { return "facts" }

// SetEventSink подключает приёмник событий (реализация EventAwareStrategy).
func (f *FactsMemory) SetEventSink(fn EventSinkFunc) { f.sink = fn }

// emit отправляет событие стратегии, если подключён приёмник.
func (f *FactsMemory) emit(kind, text string) {
	if f.sink != nil {
		f.sink(StrategyEvent{Kind: kind, Text: text})
	}
}

// History возвращает всю историю из памяти.
func (f *FactsMemory) History(mem Memory) []llm.Message {
	hist, _ := mem.Load()
	return hist
}

// Build собирает запрос: system-блок фактов (если есть) + последние N сообщений
// + текущий ввод пользователя.
func (f *FactsMemory) Build(hist []llm.Message, input string) []llm.Message {
	var msgs []llm.Message
	lastN := min(len(hist), f.keepLast)
	if len(f.facts) > 0 {
		f.emit(EventLog, fmt.Sprintf("факты: отправляю блок из %d фактов + последние %d сообщений", len(f.facts), lastN))
		msgs = append(msgs, llm.Message{Role: "system", Content: "Факты диалога:\n" + f.render()})
	} else {
		f.emit(EventLog, fmt.Sprintf("факты: пока пусто, отправляю последние %d сообщений", lastN))
	}
	if n := len(hist); n > f.keepLast {
		hist = hist[n-f.keepLast:]
	}
	msgs = append(msgs, hist...)
	msgs = append(msgs, llm.Message{Role: "user", Content: input})
	return msgs
}

// Observe извлекает факты из последнего user-сообщения и мержит их в f.facts,
// попутно сообщая о том, что извлечено и когда близок лимит ключей.
func (f *FactsMemory) Observe(hist []llm.Message) error {
	var last string
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Role == "user" {
			last = hist[i].Content
			break
		}
	}
	if last == "" {
		return nil
	}
	f.emit(EventLog, "факты: извлекаю ключевые факты из последней реплики пользователя")
	extracted := f.extractor.Extract(last)
	added := 0
	for k, v := range extracted {
		if _, exists := f.facts[k]; !exists {
			added++
		}
		f.setFact(k, v)
	}
	switch {
	case added > 0:
		f.emit(EventLog, fmt.Sprintf("факты: новых фактов %d, всего сохранено %d", added, len(f.facts)))
	case len(extracted) > 0:
		f.emit(EventLog, fmt.Sprintf("факты: новых не найдено, всего %d", len(f.facts)))
	default:
		f.emit(EventLog, "факты: в реплике нет ключевых фактов (цель/ограничение/дедлайн и т.п.)")
	}

	switch {
	case len(f.facts) >= f.keyMax:
		f.emit(EventWarn, fmt.Sprintf("факты: достигнут лимит ключей (%d) — при добавлении нового будет вытеснен самый старый", f.keyMax))
	case len(f.facts) >= f.keyMax*3/4:
		f.emit(EventPredict, fmt.Sprintf("скоро: фактов уже %d из лимита %d — близится вытеснение самого старого ключа", len(f.facts), f.keyMax))
	}
	return nil
}

// Reset очищает все факты.
func (f *FactsMemory) Reset() error {
	f.facts = map[string]string{}
	return nil
}

// State возвращает текущие факты (для отладки/сравнения).
func (f *FactsMemory) State() string {
	if len(f.facts) == 0 {
		return "(нет фактов)"
	}
	return f.render()
}

// Facts возвращает копию карты фактов (для CLI /debug).
func (f *FactsMemory) Facts() map[string]string {
	out := make(map[string]string, len(f.facts))
	for k, v := range f.facts {
		out[k] = v
	}
	return out
}

// setFact добавляет/обновляет факт с учётом лимита ключей: при переполнении
// самый «старый» ключ (по алфавиту) вытесняется.
func (f *FactsMemory) setFact(k, v string) {
	if _, exists := f.facts[k]; !exists && len(f.facts) >= f.keyMax {
		// Вытесняем первый по алфавиту ключ.
		var oldest string
		for kk := range f.facts {
			if oldest == "" || kk < oldest {
				oldest = kk
			}
		}
		if oldest != "" {
			delete(f.facts, oldest)
		}
	}
	f.facts[k] = v
}

// render форматирует факты в виде стабильных строк «ключ: значение» (по алфавиту).
func (f *FactsMemory) render() string {
	keys := make([]string, 0, len(f.facts))
	for k := range f.facts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s: %s\n", k, f.facts[k])
	}
	return sb.String()
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

package agent

import (
	"strings"
	"testing"

	"aichallenge/llm"
)

// TestHeuristicExtractFacts: хьюристическое извлечение детерминировано и находит
// пары «ключ: значение» по маркерам, корректно разделяя несколько фактов на строке
// и перенося их через новые строки.
func TestHeuristicExtractFacts(t *testing.T) {
	h := &HeuristicExtractor{}
	text := "Цель: учёт финансов.\nОграничение: работать офлайн.\nДедлайн: 30 ноября."
	facts := h.Extract(text)
	if len(facts) != 3 {
		t.Fatalf("ожидали 3 факта, получили %d: %+v", len(facts), facts)
	}
	if facts["цель"] != "учёт финансов." {
		t.Fatalf("неверный факт цель: %q", facts["цель"])
	}
	if facts["дедлайн"] != "30 ноября." {
		t.Fatalf("неверный факт дедлайн: %q", facts["дедлайн"])
	}

	// Несколько фактов в одной строке тоже разделяются.
	single := h.Extract("Ограничение: офлайн. Дедлайн: 30 ноября.")
	if single["ограничение"] != "офлайн." || single["дедлайн"] != "30 ноября." {
		t.Fatalf("неверное разделение на одной строке: %+v", single)
	}
}

// TestFactsMemoryObserveMerges: Observe извлекает факты из последнего user-сообщения
// и мержит их в карту (по ходу каждого хода).
func TestFactsMemoryObserveMerges(t *testing.T) {
	f := NewFactsMemory(nil, 10, 50, ExtractorHeuristic)
	_ = f.Observe([]llm.Message{
		{Role: "user", Content: "Цель: мобильное приложение."},
		{Role: "assistant", Content: "Понял."},
	})
	_ = f.Observe([]llm.Message{
		{Role: "user", Content: "Ограничение: офлайн. Дедлайн: 30 ноября."},
		{Role: "assistant", Content: "принято"},
	})
	if f.facts["цель"] != "мобильное приложение." {
		t.Fatalf("цель не извлечена: %+v", f.facts)
	}
	if f.facts["ограничение"] != "офлайн." || f.facts["дедлайн"] != "30 ноября." {
		t.Fatalf("мерж не сработал: %+v", f.facts)
	}
}

// TestFactsMemoryBuild: Build собирает system-блок фактов + последние N сообщений
// + ввод пользователя.
func TestFactsMemoryBuild(t *testing.T) {
	f := NewFactsMemory(nil, 2, 50, ExtractorHeuristic)
	hist := []llm.Message{
		{Role: "user", Content: "Цель: X. Ограничение: Y."},
		{Role: "assistant", Content: "ок"},
		{Role: "user", Content: "Дедлайн: Z."},
		{Role: "assistant", Content: "принято"},
	}
	_ = f.Observe(hist[:2])
	_ = f.Observe(hist)
	msgs := f.Build(hist, "новый вопрос")
	// Ожидаем: system-факты + последние 2 сообщения + ввод = 4 сообщения.
	if len(msgs) != 4 {
		t.Fatalf("ожидали 4 сообщения, получили %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" || !strings.Contains(msgs[0].Content, "цель") {
		t.Fatalf("первым должен быть system-блок фактов: %+v", msgs[0])
	}
	if !strings.Contains(msgs[0].Content, "ограничение") || !strings.Contains(msgs[0].Content, "дедлайн") {
		t.Fatalf("в фактах должны быть все ключи: %+v", msgs[0].Content)
	}
	last := msgs[len(msgs)-1]
	if last.Role != "user" || last.Content != "новый вопрос" {
		t.Fatalf("последним должен быть ввод: %+v", last)
	}
}

// TestFactsMemoryNoFactsNoSystem: без фактов system-блок не добавляется.
func TestFactsMemoryNoFactsNoSystem(t *testing.T) {
	f := NewFactsMemory(nil, 10, 50, ExtractorHeuristic)
	hist := []llm.Message{{Role: "user", Content: "просто реплика"}}
	msgs := f.Build(hist, "вопрос")
	for _, m := range msgs {
		if m.Role == "system" {
			t.Fatalf("system-блок не должен появляться без фактов: %+v", msgs)
		}
	}
}

// TestFactsMemoryKeyLimit: лимит ключей вытесняет самый «старый» (по алфавиту) ключ.
func TestFactsMemoryKeyLimit(t *testing.T) {
	f := NewFactsMemory(nil, 10, 2, ExtractorHeuristic)
	_ = f.Observe([]llm.Message{{Role: "user", Content: "Цель: X."}})
	_ = f.Observe([]llm.Message{{Role: "user", Content: "Аудитория: Y."}})
	_ = f.Observe([]llm.Message{{Role: "user", Content: "Дедлайн: Z."}})
	if len(f.facts) > 2 {
		t.Fatalf("превышен лимит ключей: %d", len(f.facts))
	}
	// Самый маленький ключ по алфавиту — «аудитория» — должен быть вытеснен.
	if _, ok := f.facts["аудитория"]; ok {
		t.Fatalf("аудитория должна была быть вытеснена: %+v", f.facts)
	}
	if _, ok := f.facts["цель"]; !ok {
		t.Fatalf("цель должна остаться: %+v", f.facts)
	}
}

// TestFactsMemoryStateAndReset: State возвращает факты, Reset очищает.
func TestFactsMemoryStateAndReset(t *testing.T) {
	f := NewFactsMemory(nil, 10, 50, ExtractorHeuristic)
	if f.State() != "(нет фактов)" {
		t.Fatalf("ожидали пустой State, получили %q", f.State())
	}
	_ = f.Observe([]llm.Message{{Role: "user", Content: "Цель: X."}})
	if !strings.Contains(f.State(), "цель") {
		t.Fatalf("State должен содержать факты: %q", f.State())
	}
	if err := f.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if f.State() != "(нет фактов)" {
		t.Fatalf("после Reset факты должны очиститься: %q", f.State())
	}
	if f.Name() != "facts" {
		t.Fatalf("ожидали имя facts, получили %q", f.Name())
	}
}

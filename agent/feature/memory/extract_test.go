package memory_test

import (
	"testing"

	"agent/feature/memory"
)

// TestHeuristicExtractFacts: хьюристическое извлечение детерминировано и находит
// пары «ключ: значение» по маркерам, корректно разделяя несколько фактов на строке.
func TestHeuristicExtractFacts(t *testing.T) {
	h := &memory.HeuristicExtractor{}
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

// TestNewExtractorModes: NewExtractor выбирает режим по имени.
func TestNewExtractorModes(t *testing.T) {
	if _, ok := memory.NewExtractor(nil, memory.ExtractorHeuristic).(*memory.HeuristicExtractor); !ok {
		t.Fatal("heuristic-режим должен давать HeuristicExtractor")
	}
	// LLM-режим без клиента возвращает LLMExtractor (пустой результат при вызове).
	if _, ok := memory.NewExtractor(nil, memory.ExtractorLLM).(*memory.LLMExtractor); !ok {
		t.Fatal("llm-режим должен давать LLMExtractor")
	}
}

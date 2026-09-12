package agent

import (
	"strings"
	"testing"

	"aichallenge/llm"
)

// collectSink возвращает приёмник событий и функцию для чтения собранного.
func collectSink() (EventSinkFunc, func() []StrategyEvent) {
	var evs []StrategyEvent
	return func(ev StrategyEvent) { evs = append(evs, ev) },
		func() []StrategyEvent { return evs }
}

func anyEvent(evs []StrategyEvent, kind, substr string) bool {
	for _, e := range evs {
		if e.Kind == kind && strings.Contains(e.Text, substr) {
			return true
		}
	}
	return false
}

// TestWindowEmitsTrimEvents: при превышении окна SlidingWindow сообщает об
// отбрасывании старых сообщений и предсказывает повторное превышение.
func TestWindowEmitsTrimEvents(t *testing.T) {
	w := NewSlidingWindow(2)
	sink, get := collectSink()
	w.SetEventSink(sink)

	hist := []llm.Message{
		{Role: "user", Content: "1"}, {Role: "assistant", Content: "2"},
		{Role: "user", Content: "3"}, {Role: "assistant", Content: "4"},
	}
	_ = w.Build(hist, "вопрос")

	evs := get()
	if !anyEvent(evs, EventLog, "отбрасываю") {
		t.Fatalf("ожидали log про отбрасывание: %+v", evs)
	}
	if !anyEvent(evs, EventPredict, "превысит окно") {
		t.Fatalf("ожидали predict про превышение окна: %+v", evs)
	}
}

// TestWindowNoPredictWhenRoom: пока история влезает в окно — предсказания нет.
func TestWindowNoPredictWhenRoom(t *testing.T) {
	w := NewSlidingWindow(10)
	sink, get := collectSink()
	w.SetEventSink(sink)

	_ = w.Build([]llm.Message{{Role: "user", Content: "a"}}, "q")
	for _, e := range get() {
		if e.Kind == EventPredict {
			t.Fatalf("не ожидали predict при достаточном месте: %+v", e)
		}
	}
}

// TestFactsEmitsExtractionEvents: Observed извлекает факты и сообщает о них.
func TestFactsEmitsExtractionEvents(t *testing.T) {
	f := NewFactsMemory(nil, 10, 50, ExtractorHeuristic)
	sink, get := collectSink()
	f.SetEventSink(sink)

	_ = f.Observe([]llm.Message{
		{Role: "user", Content: "Цель: X. Ограничение: Y."},
		{Role: "assistant", Content: "ок"},
	})

	evs := get()
	if !anyEvent(evs, EventLog, "извлекаю") {
		t.Fatalf("ожидали log про извлечение: %+v", evs)
	}
	if !anyEvent(evs, EventLog, "новых фактов") {
		t.Fatalf("ожидали log про новые факты: %+v", evs)
	}
}

// TestFactsEmitsWarnAtLimit: при достижении лимита ключей Facts шлёт warn.
func TestFactsEmitsWarnAtLimit(t *testing.T) {
	f := NewFactsMemory(nil, 10, 3, ExtractorHeuristic)
	sink, get := collectSink()
	f.SetEventSink(sink)

	for _, txt := range []string{"Цель: A.", "Ограничение: B.", "Дедлайн: C."} {
		_ = f.Observe([]llm.Message{{Role: "user", Content: txt}})
	}

	if !anyEvent(get(), EventWarn, "лимит") {
		t.Fatalf("ожидали warn про лимит ключей: %+v", get())
	}
}

// TestBranchEmitsActiveBranchLog: Branching сообщает активную ветку в Build.
func TestBranchEmitsActiveBranchLog(t *testing.T) {
	b := NewBranching()
	sink, get := collectSink()
	b.SetEventSink(sink)

	hist := []llm.Message{{Role: "user", Content: "hi"}}
	_ = b.Build(hist, "вопрос")
	if !anyEvent(get(), EventLog, "ветка") {
		t.Fatalf("ожидали log про ветку: %+v", get())
	}
}

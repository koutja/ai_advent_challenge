package context_test

import (
	"strings"
	"testing"

	"aichallenge/llm"

	"agent/feature/context"
)

// collectSink возвращает приёмник событий и функцию для чтения собранного.
func collectSink() (context.EventSinkFunc, func() []context.StrategyEvent) {
	var evs []context.StrategyEvent
	return func(ev context.StrategyEvent) { evs = append(evs, ev) },
		func() []context.StrategyEvent { return evs }
}

func anyEvent(evs []context.StrategyEvent, kind, substr string) bool {
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
	w := context.NewSlidingWindow(2)
	sink, get := collectSink()
	w.SetEventSink(sink)

	hist := []llm.Message{
		{Role: "user", Content: "1"}, {Role: "assistant", Content: "2"},
		{Role: "user", Content: "3"}, {Role: "assistant", Content: "4"},
	}
	_ = w.Build(hist, "вопрос")

	evs := get()
	if !anyEvent(evs, context.EventLog, "отбрасываю") {
		t.Fatalf("ожидали log про отбрасывание: %+v", evs)
	}
	if !anyEvent(evs, context.EventPredict, "превысит окно") {
		t.Fatalf("ожидали predict про превышение окна: %+v", evs)
	}
}

// TestWindowNoPredictWhenRoom: пока история влезает в окно — предсказания нет.
func TestWindowNoPredictWhenRoom(t *testing.T) {
	w := context.NewSlidingWindow(10)
	sink, get := collectSink()
	w.SetEventSink(sink)

	_ = w.Build([]llm.Message{{Role: "user", Content: "a"}}, "q")
	for _, e := range get() {
		if e.Kind == context.EventPredict {
			t.Fatalf("не ожидали predict при достаточном месте: %+v", e)
		}
	}
}

// TestFactsEmitsWorkingState: стратегия facts логирует состояние рабочей памяти.
func TestFactsEmitsWorkingState(t *testing.T) {
	f := context.NewFactsMemory(5)
	sink, get := collectSink()
	f.SetEventSink(sink)
	f.SetWorking(&fakeWorking{m: map[string]string{"цель": "X"}})

	_ = f.Build([]llm.Message{{Role: "user", Content: "hi"}}, "вопрос")
	if !anyEvent(get(), context.EventLog, "рабочей памяти") {
		t.Fatalf("ожидали log про рабочую память: %+v", get())
	}
}

// TestBranchEmitsActiveBranchLog: Branching сообщает активную ветку в Build.
func TestBranchEmitsActiveBranchLog(t *testing.T) {
	b := context.NewBranching()
	sink, get := collectSink()
	b.SetEventSink(sink)

	hist := []llm.Message{{Role: "user", Content: "hi"}}
	_ = b.Build(hist, "вопрос")
	if !anyEvent(get(), context.EventLog, "ветка") {
		t.Fatalf("ожидали log про ветку: %+v", get())
	}
}

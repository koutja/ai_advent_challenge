package agent

import (
	"testing"

	"aichallenge/llm"
)

// TestSlidingWindowTrimsHistory: Build режет историю до последних N сообщений
// и добавляет ввод пользователя последним.
func TestSlidingWindowTrimsHistory(t *testing.T) {
	w := NewSlidingWindow(4)
	hist := []llm.Message{
		{Role: "user", Content: "1"}, {Role: "assistant", Content: "2"},
		{Role: "user", Content: "3"}, {Role: "assistant", Content: "4"},
		{Role: "user", Content: "5"}, {Role: "assistant", Content: "6"},
	}
	msgs := w.Build(hist, "вопрос")
	if len(msgs) != 5 {
		t.Fatalf("ожидали 5 сообщений (4 истории + ввод), получили %d", len(msgs))
	}
	if msgs[0].Content != "3" {
		t.Fatalf("первым должно быть 5-е по счёту сообщение (3), получили %q", msgs[0].Content)
	}
	last := msgs[len(msgs)-1]
	if last.Role != "user" || last.Content != "вопрос" {
		t.Fatalf("последним должен быть ввод пользователя: %+v", last)
	}
}

// TestSlidingWindowKeepsShortHistory: при истории короче окна ничего не режется.
func TestSlidingWindowKeepsShortHistory(t *testing.T) {
	w := NewSlidingWindow(10)
	hist := []llm.Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}
	msgs := w.Build(hist, "вопрос")
	if len(msgs) != 3 {
		t.Fatalf("ожидали 3 сообщения, получили %d", len(msgs))
	}
}

// TestSlidingWindowDefaultSize: размер по умолчанию подставляется при <=0.
func TestSlidingWindowDefaultSize(t *testing.T) {
	w := NewSlidingWindow(0)
	if w.size != 10 {
		t.Fatalf("ожидали размер 10, получили %d", w.size)
	}
}

// TestSlidingWindowNoOpObserveReset: Observe/Reset ничего не ломают.
func TestSlidingWindowNoOpObserveReset(t *testing.T) {
	w := NewSlidingWindow(5)
	hist := []llm.Message{{Role: "user", Content: "x"}}
	if err := w.Observe(hist); err != nil {
		t.Fatalf("Observe должен быть no-op: %v", err)
	}
	if err := w.Reset(); err != nil {
		t.Fatalf("Reset должен быть no-op: %v", err)
	}
	if w.Name() != "window" {
		t.Fatalf("ожидали имя window, получили %q", w.Name())
	}
}

// TestSlidingWindowHistoryUsesMemory: History возвращает историю из памяти.
func TestSlidingWindowHistoryUsesMemory(t *testing.T) {
	mem := NewInMemory()
	_ = mem.Append(llm.Message{Role: "user", Content: "привет"})
	w := NewSlidingWindow(5)
	hist := w.History(mem)
	if len(hist) != 1 || hist[0].Content != "привет" {
		t.Fatalf("History вернул не ту историю: %+v", hist)
	}
}

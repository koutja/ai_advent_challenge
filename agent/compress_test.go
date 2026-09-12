package agent

import (
	"testing"

	"aichallenge/llm"
)

// TestContextManagerKeepsLastRaw: при истории меньше keepLast сжатия нет,
// запрос = вся история + новый ввод, сводки нет.
func TestContextManagerKeepsLastRaw(t *testing.T) {
	cm := NewContextManager(nil, 10, 10) // client не нужен: суммаризация не вызывается
	hist := []llm.Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}
	msgs := cm.Build(hist, "вопрос")
	if cm.Summary() != "" {
		t.Fatalf("сводка не должна быть пустой для короткой истории")
	}
	if len(msgs) != 3 {
		t.Fatalf("ожидали 3 сообщения (история + ввод), получили %d", len(msgs))
	}
	last := msgs[len(msgs)-1]
	if last.Role != "user" || last.Content != "вопрос" {
		t.Fatalf("последним должен быть ввод пользователя: %+v", last)
	}
}

// TestContextManagerBuildAddsUserLast: построенный запрос всегда заканчивается
// вводом пользователя, даже при длинной истории.
func TestContextManagerBuildAddsUserLast(t *testing.T) {
	// summarizeEvery большой — суммаризация (и вызов LLM) в этом тесте не запускается.
	cm := NewContextManager(nil, 2, 1000)
	hist := []llm.Message{
		{Role: "user", Content: "1"}, {Role: "assistant", Content: "2"},
		{Role: "user", Content: "3"}, {Role: "assistant", Content: "4"},
		{Role: "user", Content: "5"}, {Role: "assistant", Content: "6"},
	}
	msgs := cm.Build(hist, "новый вопрос")
	if n := len(msgs); n == 0 {
		t.Fatal("пустой результат Build")
	}
	last := msgs[len(msgs)-1]
	if last.Role != "user" || last.Content != "новый вопрос" {
		t.Fatalf("ожидали ввод пользователя последним, получили %+v", last)
	}
}

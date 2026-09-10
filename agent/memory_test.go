package agent

import (
	"path/filepath"
	"testing"

	"aichallenge/llm"
)

// TestSQLiteMemoryPersistence проверяет, что история сохраняется в SQLite и
// восстанавливается после переоткрытия БД (имитация перезапуска приложения).
func TestSQLiteMemoryPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hist.db")

	m1, err := NewSQLiteMemory(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := m1.Append(llm.Message{Role: "user", Content: "привет"}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if err := m1.Append(llm.Message{Role: "assistant", Content: "здравствуй"}); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	if err := m1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Повторное открытие той же БД = «перезапуск».
	m2, err := NewSQLiteMemory(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer m2.Close()

	hist, err := m2.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("ожидали 2 сообщения, получили %d: %+v", len(hist), hist)
	}
	if hist[0].Content != "привет" || hist[0].Role != "user" {
		t.Fatalf("первое сообщение не восстановлено: %+v", hist[0])
	}
	if hist[1].Content != "здравствуй" || hist[1].Role != "assistant" {
		t.Fatalf("второе сообщение не восстановлено: %+v", hist[1])
	}
}

// TestSQLiteMemoryReset проверяет очистку истории.
func TestSQLiteMemoryReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hist.db")
	m, err := NewSQLiteMemory(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	if err := m.Append(llm.Message{Role: "user", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	hist, err := m.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(hist) != 0 {
		t.Fatalf("после reset история не пуста: %d", len(hist))
	}
}

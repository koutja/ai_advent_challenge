package memory_test

import (
	"path/filepath"
	"testing"

	"agent/feature/memory"
)

// TestSQLiteLongPersistence: долговременная память переживает перезапуск
// (запись сохраняется в SQLite и восстанавливается после переоткрытия).
func TestSQLiteLongPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "long.db")

	m1, err := memory.NewSQLiteLong(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := m1.Put(memory.LongEntry{Kind: memory.KindProfile, Key: "имя", Value: "Михаил"}); err != nil {
		t.Fatalf("put profile: %v", err)
	}
	if err := m1.Put(memory.LongEntry{Kind: memory.KindDecision, Key: "стек", Value: "Go"}); err != nil {
		t.Fatalf("put decision: %v", err)
	}
	if err := m1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// «Перезапуск».
	m2, err := memory.NewSQLiteLong(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer m2.Close()

	all, err := m2.All()
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ожидали 2 записи после перезапуска, получили %d: %+v", len(all), all)
	}
	profiles, err := m2.ByKind(memory.KindProfile)
	if err != nil || len(profiles) != 1 {
		t.Fatalf("ByKind(profile) должен вернуть 1 запись: %+v / %v", profiles, err)
	}
	if profiles[0].Value != "Михаил" {
		t.Fatalf("неверное значение профиля: %q", profiles[0].Value)
	}
}

// TestInMemoryLongReset: in-memory долговременная память очищается через Reset.
func TestInMemoryLongReset(t *testing.T) {
	m := memory.NewInMemoryLong()
	_ = m.Put(memory.LongEntry{Kind: memory.KindKnowledge, Key: "x", Value: "y"})
	if all, _ := m.All(); len(all) != 1 {
		t.Fatalf("ожидали 1 запись, получили %d", len(all))
	}
	if err := m.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if all, _ := m.All(); len(all) != 0 {
		t.Fatalf("после reset записей быть не должно, получили %d", len(all))
	}
}

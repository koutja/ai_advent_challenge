package task

import (
	"path/filepath"
	"testing"
)

// TestSQLitePersistence проверяет, что пауза и шаг переживают перезапуск:
// состояние пишется в SQLite и восстанавливается новым автоматом.
func TestSQLitePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task.db")

	// Первый «процесс»: начинаем задачу, доходим до execution/2, ставим паузу.
	store1, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	m1, err := New(store1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	must(t, m1.Begin("персистентная задача"))
	must(t, m1.NextStage()) // execution
	must(t, m1.Advance("первый шаг"))
	must(t, m1.SetExpected("второй шаг"))
	must(t, m1.Pause())
	st := m1.Snapshot()
	if st.Stage != StageExecution || st.Step != 2 || !st.Paused || st.Expected != "второй шаг" {
		t.Fatalf("перед перезапуском состояние некорректно: %+v", st)
	}
	store1.Close()

	// Второй «процесс»: открываем ту же БД — состояние должно восстановиться.
	store2, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore (2): %v", err)
	}
	defer store2.Close()
	m2, err := New(store2)
	if err != nil {
		t.Fatalf("New (2): %v", err)
	}
	got := m2.Snapshot()
	if got.Goal != "персистентная задача" || got.Stage != StageExecution ||
		got.Step != 2 || !got.Paused || got.Expected != "второй шаг" {
		t.Fatalf("после перезапуска состояние потеряно: %+v", got)
	}
	if len(got.Log) != 1 || got.Log[0] == "" {
		t.Fatalf("лог не восстановился: %v", got.Log)
	}

	// Resume продолжает с того же шага без повторных объяснений.
	must(t, m2.Resume())
	got = m2.Snapshot()
	if got.Paused || got.Step != 2 || got.Expected != "второй шаг" {
		t.Fatalf("resume после перезапуска сломан: %+v", got)
	}
}

// TestSQLiteClear проверяет, что Clear стирает сохранённое состояние.
func TestSQLiteClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clear.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	m, err := New(store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	must(t, m.Begin("g"))
	must(t, m.Pause())
	must(t, m.Reset())

	m2, err := New(store)
	if err != nil {
		t.Fatalf("New (после Reset): %v", err)
	}
	if st := m2.Snapshot(); st.IsActive() {
		t.Fatalf("Clear не очистил состояние: %+v", st)
	}
}

// TestInMemoryRoundtrip — in-memory хранилище корректно сохраняет лог.
func TestInMemoryRoundtrip(t *testing.T) {
	store := NewInMemoryStore()
	m, err := New(store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	must(t, m.Begin("g"))
	must(t, m.Advance("итог"))
	must(t, m.Pause())

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Log) != 1 || got.Log[0] == "" {
		t.Fatalf("лог не сохранился: %v", got.Log)
	}
	if !got.Paused {
		t.Fatal("пауза не сохранилась")
	}
}

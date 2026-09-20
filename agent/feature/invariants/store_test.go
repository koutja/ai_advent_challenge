package invariants

import (
	"path/filepath"
	"testing"
)

func TestInMemoryStoreCRUD(t *testing.T) {
	s := NewInMemoryStore()
	inv := Invariant{ID: "a1", Title: "Т", Category: CatStack, Text: "x", Severity: SeverityHard, Enabled: true}
	if err := s.Set(inv); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("a1")
	if err != nil || got == nil {
		t.Fatalf("Get(a1) = %v, %v; want non-nil", got, err)
	}
	if got.ID != "a1" || !got.Enabled {
		t.Fatalf("некорректное чтение: %+v", got)
	}
	if _, err := s.Get("nope"); err != nil || s.mustExist("nope") {
		// nope не существует
	}

	list, _ := s.List()
	if len(list) != 1 {
		t.Fatalf("List() len = %d, want 1", len(list))
	}
	all, _ := s.All()
	if len(all) != 1 {
		t.Fatalf("All() len = %d, want 1", len(all))
	}
	// Выключенный — в All, но не в List.
	s.Set(Invariant{ID: "b2", Title: "Офф", Severity: SeverityHard, Enabled: false})
	if n := len(mustList(s)); n != 1 {
		t.Fatalf("List() len = %d, want 1 (только активные)", n)
	}
	if n := len(mustAll(s)); n != 2 {
		t.Fatalf("All() len = %d, want 2", n)
	}

	if err := s.Delete("a1"); err != nil {
		t.Fatal(err)
	}
	if s.mustExist("a1") {
		t.Fatal("a1 не удалён")
	}
	if err := s.Reset(); err != nil {
		t.Fatal(err)
	}
	if n := len(mustAll(s)); n != 0 {
		t.Fatalf("после Reset All() len = %d, want 0", n)
	}
}

func TestSQLiteStorePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inv.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	inv := Invariant{ID: "s1", Title: "SQLite", Category: CatDecision, Text: "хранение", Keywords: []string{"postgres", "mysql"}, Severity: SeverityHard, Enabled: true}
	if err := s.Set(inv); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Переоткрытие — данные переживают (инварианты хранятся отдельно от диалога).
	s2, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.Get("s1")
	if err != nil || got == nil {
		t.Fatalf("после переоткрытия Get(s1) = %v, %v", got, err)
	}
	if got.Title != "SQLite" || !got.Enabled || len(got.Keywords) != 2 {
		t.Fatalf("некорректное восстановление: %+v", got)
	}
}

func TestSQLiteReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inv2.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Set(Invariant{ID: "x", Title: "x", Severity: SeverityHard, Enabled: true})
	if err := s.Reset(); err != nil {
		t.Fatal(err)
	}
	if n := len(mustAll(s)); n != 0 {
		t.Fatalf("после Reset len = %d, want 0", n)
	}
}

// ---- helpers ----

func (s *InMemoryStore) mustExist(id string) bool {
	_, ok := s.items[id]
	return ok
}

func mustList(s Store) []Invariant {
	l, _ := s.List()
	return l
}

func mustAll(s Store) []Invariant {
	a, _ := s.All()
	return a
}

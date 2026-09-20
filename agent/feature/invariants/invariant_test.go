package invariants

import (
	"strings"
	"testing"
)

func TestHard(t *testing.T) {
	tests := []struct {
		name string
		inv  Invariant
		hard bool
	}{
		{"hard+enabled", Invariant{Severity: SeverityHard, Enabled: true}, true},
		{"hard+disabled", Invariant{Severity: SeverityHard, Enabled: false}, false},
		{"soft+enabled", Invariant{Severity: SeveritySoft, Enabled: true}, false},
		{"empty", Invariant{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.inv.Hard(); got != tc.hard {
				t.Fatalf("Hard() = %v, want %v", got, tc.hard)
			}
		})
	}
}

func TestSystemBlockRendersInvariants(t *testing.T) {
	m := NewManager(NewInMemoryStore())
	if err := m.Add(Invariant{
		ID: "i1", Title: "Стек: Go", Category: CatStack, Severity: SeverityHard,
		Text: "Реализация на Go.", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(Invariant{
		ID: "i2", Title: "Перф", Category: CatDecision, Severity: SeveritySoft,
		Text: "Без O(n^2).", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	block := m.SystemBlock()
	if block == "" {
		t.Fatal("SystemBlock() пустой при активных инвариантах")
	}
	for _, want := range []string{"Неизменные инварианты", "[стек]", "Стек: Go", "[техническое решение]", "ОТКАЖИСЬ", "противоречит инварианту"} {
		if !strings.Contains(block, want) {
			t.Fatalf("SystemBlock не содержит %q:\n%s", want, block)
		}
	}
	// Выключенные инварианты не попадают в блок.
	m.Add(Invariant{ID: "i3", Title: "Выкл", Category: CatBusiness, Severity: SeverityHard, Text: "x", Enabled: false})
	if strings.Contains(m.SystemBlock(), "Выкл") {
		t.Fatal("выключенный инвариант попал в SystemBlock")
	}
}

func TestSystemBlockEmpty(t *testing.T) {
	m := NewManager(NewInMemoryStore())
	if got := m.SystemBlock(); got != "" {
		t.Fatalf("SystemBlock() = %q, want пусто", got)
	}
}

func TestSeedFillsEmptyStore(t *testing.T) {
	m := NewManager(NewInMemoryStore())
	n, err := m.Seed()
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("Seed() не добавил инварианты в пустое хранилище")
	}
	active := m.Active()
	if len(active) == 0 {
		t.Fatal("после Seed() нет активных инвариантов")
	}
	// Повторный Seed ничего не добавляет.
	n2, _ := m.Seed()
	if n2 != 0 {
		t.Fatalf("повторный Seed() добавил %d, want 0", n2)
	}
}

func TestCategoryLabel(t *testing.T) {
	if got := CategoryLabel(""); got != "без категории" {
		t.Fatalf("CategoryLabel('') = %q, want 'без категории'", got)
	}
	if got := CategoryLabel(CatArchitecture); got != CatArchitecture {
		t.Fatalf("CategoryLabel(%q) = %q", CatArchitecture, got)
	}
}

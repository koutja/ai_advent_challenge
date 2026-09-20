package invariants

import (
	"strings"
	"testing"
)

// newTestManager создаёт менеджера с двумя инвариантами: один hard, один soft.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(NewInMemoryStore())
	if err := m.Add(Invariant{
		ID: "stack_go", Title: "Стек: Go", Category: CatStack, Severity: SeverityHard, Enabled: true,
		Text:     "Реализация на Go.",
		Keywords: []string{"python", "переписать на rust"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(Invariant{
		ID: "soft_perf", Title: "Перф", Category: CatDecision, Severity: SeveritySoft, Enabled: true,
		Text:     "Без вложенных циклов.",
		Keywords: []string{"вложенный цикл"},
	}); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestHardConflictDetected — конфликт запроса и hard-инварианта обязателен для отказа.
func TestHardConflictDetected(t *testing.T) {
	m := newTestManager(t)
	cs := m.CheckConflict("предлагаю переписать на rust сервис")
	if len(cs) == 0 {
		t.Fatal("ожидался конфликт с hard-инвариантом")
	}
	hard := HardConflicts(cs)
	if len(hard) != 1 || hard[0].Invariant.ID != "stack_go" {
		t.Fatalf("HardConflicts() = %+v, want 1 conflict stack_go", hard)
	}
}

// TestRefusalExplainsInvariant — как ассистент объясняет отказ.
func TestRefusalExplainsInvariant(t *testing.T) {
	m := newTestManager(t)
	cs := m.CheckConflict("нужно переписать на rust")
	refusal := m.RefusalTextAll(HardConflicts(cs))

	for _, want := range []string{"Не могу выполнить запрос", "[стек]", "Стек: Go", "нарушает инвариант", "неизменное ограничение"} {
		if !strings.Contains(refusal, want) {
			t.Fatalf("отказ не содержит %q:\n%s", want, refusal)
		}
	}
	// Причина конфликта объясняется.
	if !strings.Contains(refusal, "переписать на rust") && !strings.Contains(refusal, "rust") {
		t.Fatalf("отказ не объясняет причину (keyword):\n%s", refusal)
	}
}

// TestSoftConflictWarnsButDoesNotRefuse — soft-конфликт не является обязательным отказом.
func TestSoftConflictWarnsButDoesNotRefuse(t *testing.T) {
	m := newTestManager(t)
	cs := m.CheckConflict("можно ли использовать вложенный цикл здесь?")
	if len(cs) == 0 {
		t.Fatal("ожидался мягкий конфликт")
	}
	if n := len(HardConflicts(cs)); n != 0 {
		t.Fatalf("soft-конфликт ошибочно стал hard: %+v", cs)
	}
}

// TestNoConflictNoRefusal — без конфликта отказ не формируется.
func TestNoConflictNoRefusal(t *testing.T) {
	m := newTestManager(t)
	cs := m.CheckConflict("помоги написать функцию на Go")
	if len(cs) != 0 {
		t.Fatalf("конфликт не ожидался: %+v", cs)
	}
	if got := m.RefusalTextAll(cs); got != "" {
		t.Fatalf("отказ для пустых конфликтов должен быть пустым, got %q", got)
	}
}

// TestRefusalMultipleConflicts — сводный отказ по нескольким конфликтам.
func TestRefusalMultipleConflicts(t *testing.T) {
	m := newTestManager(t)
	// Один hard (стек) + один soft (вложенный цикл).
	cs := m.CheckConflict("переписать на rust, используя вложенный цикл")
	if len(cs) < 2 {
		t.Fatalf("ожидалось 2 конфликта, got %d: %+v", len(cs), cs)
	}
	if !strings.Contains(m.RefusalTextAll(cs), "несколько инвариантов") {
		t.Fatalf("сводный отказ не помечен как множественный:\n%s", m.RefusalTextAll(cs))
	}
}

// TestDisabledInvariantIgnored — выключенный инвариант не участвует в проверке.
func TestDisabledInvariantIgnored(t *testing.T) {
	m := NewManager(NewInMemoryStore())
	m.Add(Invariant{ID: "d1", Title: "Off", Category: CatStack, Severity: SeverityHard,
		Text: "x", Keywords: []string{"python"}, Enabled: false})
	if cs := m.CheckConflict("используем python"); len(cs) != 0 {
		t.Fatalf("выключенный инвариант вызвал конфликт: %+v", cs)
	}
}

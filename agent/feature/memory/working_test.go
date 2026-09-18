package memory_test

import (
	"testing"

	"agent/feature/memory"
)

// TestWorkingSetGetClear: рабочая память хранит пары ключ-значение, All возвращает
// копию, Clear очищает.
func TestWorkingSetGetClear(t *testing.T) {
	w := memory.NewInMemoryWorking()
	w.Set("цель", "X")
	w.Set("дедлайн", "Z")
	if v, ok := w.Get("цель"); !ok || v != "X" {
		t.Fatalf("Get(цель) = %q, %v", v, ok)
	}
	if w.Count() != 2 {
		t.Fatalf("ожидали 2 факта, получили %d", w.Count())
	}
	if len(w.All()) != 2 {
		t.Fatalf("All должен возвращать 2 факта, получили %d", len(w.All()))
	}
	w.Clear()
	if w.Count() != 0 {
		t.Fatalf("после Clear рабочая память должна быть пустой, получили %d", w.Count())
	}
}

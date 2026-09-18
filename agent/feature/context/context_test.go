package context_test

import (
	"strings"
	"testing"

	"agent/feature/context"
)

// TestNewStrategyValid: селектор возвращает стратегии нужного типа по имени.
func TestNewStrategyValid(t *testing.T) {
	opts := context.Options{WindowSize: 10, FactsKeepLast: 10}
	cases := []struct {
		name string
		want string
	}{
		{"window", "window"},
		{"facts", "facts"},
		{"branch", "branch"},
	}
	for _, c := range cases {
		st, err := context.NewStrategy(c.name, opts)
		if err != nil {
			t.Fatalf("NewStrategy(%q): %v", c.name, err)
		}
		if st.Name() != c.want {
			t.Fatalf("ожидали имя %q, получили %q", c.want, st.Name())
		}
	}
}

// TestNewStrategyInvalid: невалидное имя — ошибка.
func TestNewStrategyInvalid(t *testing.T) {
	if _, err := context.NewStrategy("bogus", context.Options{}); err == nil {
		t.Fatal("ожидали ошибку для невалидного имени стратегии")
	}
}

// TestFactsStrategyUsesWorkingView: стратегия facts видит рабочую память через
// WorkingView и отражает её в State.
func TestFactsStrategyUsesWorkingView(t *testing.T) {
	f := context.NewFactsMemory(5)
	f.SetWorking(&fakeWorking{m: map[string]string{"цель": "X", "дедлайн": "Z"}})
	if !strings.Contains(f.State(), "2 фактов") {
		t.Fatalf("State должен отражать рабочую память: %q", f.State())
	}
}

type fakeWorking struct{ m map[string]string }

func (f *fakeWorking) All() map[string]string { return f.m }

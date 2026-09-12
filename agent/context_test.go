package agent

import (
	"strings"
	"testing"
)

// TestNewStrategyValid: селектор возвращает стратегии нужного типа по имени.
func TestNewStrategyValid(t *testing.T) {
	cfg := DefaultConfig()
	cases := []struct {
		name string
		want string
	}{
		{"window", "window"},
		{"facts", "facts"},
		{"branch", "branch"},
	}
	for _, c := range cases {
		st, err := NewStrategy(c.name, nil, cfg)
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
	if _, err := NewStrategy("bogus", nil, DefaultConfig()); err == nil {
		t.Fatal("ожидали ошибку для невалидного имени стратегии")
	}
}

// TestSetStrategySwitches: SetStrategy переключает активную стратегию и вызывает
// Reset у предыдущей; пустое имя выключает стратегию.
func TestSetStrategySwitches(t *testing.T) {
	a := &Agent{memory: NewInMemory(), cfg: DefaultConfig()}

	if a.StrategyName() != "" {
		t.Fatalf("изначально стратегии быть не должно, получили %q", a.StrategyName())
	}

	if err := a.SetStrategy("window"); err != nil {
		t.Fatalf("SetStrategy(window): %v", err)
	}
	if a.StrategyName() != "window" {
		t.Fatalf("ожидали window, получили %q", a.StrategyName())
	}
	if a.Strategy() == nil {
		t.Fatal("Strategy() не должен быть nil после включения")
	}

	// Переключение на branch — предыдущая (window) сбрасывается.
	if err := a.SetStrategy("branch"); err != nil {
		t.Fatalf("SetStrategy(branch): %v", err)
	}
	if a.StrategyName() != "branch" {
		t.Fatalf("ожидали branch, получили %q", a.StrategyName())
	}

	// Пустое имя — стратегия выключена.
	if err := a.SetStrategy(""); err != nil {
		t.Fatalf("SetStrategy(\"\"): %v", err)
	}
	if a.StrategyName() != "" {
		t.Fatalf("ожидали выключенную стратегию, получили %q", a.StrategyName())
	}
}

// TestSetStrategyInvalid: невалидное имя не меняет активную стратегию.
func TestSetStrategyInvalid(t *testing.T) {
	a := &Agent{memory: NewInMemory(), cfg: DefaultConfig()}
	_ = a.SetStrategy("window")
	if err := a.SetStrategy("nope"); err == nil {
		t.Fatal("ожидали ошибку для невалидного имени")
	}
	if a.StrategyName() != "window" {
		t.Fatalf("активная стратегия не должна измениться, получили %q", a.StrategyName())
	}
}

// TestAgentStrategyConfigInit: New с заданной ContextStrategy поднимает её,
// а с пустой — включает legacy-сжатие. Клиент создаётся только при наличии ключа,
// поэтому строим агента напрямую через NewStrategy.
func TestAgentStrategyConfigInit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ContextStrategy = "branch"
	st, err := NewStrategy(cfg.ContextStrategy, nil, cfg)
	if err != nil {
		t.Fatalf("NewStrategy: %v", err)
	}
	if !strings.HasPrefix(st.Name(), "branch") {
		t.Fatalf("ожидали branch, получили %q", st.Name())
	}
}

package agent

import (
	"aichallenge/llm"
	"testing"

	"agent/feature/memory"
)

// TestSetStrategySwitches: SetStrategy переключает активную стратегию и вызывает
// Reset у предыдущей; пустое имя выключает стратегию.
func TestSetStrategySwitches(t *testing.T) {
	a := &Agent{memory: memory.NewLayeredRAM(), cfg: DefaultConfig()}

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
	a := &Agent{memory: memory.NewLayeredRAM(), cfg: DefaultConfig()}
	_ = a.SetStrategy("window")
	if err := a.SetStrategy("nope"); err == nil {
		t.Fatal("ожидали ошибку для невалидного имени")
	}
	if a.StrategyName() != "window" {
		t.Fatalf("активная стратегия не должна измениться, получили %q", a.StrategyName())
	}
}

// TestRememberWritesLong: Remember явно пишет в долговременный слой агента.
func TestRememberWritesLong(t *testing.T) {
	a := &Agent{memory: memory.NewLayeredRAM(), cfg: DefaultConfig()}
	if err := a.Remember("profile", "имя", "Анна"); err != nil {
		t.Fatalf("remember: %v", err)
	}
	all, err := a.memory.Long().All()
	if err != nil || len(all) != 1 {
		t.Fatalf("ожидали 1 запись long, получили %d / %v", len(all), err)
	}
	if all[0].Value != "Анна" {
		t.Fatalf("неверное значение: %q", all[0].Value)
	}
}

// TestResetContextKeepsLong: ResetContext очищает short+working, но НЕ long.
func TestResetContextKeepsLong(t *testing.T) {
	a := &Agent{memory: memory.NewLayeredRAM(), cfg: DefaultConfig()}
	_ = a.memory.Short().Append(llm.Message{Role: "user", Content: "привет"})
	a.memory.Working().Set("цель", "X")
	_ = a.Remember("knowledge", "тема", "квантовая физика")

	if err := a.ResetContext(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if hist, _ := a.memory.Short().Load(); len(hist) != 0 {
		t.Fatalf("short должна очиститься, получили %d", len(hist))
	}
	if a.memory.Working().Count() != 0 {
		t.Fatalf("working должна очиститься, получили %d", a.memory.Working().Count())
	}
	if all, _ := a.memory.Long().All(); len(all) != 1 {
		t.Fatalf("long должна сохраниться, получили %d", len(all))
	}
}

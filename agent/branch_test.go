package agent

import (
	"testing"

	"aichallenge/llm"
)

// TestBranchingCheckpointBranchSwitch: Checkpoint → Branch → Switch дают
// независимые истории веток.
func TestBranchingCheckpointBranchSwitch(t *testing.T) {
	b := NewBranching()

	// Ход в основной ветке.
	_ = b.Observe([]llm.Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}})
	if got := len(b.History(nil)); got != 2 {
		t.Fatalf("ожидали 2 сообщения в main, получили %d", got)
	}

	// Контрольная точка "cp1" с текущим состоянием.
	if err := b.Checkpoint("cp1"); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if b.Active() != "cp1" {
		t.Fatalf("после Checkpoint активна должна быть cp1, получили %q", b.Active())
	}

	// Новая ветка "feature" от активной (cp1 == 2 сообщения).
	if err := b.Branch("feature"); err != nil {
		t.Fatalf("Branch: %v", err)
	}
	if got := len(b.History(nil)); got != 2 {
		t.Fatalf("ветка feature должна стартовать с 2 сообщений, получили %d", got)
	}

	// Добавляем ход только в feature.
	_ = b.Observe([]llm.Message{
		{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"},
		{Role: "user", Content: "more"}, {Role: "assistant", Content: "yes"},
	})
	if got := len(b.History(nil)); got != 4 {
		t.Fatalf("feature должна иметь 4 сообщения, получили %d", got)
	}

	// Переключаемся обратно на контрольную точку cp1.
	if err := b.Switch("cp1"); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if got := len(b.History(nil)); got != 2 {
		t.Fatalf("cp1 должна иметь 2 сообщения (независимость веток), получили %d", got)
	}
	if got := b.History(nil)[0].Content; got != "hi" {
		t.Fatalf("cp1 начинается неверно: %q", got)
	}
}

// TestBranchingSwitchMissing: переключение на несуществующую ветку — ошибка.
func TestBranchingSwitchMissing(t *testing.T) {
	b := NewBranching()
	if err := b.Switch("nope"); err == nil {
		t.Fatal("ожидали ошибку при переключении на несуществующую ветку")
	}
}

// TestBranchingResetAndNames: Reset возвращает к "main"; Name/Branches работают.
func TestBranchingResetAndNames(t *testing.T) {
	b := NewBranching()
	if b.Name() != "branch" {
		t.Fatalf("ожидали имя branch, получили %q", b.Name())
	}
	_ = b.Checkpoint("cpA")
	_ = b.Branch("brB")
	if len(b.Branches()) != 3 {
		t.Fatalf("ожидали 3 ветки (main,cpA,brB), получили %v", b.Branches())
	}
	if err := b.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if b.Active() != "main" || len(b.Branches()) != 1 {
		t.Fatalf("после Reset должна остаться только main, получили active=%q branches=%v", b.Active(), b.Branches())
	}
}

// TestBranchingAutoNames: пустые имена генерируются автоматически.
func TestBranchingAutoNames(t *testing.T) {
	b := NewBranching()
	if err := b.Branch(""); err != nil {
		t.Fatalf("Branch без имени: %v", err)
	}
	if b.Active() != "br1" {
		t.Fatalf("ожидали авто-имя br1, получили %q", b.Active())
	}
}

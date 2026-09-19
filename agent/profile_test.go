package agent

import (
	"strings"
	"testing"

	"agent/feature/memory"
	"agent/feature/profile"
)

// TestProfileInjectedIntoRequest: активный профиль — первый system-блок каждого запроса.
func TestProfileInjectedIntoRequest(t *testing.T) {
	p := profile.Profile{ID: "kutyakin", Name: "Михаил", Style: "кратко"}
	a := &Agent{memory: memory.NewLayeredRAM(), cfg: DefaultConfig(), activeProfile: &p}
	msgs := a.prepareMessages(nil, "привет")
	if len(msgs) == 0 || msgs[0].Role != "system" {
		t.Fatalf("первым должен быть system-блок профиля: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "Профиль пользователя") || !strings.Contains(msgs[0].Content, "кратко") {
		t.Fatalf("первый блок не содержит профиль: %q", msgs[0].Content)
	}
	// Ввод пользователя остаётся последним.
	last := msgs[len(msgs)-1]
	if last.Role != "user" || last.Content != "привет" {
		t.Fatalf("ввод пользователя должен быть последним: %+v", last)
	}
}

// TestDifferentProfilesDifferentBlocks: разные профили дают разные system-блоки,
// значит ассистент адаптируется под профиль автоматически.
func TestDifferentProfilesDifferentBlocks(t *testing.T) {
	terse := profile.Profile{ID: "terse", Style: "максимально кратко"}
	detailed := profile.Profile{ID: "detailed", Style: "развёрнуто, объясняя детали"}

	aTerse := &Agent{memory: memory.NewLayeredRAM(), cfg: DefaultConfig(), activeProfile: &terse}
	aDetailed := &Agent{memory: memory.NewLayeredRAM(), cfg: DefaultConfig(), activeProfile: &detailed}

	blockT := aTerse.prepareMessages(nil, "q")[0].Content
	blockD := aDetailed.prepareMessages(nil, "q")[0].Content
	if blockT == blockD {
		t.Fatal("system-блоки разных профилей не должны совпадать")
	}
	if !strings.Contains(blockT, "кратко") || !strings.Contains(blockD, "развёрнуто") {
		t.Fatalf("блоки не отражают стиль профиля:\n%s\n---\n%s", blockT, blockD)
	}
}

// TestProfileStoreWiring: SetActiveProfile/SaveProfile работают через хранилище.
func TestProfileStoreWiring(t *testing.T) {
	a := &Agent{memory: memory.NewLayeredRAM(), cfg: DefaultConfig()}
	a.profiles = profile.NewInMemoryStore()

	if err := a.SaveProfile(profile.Profile{ID: "kutyakin", Name: "Михаил"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if a.ActiveProfile() != nil {
		t.Fatal("до активации активного профиля быть не должно")
	}
	if err := a.SetActiveProfile("kutyakin"); err != nil {
		t.Fatalf("set active: %v", err)
	}
	got := a.ActiveProfile()
	if got == nil || got.Name != "Михаил" {
		t.Fatalf("активный профиль неверен: %+v", got)
	}
	// Несуществующий профиль — ошибка.
	if err := a.SetActiveProfile("nope"); err == nil {
		t.Fatal("ожидали ошибку для несуществующего профиля")
	}
}

package profile_test

import (
	"path/filepath"
	"strings"
	"testing"

	"agent/feature/profile"
)

// TestProfileSystemBlock: SystemBlock рендерит поля профиля и инструкцию адаптации;
// пустой профиль — пустая строка.
func TestProfileSystemBlock(t *testing.T) {
	p := profile.Profile{
		ID: "kutyakin", Name: "Михаил", Role: "Flutter-разработчик",
		Style: "кратко", Format: "списками",
		Constraints: []string{"без воды"}, Expertise: []string{"Dart"},
	}
	block := p.SystemBlock()
	for _, want := range []string{"Профиль пользователя", "стиль: кратко", "формат: списками", "ограничения", "экспертиза", "Адаптируй ответы"} {
		if !strings.Contains(block, want) {
			t.Fatalf("SystemBlock не содержит %q:\n%s", want, block)
		}
	}
	if (&profile.Profile{}).SystemBlock() != "" {
		t.Fatal("пустой профиль должен давать пустой system-блок")
	}
}

// TestProfileSetField: SetField обновляет скалярные поля и добавляет в списки.
func TestProfileSetField(t *testing.T) {
	p := &profile.Profile{}
	if !p.SetField("style", "кратко") {
		t.Fatal("поле style должно распознаваться")
	}
	if !p.SetField("constraint", "без воды") {
		t.Fatal("поле constraint должно распознаваться")
	}
	if p.SetField("bogus", "x") {
		t.Fatal("неизвестное поле должно вернуть false")
	}
	if p.Style != "кратко" || len(p.Constraints) != 1 {
		t.Fatalf("SetField применился неверно: %+v", p)
	}
}

// TestTemplatesHaveKutyakin: заготовка профиля по анкете существует и содержит роль.
func TestTemplatesHaveKutyakin(t *testing.T) {
	tpl := profile.Templates()
	p, ok := tpl["kutyakin"]
	if !ok {
		t.Fatalf("нет заготовки kutyakin, доступны: %v", profile.TemplateNames())
	}
	if !strings.Contains(strings.ToLower(p.Role), "flutter") {
		t.Fatalf("роль профиля kutyakin не про Flutter: %q", p.Role)
	}
}

// TestSQLiteStorePersistence: профиль переживает перезапуск (сохраняется в SQLite).
func TestSQLiteStorePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.db")

	s1, err := profile.NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s1.Set(profile.Profile{ID: "kutyakin", Name: "Михаил", Style: "кратко", Expertise: []string{"Dart", "Flutter"}}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// «Перезапуск».
	s2, err := profile.NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	got, err := s2.Get("kutyakin")
	if err != nil || got == nil {
		t.Fatalf("профиль не восстановлен: %v / %v", got, err)
	}
	if got.Name != "Михаил" || got.Style != "кратко" {
		t.Fatalf("неверно восстановлен профиль: %+v", got)
	}
	if len(got.Expertise) != 2 {
		t.Fatalf("список expertise не восстановлен: %+v", got.Expertise)
	}
}

// TestInMemoryStoreListSorted: List возвращает профили, отсортированные по ID.
func TestInMemoryStoreListSorted(t *testing.T) {
	s := profile.NewInMemoryStore()
	_ = s.Set(profile.Profile{ID: "z"})
	_ = s.Set(profile.Profile{ID: "a"})
	_ = s.Set(profile.Profile{ID: "m"})
	list, _ := s.List()
	if len(list) != 3 || list[0].ID != "a" || list[2].ID != "z" {
		t.Fatalf("List не отсортирован по ID: %+v", list)
	}
}

package memory_test

import (
	"strings"
	"testing"

	"aichallenge/llm"

	"agent/feature/memory"
)

// TestLayersStoredSeparately: три слоя хранятся раздельно — запись в один
// не влияет на другие.
func TestLayersStoredSeparately(t *testing.T) {
	m := memory.NewLayeredRAM()

	// short — история диалога.
	_ = m.Short().Append(llm.Message{Role: "user", Content: "привет"})
	// working — данные задачи.
	m.Working().Set("цель", "X")
	// long — профиль/решения/знания.
	if err := m.Remember(memory.KindProfile, "имя", "Михаил"); err != nil {
		t.Fatalf("remember: %v", err)
	}

	hist, _ := m.Short().Load()
	if len(hist) != 1 || hist[0].Content != "привет" {
		t.Fatalf("short слой повреждён: %+v", hist)
	}
	if v, ok := m.Working().Get("цель"); !ok || v != "X" {
		t.Fatalf("working слой повреждён: %q/%v", v, ok)
	}
	if all, _ := m.Long().All(); len(all) != 1 {
		t.Fatalf("long слой повреждён: %d записей", len(all))
	}
}

// TestRouteUserTurn: явный роутинг кладёт извлечённые факты в рабочий слой.
func TestRouteUserTurn(t *testing.T) {
	m := memory.NewLayeredRAM()
	ex := &memory.HeuristicExtractor{}
	added := m.RouteUserTurn("Цель: мобильное приложение. Дедлайн: 30 ноября.", ex)
	if added != 2 {
		t.Fatalf("ожидали 2 новых факта, получили %d", added)
	}
	if v, ok := m.Working().Get("цель"); !ok || v != "мобильное приложение." {
		t.Fatalf("факт цель не попал в working: %q/%v", v, ok)
	}
	// Повторный прогон тех же фактов — новых нет.
	if added = m.RouteUserTurn("Цель: мобильное приложение.", ex); added != 0 {
		t.Fatalf("повторный факт не должен считаться новым, получили %d", added)
	}
}

// TestPrependComposition: Prepend ставит system-блоки long и working перед
// историей/вводом — так слои влияют на запрос к LLM.
func TestPrependComposition(t *testing.T) {
	m := memory.NewLayeredRAM()
	m.Working().Set("цель", "финансы")
	if err := m.Remember(memory.KindDecision, "стек", "Go"); err != nil {
		t.Fatalf("remember: %v", err)
	}

	msgs := m.Prepend([]llm.Message{{Role: "user", Content: "новый вопрос"}})
	if len(msgs) != 3 {
		t.Fatalf("ожидали 3 сообщения (long, working, user), получили %d", len(msgs))
	}
	if msgs[0].Role != "system" || !strings.Contains(msgs[0].Content, "Долговременная") {
		t.Fatalf("первым должен быть long-блок: %+v", msgs[0])
	}
	if msgs[1].Role != "system" || !strings.Contains(msgs[1].Content, "текущей задачи") {
		t.Fatalf("вторым должен быть working-блок: %+v", msgs[1])
	}
	if msgs[2].Role != "user" || msgs[2].Content != "новый вопрос" {
		t.Fatalf("последним должен быть ввод: %+v", msgs[2])
	}
}

// TestResetSessionKeepsLong: ResetSession очищает short+working, но НЕ long.
func TestResetSessionKeepsLong(t *testing.T) {
	m := memory.NewLayeredRAM()
	_ = m.Short().Append(llm.Message{Role: "user", Content: "x"})
	m.Working().Set("цель", "X")
	if err := m.Remember(memory.KindKnowledge, "тема", "квантовая физика"); err != nil {
		t.Fatalf("remember: %v", err)
	}

	if err := m.ResetSession(); err != nil {
		t.Fatalf("reset session: %v", err)
	}
	if hist, _ := m.Short().Load(); len(hist) != 0 {
		t.Fatalf("short должна очиститься, получили %d", len(hist))
	}
	if m.Working().Count() != 0 {
		t.Fatalf("working должна очиститься, получили %d", m.Working().Count())
	}
	if all, _ := m.Long().All(); len(all) != 1 {
		t.Fatalf("long должна сохраниться, получили %d", len(all))
	}
}

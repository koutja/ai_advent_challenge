package agent

import (
	"strings"
	"testing"

	"aichallenge/llm"

	"agent/feature/invariants"
	"agent/feature/memory"
)

// newInvariantAgent создаёт агента с активным hard-инвариантом «Стек: Go».
// Клиент указывает на недоступный адрес: если Say() дойдёт до вызова LLM,
// тест получит ошибку соединения вместо отказа — значит, инвариант отработал раньше.
func newInvariantAgent(t *testing.T) *Agent {
	t.Helper()
	client, err := llm.NewWithConfig("http://127.0.0.1:1", "k", "m")
	if err != nil {
		t.Fatal(err)
	}
	mgr := invariants.NewManager(invariants.NewInMemoryStore())
	if err := mgr.Add(invariants.Invariant{
		ID: "stack_go", Title: "Стек: Go", Category: invariants.CatStack,
		Severity: invariants.SeverityHard, Enabled: true,
		Text:     "Реализация только на Go.",
		Keywords: []string{"python", "переписать на rust"},
	}); err != nil {
		t.Fatal(err)
	}
	return &Agent{memory: memory.NewLayeredRAM(), cfg: DefaultConfig(), client: client, invariants: mgr}
}

// TestSayRefusesConflictWithoutLLM — конфликт запроса и hard-инварианта: ассистент
// отказывается БЕЗ обращения к LLM (клиент на недоступный адрес — любой реальный
// вызов вернул бы ошибку, а не текст отказа).
func TestSayRefusesConflictWithoutLLM(t *testing.T) {
	a := newInvariantAgent(t)
	reply, err := a.Say("предлагаю написать сервис на python")
	if err != nil {
		t.Fatalf("Say() вернул ошибку: %v", err)
	}
	if !strings.Contains(reply.Text, "Не могу выполнить запрос") {
		t.Fatalf("ожидался отказ, got:\n%s", reply.Text)
	}
	if !strings.Contains(reply.Text, "Стек: Go") {
		t.Fatalf("отказ не упоминает нарушенный инвариант:\n%s", reply.Text)
	}
	// Отказанный ход не сохраняется в историю диалога (LLM не вызывался, пара не записана).
	if hist, _ := a.memory.Short().Load(); len(hist) != 0 {
		t.Fatalf("отказанный ход попал в историю: %d сообщений", len(hist))
	}
}

// TestSayAllowsNonConflict — без конфликта инвариант не блокирует запрос.
// Клиент недоступен, поэтому здесь ожидается ОШИБКА соединения, но НЕ текст отказа —
// это подтверждает, что pre-check не срабатывает на корректном запросе.
func TestSayAllowsNonConflict(t *testing.T) {
	a := newInvariantAgent(t)
	reply, err := a.Say("помоги написать функцию на Go")
	if err == nil {
		// Не должно произойти: клиент на 127.0.0.1:1 недоступен.
		t.Fatalf("ожидалась ошибка соединения (нет конфликта, LLM вызван), got reply: %s", reply.Text)
	}
	if strings.Contains(err.Error(), "инвариант") {
		t.Fatalf("конфликт ошибочно обнаружен на корректном запросе: %v", err)
	}
}

// TestInvariantsBlockInjectedFirst — system-блок инвариантов идёт первым сообщением
// запроса (выше профиля и состояния задачи).
func TestInvariantsBlockInjectedFirst(t *testing.T) {
	a := newInvariantAgent(t)
	msgs := a.prepareMessages(nil, "привет")
	if len(msgs) == 0 {
		t.Fatal("prepareMessages не вернул сообщений")
	}
	first := msgs[0]
	if first.Role != "system" || !strings.Contains(first.Content, "Неизменные инварианты") {
		t.Fatalf("первый system-блок не инварианты: %+v", first)
	}
	if !strings.Contains(first.Content, "Стек: Go") {
		t.Fatalf("блок инвариантов не содержит правило: %s", first.Content)
	}
}

// TestInvariantsDisabledNoBlockAndNoRefusal — при выключенном feature блок не
// инжектится и конфликты не детектируются.
func TestInvariantsDisabled(t *testing.T) {
	a := &Agent{memory: memory.NewLayeredRAM(), cfg: DefaultConfig()} // invariants nil
	msgs := a.prepareMessages(nil, "привет")
	for _, m := range msgs {
		if strings.Contains(m.Content, "Неизменные инварианты") {
			t.Fatal("блок инвариантов инжектится при выключенном feature")
		}
	}
	if a.Invariants() != nil {
		t.Fatal("Invariants() должен быть nil при выключенном feature")
	}
}

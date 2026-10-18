package agent

import (
	"strings"
	"testing"
)

// TestParseFilePlan — валидный JSON-план разбирается.
func TestParseFilePlan(t *testing.T) {
	text := `Вот план:
{"scenario":"find","search":[{"pattern":"llm\\.New","path":"agent","file_pattern":".*\\.go"}],"read":["agent/agent.go"],"output_file":""}`
	plan, ok := parseFilePlan(text)
	if !ok {
		t.Fatal("ожидали успешный разбор плана")
	}
	if plan.Scenario != "find" || len(plan.Searches) != 1 || plan.Searches[0].Pattern != `llm\.New` {
		t.Errorf("план: %+v", plan)
	}
	if len(plan.Reads) != 1 || plan.Reads[0] != "agent/agent.go" {
		t.Errorf("read: %v", plan.Reads)
	}
}

// TestParseFilePlanInvalid — невалидный/пустой ответ → false.
func TestParseFilePlanInvalid(t *testing.T) {
	if _, ok := parseFilePlan("не JSON"); ok {
		t.Error("ожидали false для не-JSON")
	}
	if _, ok := parseFilePlan(""); ok {
		t.Error("ожидали false для пустого текста")
	}
	if _, ok := parseFilePlan(`{"scenario":""}`); ok {
		t.Error("ожидали false для пустого сценария")
	}
}

// TestPlanLooksValid — «эхо промпта» (мусорный сценарий) отклоняется.
func TestPlanLooksValid(t *testing.T) {
	if planLooksValid(&FilePlan{Scenario: "find|generate|check|docs|diff"}) {
		t.Error("ожидали false для эха промпта")
	}
	if planLooksValid(&FilePlan{Scenario: "мусор"}) {
		t.Error("ожидали false для неизвестного сценария")
	}
	if !planLooksValid(&FilePlan{Scenario: "find"}) {
		t.Error("ожидали true для find")
	}
}

// TestFallbackFilePlan — детерминированный план по ключевым словам.
func TestFallbackFilePlan(t *testing.T) {
	plan := fallbackFilePlan("найти все места использования llm.New")
	if plan.Scenario != "find" || len(plan.Searches) != 1 {
		t.Errorf("fallback find: %+v", plan)
	}
	// Стоп-слова отбрасываются, идентификатор с точкой — в паттерне.
	if !strings.Contains(plan.Searches[0].Pattern, `llm\.New`) {
		t.Errorf("fallback find: паттерн должен содержать llm.New: %q", plan.Searches[0].Pattern)
	}
	plan = fallbackFilePlan("сгенерировать changelog по последним коммитам")
	if plan.Scenario != "generate" || plan.OutputFile == "" {
		t.Errorf("fallback generate: %+v", plan)
	}
	plan = fallbackFilePlan("проверить соответствие файлов инвариантам")
	if plan.Scenario != "check" {
		t.Errorf("fallback check: %+v", plan)
	}
	plan = fallbackFilePlan("обновить документацию по изменениям кода")
	if plan.Scenario != "docs" || plan.OutputFile == "" {
		t.Errorf("fallback docs: %+v", plan)
	}
}

// TestFilePlanNoClient — без LLM-клиента планирование невозможно.
func TestFilePlanNoClient(t *testing.T) {
	ag := &Agent{}
	if _, err := ag.FilePlan("найти llm.New"); err == nil {
		t.Error("ожидали ошибку «LLM-клиент не инициализирован»")
	}
}

// TestFileGenerateNoClient — без LLM-клиента генерация невозможна.
func TestFileGenerateNoClient(t *testing.T) {
	ag := &Agent{}
	if _, err := ag.FileGenerate("цель", &FilePlan{Scenario: "find"}, "контекст"); err == nil {
		t.Error("ожидали ошибку «LLM-клиент не инициализирован»")
	}
}

// TestFileGenerateUser — запрос пользователя содержит цель и контекст.
func TestFileGenerateUser(t *testing.T) {
	u := fileGenerateUser("найти llm.New", &FilePlan{Scenario: "find"}, "## Поиск: llm.New\n- llm/llm.go:1")
	if !strings.Contains(u, "найти llm.New") || !strings.Contains(u, "llm/llm.go") {
		t.Errorf("fileGenerateUser: %q", u)
	}
}

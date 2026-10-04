package agent

import (
	"strings"
	"testing"
)

func TestGoalKeywordsHit(t *testing.T) {
	cases := []struct {
		name     string
		answer   string
		keywords []string
		want     bool
	}{
		{"all present", "Чтобы настроить RAG в новом дне, подключите llm", []string{"rag", "llm", "день"}, true},
		{"one missing", "Подключите llm в go.mod", []string{"rag", "llm", "день"}, false},
		{"case insensitive", "RAG и LLM для нового ДНЯ", []string{"rag", "llm", "день"}, true},
		{"empty keywords", "любой ответ", []string{}, true},
		{"empty answer", "", []string{"rag"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := goalKeywordsHit(c.answer, c.keywords); got != c.want {
				t.Errorf("goalKeywordsHit(%q, %v) = %v, want %v", c.answer, c.keywords, got, c.want)
			}
		})
	}
}

func TestLoadChatScenarios(t *testing.T) {
	scenarios, err := LoadChatScenarios("chat_scenarios.json")
	if err != nil {
		t.Fatalf("LoadChatScenarios: %v", err)
	}
	if len(scenarios) != 2 {
		t.Fatalf("expected 2 scenarios, got %d", len(scenarios))
	}
	for i, sc := range scenarios {
		if sc.ID == "" || sc.Title == "" || sc.Goal == "" {
			t.Errorf("scenario[%d]: empty id/title/goal", i)
		}
		if len(sc.Turns) < 10 {
			t.Errorf("scenario %s: expected >=10 turns, got %d", sc.ID, len(sc.Turns))
		}
		if len(sc.GoalKeywords) == 0 {
			t.Errorf("scenario %s: no goal_keywords", sc.ID)
		}
		for j, tr := range sc.Turns {
			if tr.Message == "" {
				t.Errorf("scenario %s turn[%d]: empty message", sc.ID, j)
			}
		}
	}
}

func TestRenderChatScenarioReport(t *testing.T) {
	results := []ChatScenarioResult{
		{
			Scenario: ChatScenario{
				ID: "s01", Title: "Тест", Goal: "Цель диалога",
				GoalKeywords: []string{"rag"},
				Turns: []ChatTurn{
					{Message: "Привет", Expect: []string{"rag"}, SourcesExpected: []string{"README.md"}},
				},
			},
			Turns: []ChatTurnResult{
				{Index: 0, Message: "Привет", Answer: "Ответ с rag", FormatOK: true, HasSources: true,
					FactHits: 1, FactTotal: 1, SourcesFound: true, TopScore: 0.85, Engine: "sidecar", GoalPreserved: true},
			},
			GoalPreserved:   true,
			SourcesAllTurns: true,
			FormatAllTurns:  true,
			FactsHits:       1,
			FactsTotal:      1,
			Goal:            "Цель диалога",
			Facts:           map[string]string{"goal": "Цель диалога", "ограничение": "тест"},
		},
	}
	report := RenderChatScenarioReport(results)
	if !strings.Contains(report, "Мини-чат с RAG") {
		t.Error("report missing title")
	}
	if !strings.Contains(report, "s01") {
		t.Error("report missing scenario id")
	}
	if !strings.Contains(report, "Транскрипт") {
		t.Error("report missing transcript section")
	}
	if !strings.Contains(report, "Ответ с rag") {
		t.Error("report missing answer in transcript")
	}
}

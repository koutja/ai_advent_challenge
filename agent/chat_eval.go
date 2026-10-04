package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"agent/feature/rag"
)

// ChatTurn — одна реплика пользователя в сценарии мини-чата.
type ChatTurn struct {
	Message         string   `json:"message"`
	Expect          []string `json:"expect"`           // ключевые слова, которые должны быть в ответе
	SourcesExpected []string `json:"sources_expected"` // ожидаемые пути источников (хотя бы один)
}

// ChatScenario — длинный сценарий (10–15 реплик) для проверки мини-чата.
type ChatScenario struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Goal         string     `json:"goal"`
	GoalKeywords []string   `json:"goal_keywords"` // ключевые слова цели — для проверки «цель не потеряна»
	Turns        []ChatTurn `json:"turns"`
}

// ChatTurnResult — результат одного хода сценария.
type ChatTurnResult struct {
	Index         int // 0-based
	Message       string
	Answer        string
	Unknown       bool
	FormatOK      bool
	HasSources    bool
	FactHits      int
	FactTotal     int
	SourcesFound  bool // хотя бы один ожидаемый источник найден
	TopScore      float64
	Engine        string
	GoalPreserved bool // (только последний успешный ход) — цель не потеряна
	Error         bool // ход завершился ошибкой API (не учитывается в агрегатах)
}

// ChatScenarioResult — результат всего сценария.
type ChatScenarioResult struct {
	Scenario        ChatScenario
	Turns           []ChatTurnResult
	GoalPreserved   bool // финальный ответ всё ещё про цель
	SourcesAllTurns bool // источники были в каждом ходе (кроме итогового, где sources_expected пуст)
	FormatAllTurns  bool // строгий формат соблюдён в каждом ходе
	FactsTotal      int
	FactsHits       int
	Goal            string // зафиксированная цель (из SessionFacts)
	Facts           map[string]string
}

// LoadChatScenarios загружает сценарии из JSON-файла.
func LoadChatScenarios(path string) ([]ChatScenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var wrap struct {
		Scenarios []ChatScenario `json:"scenarios"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	return wrap.Scenarios, nil
}

// RunChatScenarios прогоняет сценарии через мини-чат (SayChat): каждый сценарий
// начинается с сброса контекста (новая сессия, новая цель), затем реплики
// выполняются по очереди с проверкой источников/формата/фактов/цели.
func RunChatScenarios(ctx context.Context, ag *Agent, scenarios []ChatScenario) []ChatScenarioResult {
	out := make([]ChatScenarioResult, 0, len(scenarios))
	for _, sc := range scenarios {
		out = append(out, runChatScenario(ctx, ag, sc))
	}
	return out
}

func runChatScenario(ctx context.Context, ag *Agent, sc ChatScenario) ChatScenarioResult {
	res := ChatScenarioResult{Scenario: sc, Facts: map[string]string{}}

	// Новая сессия: сброс короткого + рабочего слоёв (новая цель диалога).
	_ = ag.ResetContext()

	lastSuccessIdx := -1 // индекс последнего успешного хода (для проверки цели)
	for i, turn := range sc.Turns {
		tr := ChatTurnResult{Index: i, Message: turn.Message}

		reply, err := ag.SayChat(turn.Message)
		if err != nil {
			tr.Answer = fmt.Sprintf("(ошибка: %v)", err)
			tr.Error = true
			res.Turns = append(res.Turns, tr)
			continue
		}
		tr.Answer = reply.Text
		tr.Unknown = reply.Unknown
		tr.FormatOK = reply.FormatOK
		tr.HasSources = len(reply.Sources) > 0
		tr.Engine = reply.Engine
		if len(reply.Sources) > 0 {
			tr.TopScore = reply.Sources[0].Score
		}

		// Факты: ожидаемые ключевые слова в ответе.
		tr.FactHits, tr.FactTotal = rag.FactCoverage(reply.Text, turn.Expect)
		res.FactsHits += tr.FactHits
		res.FactsTotal += tr.FactTotal

		// Источники: хотя бы один ожидаемый путь совпал с найденным чанком.
		if len(turn.SourcesExpected) > 0 && reply.Citation != nil {
			real, _ := rag.ValidateSources(*reply.Citation, reply.Sources)
			tr.SourcesFound = real > 0
		} else if len(turn.SourcesExpected) == 0 {
			tr.SourcesFound = true // нет ожиданий — не штрафуем
		}

		lastSuccessIdx = i
		res.Turns = append(res.Turns, tr)
	}

	// Проверка «цель не потеряна» — на последнем УСПЕШНОМ ходе: ответ содержит
	// goal_keywords. Если все ходы упали с ошибкой API — цель считаем потерянной.
	if lastSuccessIdx >= 0 {
		tr := &res.Turns[lastSuccessIdx]
		tr.GoalPreserved = goalKeywordsHit(tr.Answer, sc.GoalKeywords)
		res.GoalPreserved = tr.GoalPreserved
	}

	// Агрегаты: источники/формат во всех НЕ-ошибочных ходах.
	// Ходы с ошибкой API исключаются — они не отражают качество агента.
	res.SourcesAllTurns = true
	res.FormatAllTurns = true
	for _, tr := range res.Turns {
		if tr.Error {
			continue
		}
		if !tr.HasSources {
			res.SourcesAllTurns = false
		}
		if !tr.FormatOK && !tr.Unknown {
			res.FormatAllTurns = false
		}
	}

	// Зафиксированная цель и факты сессии (для отчёта).
	res.Goal = ag.Goal()
	res.Facts = ag.SessionFacts()

	return res
}

// goalKeywordsHit проверяет, что БОЛЬШИНСТВО ключевых слов цели встречаются в
// ответе (регистронезависимо). Пустой список → true (не проверяем).
//
// Используется «большинство», а не «все»: в русском языке слова склоняются
// (день → дня/дне), и строгое совпадение по всем ключевым словам давало бы ложные
// срабатывания «цель потеряна» из-за морфологии. Латинские термины (rag, llm,
// window) не склоняются и совпадают точно.
func goalKeywordsHit(answer string, keywords []string) bool {
	if len(keywords) == 0 {
		return true
	}
	low := strings.ToLower(answer)
	hits := 0
	for _, kw := range keywords {
		if strings.Contains(low, strings.ToLower(kw)) {
			hits++
		}
	}
	required := (len(keywords) + 1) / 2 // большинство (ceil(n/2))
	return hits >= required
}

// RenderChatScenarioReport формирует markdown-отчёт прогона сценариев.
func RenderChatScenarioReport(results []ChatScenarioResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Мини-чат с RAG + памятью (День 25)\n\n")
	fmt.Fprintf(&b, "_Сформировано %s_\n\n", time.Now().Format("2006-01-02 15:04:05"))

	var totalTurns, sourcesOK, formatOK, factsH, factsT int
	var goalsPreserved, apiErrors int
	for _, r := range results {
		totalTurns += len(r.Turns)
		if r.SourcesAllTurns {
			sourcesOK++
		}
		if r.FormatAllTurns {
			formatOK++
		}
		if r.GoalPreserved {
			goalsPreserved++
		}
		factsH += r.FactsHits
		factsT += r.FactsTotal
		for _, tr := range r.Turns {
			if tr.Error {
				apiErrors++
			}
		}
	}

	fmt.Fprintf(&b, "## Сводка\n\n")
	fmt.Fprintf(&b, "| Сценарий | ходов | источники (все ходы) | формат (все ходы) | факты | цель не потеряна | зафиксированная цель |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|---|\n")
	for _, r := range results {
		fmt.Fprintf(&b, "| %s | %d | %v | %v | %d/%d | %v | %s |\n",
			r.Scenario.ID, len(r.Turns), mark(r.SourcesAllTurns), mark(r.FormatAllTurns),
			r.FactsHits, r.FactsTotal, mark(r.GoalPreserved), shortStr(r.Goal, 60))
	}
	fmt.Fprintf(&b, "\n**Итого:** сценариев %d, ходов %d (из них %d с ошибкой API); "+
		"источники во всех ходах %d/%d, формат во всех ходах %d/%d, факты %d/%d, "+
		"цель не потеряна %d/%d.\n\n",
		len(results), totalTurns, apiErrors,
		sourcesOK, len(results), formatOK, len(results),
		factsH, factsT, goalsPreserved, len(results))

	for _, r := range results {
		fmt.Fprintf(&b, "## %s — %s\n\n", r.Scenario.ID, r.Scenario.Title)
		fmt.Fprintf(&b, "Цель: %s\n\n", r.Scenario.Goal)
		fmt.Fprintf(&b, "Зафиксированная цель (SessionFacts): %s\n\n", shortStr(r.Goal, 80))
		if len(r.Facts) > 0 {
			fmt.Fprintf(&b, "Факты сессии:\n")
			for k, v := range r.Facts {
				fmt.Fprintf(&b, "- %s: %s\n", k, shortStr(v, 80))
			}
			fmt.Fprintf(&b, "\n")
		}
		fmt.Fprintf(&b, "| # | источники | формат | факты | top | движок | цель? |\n")
		fmt.Fprintf(&b, "|---|---|---|---|---|---|---|\n")
		for _, tr := range r.Turns {
			goalMark := ""
			if tr.Index == len(r.Turns)-1 {
				goalMark = mark(tr.GoalPreserved)
			}
			errMark := ""
			if tr.Error {
				errMark = " ⚠API"
			}
			fmt.Fprintf(&b, "| %d | %v | %v | %d/%d | %.3f | %s | %s%s |\n",
				tr.Index+1, mark(tr.HasSources), mark(tr.FormatOK),
				tr.FactHits, tr.FactTotal, tr.TopScore, orDash(tr.Engine), goalMark, errMark)
		}
		fmt.Fprintf(&b, "\n### Транскрипт\n\n")
		for _, tr := range r.Turns {
			fmt.Fprintf(&b, "**[%d] Пользователь:** %s\n\n", tr.Index+1, tr.Message)
			fmt.Fprintf(&b, "**Ассистент:**\n```\n%s\n```\n\n", tr.Answer)
		}
	}

	return b.String()
}

func mark(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func shortStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

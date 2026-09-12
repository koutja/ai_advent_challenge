package agent

import (
	"strings"
	"testing"
)

// TestScoreFinalAnswer: полный ответ со всеми категориями даёт положительное
// качество и стабильность; пустой — нули.
func TestScoreFinalAnswer(t *testing.T) {
	q, s := scoreFinalAnswer("Цель: мобильное приложение для учёта финансов. Ограничение: работать офлайн. Дедлайн: 30 ноября.")
	if q <= 0 {
		t.Fatalf("ожидали ненулевое качество, получили %v", q)
	}
	if s != 1.0 {
		t.Fatalf("ожидали стабильность 1.0, получили %v", s)
	}

	q2, s2 := scoreFinalAnswer("просто какой-то ответ без фактов")
	if q2 != 0 || s2 != 0 {
		t.Fatalf("без фактов качество и стабильность должны быть 0, получили %v/%v", q2, s2)
	}
}

// TestAnalyzeStrategies: анализ содержит все разделы и рекомендует стратегию с
// максимальным комбинированным баллом.
func TestAnalyzeStrategies(t *testing.T) {
	results := []StrategyResult{
		{Name: "window", Tokens: 100, Quality: 0.5, Stability: 0.33, Commands: 12},
		{Name: "facts", Tokens: 150, Quality: 0.9, Stability: 1.0, Commands: 12},
		{Name: "branch", Tokens: 300, Quality: 0.95, Stability: 1.0, Commands: 12},
	}
	analysis := AnalyzeStrategies(results)
	for _, want := range []string{"Меньше всего токенов", "Лучшее качество", "Лучшая стабильность", "Рекомендация"} {
		if !strings.Contains(analysis, want) {
			t.Fatalf("анализ не содержит %q:\n%s", want, analysis)
		}
	}
	// Комбинированные баллы: facts 0.86 > branch 0.78 > window 0.47.
	if !strings.Contains(analysis, "`facts`") {
		t.Fatalf("ожидали рекомендацию facts:\n%s", analysis)
	}
}

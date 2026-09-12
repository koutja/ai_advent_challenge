package agent

import (
	"fmt"
	"strconv"
	"strings"
)

// StrategyResult — метрики одной стратегии после прогона сценария сравнения.
type StrategyResult struct {
	Name      string   `json:"name"`
	Tokens    int      `json:"tokens"`    // сумма request+completion по всем ходам
	Quality   float64  `json:"quality"`   // 0..1: доля ключевых терминов в проверочном ответе
	Stability float64  `json:"stability"` // 0..1: доля категорий (цель/ограничения/дедлайн), не потерянных к концу
	Commands  int      `json:"commands"`  // число ходов сценария
	Answers   []string `json:"answers"`   // ответы на проверочный вопрос
	Error     string   `json:"error,omitempty"`
}

// StrategyNames — порядок прогона сравнения стратегий.
var StrategyNames = []string{"window", "facts", "branch"}

// CompareProgress — событие прогресса прогона одной стратегии (для UI/SSE).
type CompareProgress struct {
	Strategy string `json:"strategy"`
	Step     int    `json:"step"`    // 0..Total (ход в сценарии; 0 = старт стратегии)
	Total    int    `json:"total"`   // всего ходов в сценарии
	Status   string `json:"status"`  // "start" | "ok" | "error" | "question" | "done"
	Message  string `json:"message"` // человекочитаемый статус
}

// ProgressFunc — опциональный колбэк прогресса (nil — без стриминга).
type ProgressFunc func(p CompareProgress)

// compareScenario — фиксированный сценарий «собираем ТЗ» (~12 ходов).
var compareScenario = []string{
	"Цель: создаём мобильное приложение для учёта личных финансов.",
	"Аудитория: молодые люди 20–35 лет, активные пользователи смартфонов.",
	"Фичи: учёт доходов и расходов, категории, отчёты за месяц.",
	"Ограничение: приложение должно работать офлайн.",
	"Антифичи: без рекламы, без обязательной регистрации.",
	"Дедлайн: релиз MVP к 30 ноября.",
	"Предпочтение: стек на Swift/Kotlin, сервер на Go.",
	"Договорённость: интеграция с банковскими API через SDK.",
	"Требование: поддержка двух языков — русский и английский.",
	"Фича: напоминания о платежах и уведомления.",
	"Предпочтение: тёмная тема интерфейса.",
	"Ограничение: бюджет на разработку до 500 тысяч рублей.",
}

// CompareQuestion — финальный проверочный вопрос для оценки качества.
const CompareQuestion = "Перечисли цель, ограничения и дедлайн."

// Ключевые термины для эвристической оценки по фактам сценария.
var (
	compareGoalTerms       = []string{"финанс", "мобильн", "учёт"}
	compareConstraintTerms = []string{"офлайн", "бюджет", "регистрац"}
	compareDeadlineTerms   = []string{"ноябр", "дедлайн", "срок"}
)

// CompareStrategies прогоняет сценарий «собираем ТЗ» на свежем агенте для каждой
// стратегии (window/facts/branch) и возвращает метрики. Ошибка при невозможности
// создать агента (например, отсутствует LLM_API_KEY).
//
// progress — опциональный колбэк прогресса (nil — без стриминга). Используется
// веб-интерфейсом для живого отображения хода сравнения.
func CompareStrategies(cfg *Config, progress ProgressFunc) ([]StrategyResult, error) {
	results := make([]StrategyResult, 0, len(StrategyNames))
	for _, name := range StrategyNames {
		emit := func(p CompareProgress) {
			if progress != nil {
				progress(p)
			}
		}
		c := *cfg
		c.ContextStrategy = name
		emit(CompareProgress{Strategy: name, Step: 0, Total: len(compareScenario), Status: "start", Message: "Старт стратегии"})
		ag, err := New(&c, NewInMemory())
		if err != nil {
			msg := "не удалось создать агента: " + err.Error()
			emit(CompareProgress{Strategy: name, Status: "error", Message: msg})
			results = append(results, StrategyResult{Name: name, Error: msg})
			continue
		}
		res := runStrategyScenario(ag, name, emit)
		emit(CompareProgress{Strategy: name, Step: len(compareScenario), Total: len(compareScenario), Status: "done", Message: "Стратегия завершена"})
		results = append(results, res)
	}
	return results, nil
}

// runStrategyScenario гоняет сценарий через свежий агент, считает метрики и
// уведомляет о прогрессе через emit (может быть no-op).
func runStrategyScenario(ag *Agent, name string, emit func(CompareProgress)) StrategyResult {
	var res StrategyResult
	res.Name = name
	res.Commands = len(compareScenario)

	for i, input := range compareScenario {
		step := i + 1
		reply, err := ag.Say(input)
		if err != nil {
			emit(CompareProgress{Strategy: name, Step: step, Total: len(compareScenario), Status: "error", Message: "ошибка хода: " + err.Error()})
			if res.Error == "" {
				res.Error = "ошибка хода: " + err.Error()
			}
			continue
		}
		emit(CompareProgress{Strategy: name, Step: step, Total: len(compareScenario), Status: "ok", Message: "ход " + strconv.Itoa(step) + "/" + strconv.Itoa(len(compareScenario))})
		if reply.Stats != nil {
			res.Tokens += reply.Stats.RequestTokens + reply.Stats.ResponseTokens
		}
	}

	emit(CompareProgress{Strategy: name, Step: len(compareScenario), Total: len(compareScenario), Status: "question", Message: "проверочный вопрос"})
	final, err := ag.Say(CompareQuestion)
	if err != nil {
		emit(CompareProgress{Strategy: name, Status: "error", Message: "ошибка проверки: " + err.Error()})
		if res.Error == "" {
			res.Error = "ошибка проверки: " + err.Error()
		}
		return res
	}
	res.Answers = append(res.Answers, final.Text)
	res.Quality, res.Stability = scoreFinalAnswer(final.Text)
	return res
}

// scoreFinalAnswer оценивает качество и стабильность по фактам в ответе.
func scoreFinalAnswer(text string) (quality, stability float64) {
	low := strings.ToLower(text)
	goal := countTerms(low, compareGoalTerms)
	constraint := countTerms(low, compareConstraintTerms)
	deadline := countTerms(low, compareDeadlineTerms)

	total := len(compareGoalTerms) + len(compareConstraintTerms) + len(compareDeadlineTerms)
	if total > 0 {
		quality = float64(goal+constraint+deadline) / float64(total)
	}
	// Стабильность: доля категорий (цель/ограничения/дедлайн), представленных в ответе.
	kept := 0
	if goal > 0 {
		kept++
	}
	if constraint > 0 {
		kept++
	}
	if deadline > 0 {
		kept++
	}
	stability = float64(kept) / 3.0
	return quality, stability
}

// countTerms считает, сколько терминов из списка встречается в тексте.
func countTerms(lowText string, terms []string) int {
	c := 0
	for _, t := range terms {
		if strings.Contains(lowText, t) {
			c++
		}
	}
	return c
}

// AnalyzeStrategies формирует текстовый анализ по результатам сравнения: лучшая
// стратегия по каждому критерию и общая рекомендация (комбинированный балл).
func AnalyzeStrategies(results []StrategyResult) string {
	if len(results) == 0 {
		return "Нет данных для анализа."
	}

	var sb strings.Builder
	sb.WriteString("## Анализ\n\n")

	bestTokens := results[0]
	for _, r := range results[1:] {
		if r.Tokens < bestTokens.Tokens || (bestTokens.Tokens == 0 && r.Tokens > 0) {
			bestTokens = r
		}
	}
	bestQuality := results[0]
	for _, r := range results[1:] {
		if r.Quality > bestQuality.Quality {
			bestQuality = r
		}
	}
	bestStab := results[0]
	for _, r := range results[1:] {
		if r.Stability > bestStab.Stability {
			bestStab = r
		}
	}

	maxTokens := 0
	for _, r := range results {
		if r.Tokens > maxTokens {
			maxTokens = r.Tokens
		}
	}

	fmt.Fprintf(&sb, "- **Меньше всего токенов:** `%s` (%d)\n", bestTokens.Name, bestTokens.Tokens)
	fmt.Fprintf(&sb, "- **Лучшее качество:** `%s` (%.0f%%)\n", bestQuality.Name, bestQuality.Quality*100)
	fmt.Fprintf(&sb, "- **Лучшая стабильность:** `%s` (%.0f%%)\n", bestStab.Name, bestStab.Stability*100)

	best := results[0]
	bestScore := combinedScore(best, maxTokens)
	for _, r := range results[1:] {
		if s := combinedScore(r, maxTokens); s > bestScore {
			best = r
			bestScore = s
		}
	}
	fmt.Fprintf(&sb, "\n**Рекомендация:** стратегия **`%s`** (комбинированный балл %.0f%%)\n",
		best.Name, bestScore*100)
	sb.WriteString("\n*Комбинированный балл = 0.4×качество + 0.4×стабильность + 0.2×(1 − токены/макс).*\n")
	return sb.String()
}

// combinedScore считает комбинированный балл стратегии (0..1).
func combinedScore(r StrategyResult, maxTokens int) float64 {
	tok := 0.0
	if maxTokens > 0 {
		tok = float64(r.Tokens) / float64(maxTokens)
	}
	return 0.4*r.Quality + 0.4*r.Stability + 0.2*(1-tok)
}

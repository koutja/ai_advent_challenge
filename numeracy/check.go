package main

import (
	"math"
	"strconv"
	"strings"
)

// CheckResult — результат проверки ответа.
type CheckResult struct {
	Correct  bool   // ответ верен
	Expected string // правильный ответ для показа
	Parsed   string // как распознан ввод пользователя
}

// CheckAnswer проверяет ответ пользователя строго по ЗАДАЧЕ из базы
// (числа/варианты — источник истины; текст задачи на проверку не влияет).
func CheckAnswer(t *Task, input string) CheckResult {
	input = strings.TrimSpace(input)
	if input == "" {
		return CheckResult{Correct: false, Expected: fmtExpected(t), Parsed: input}
	}
	if t.Kind == AnswerChoice {
		return checkChoice(t, input)
	}
	return checkNumeric(t, input)
}

// checkChoice: принимаем номер варианта (1-based) или точный текст варианта.
func checkChoice(t *Task, input string) CheckResult {
	expected := t.Options[t.CorrectIdx]
	if n, err := strconv.Atoi(input); err == nil && n >= 1 && n <= len(t.Options) {
		return CheckResult{Correct: n-1 == t.CorrectIdx, Expected: "№" + strconv.Itoa(t.CorrectIdx+1) + " — «" + expected + "»", Parsed: input}
	}
	for i, opt := range t.Options {
		if strings.EqualFold(strings.TrimSpace(opt), input) {
			return CheckResult{Correct: i == t.CorrectIdx, Expected: expected, Parsed: input}
		}
	}
	return CheckResult{Correct: false, Expected: expected, Parsed: input}
}

// checkNumeric: числовой ответ с допуском; дроби («1/6») и доли (<1) для
// ответов в процентах переводятся автоматически.
func checkNumeric(t *Task, input string) CheckResult {
	v, ok := parseNumberInput(input)
	if !ok {
		return CheckResult{Correct: false, Expected: fmtExpected(t), Parsed: input}
	}
	if t.Unit == UnitPercent && v <= 1 && t.Expected > 1 {
		v *= 100 // доля («0.16», «1/6») → проценты
	}
	correct := math.Abs(v-t.Expected) <= t.Tolerance
	return CheckResult{Correct: correct, Expected: fmtExpected(t), Parsed: input}
}

// parseNumberInput разбирает ответ: число (запятая/точка, пробелы, %) или дробь «a/b».
func parseNumberInput(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "%", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, " ", "") // неразрывный пробел
	s = strings.ReplaceAll(s, ",", ".")
	if idx := strings.Index(s, "/"); idx >= 0 {
		a, err1 := strconv.ParseFloat(strings.TrimSpace(s[:idx]), 64)
		b, err2 := strconv.ParseFloat(strings.TrimSpace(s[idx+1:]), 64)
		if err1 != nil || err2 != nil || b == 0 {
			return 0, false
		}
		return a / b, true
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// fmtExpected — текст правильного ответа для показа.
func fmtExpected(t *Task) string {
	switch t.Kind {
	case AnswerChoice:
		return "№" + strconv.Itoa(t.CorrectIdx+1) + " — «" + t.Options[t.CorrectIdx] + "»"
	case AnswerNumeric:
		if t.Unit == UnitPercent {
			return strconv.FormatFloat(math.Round(t.Expected*10)/10, 'f', -1, 64) + "%"
		}
		return strconv.FormatFloat(t.Expected, 'f', -1, 64)
	}
	return ""
}

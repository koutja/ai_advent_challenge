package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"aichallenge/llm"
)

// numberRe — первое число в тексте (целое).
var numberRe = regexp.MustCompile(`-?\d+`)

// generationPrompt — AI генерирует задачу целиком (вопрос, ответ, объяснение).
// Задача — это НЕ хардкод: её сочиняет LLM, результат сохраняется в SQLite.
const generationPrompt = `Ты — генератор задач по количественной грамотности. Сгенерируй ОДНУ НОВУЮ задачу (не копируй шаблонные формулировки). Категория и уровень заданы. Верни ТОЛЬКО JSON без пояснений:

{
  "category": "percent|probability|scale",
  "level": "easy|hard",
  "kind": "numeric|choice",
  "unit": "percent|amount",
  "expected": 28.0,
  "tolerance": 1.0,
  "question": "текст задачи на русском",
  "options": ["вариант 1", "вариант 2", "вариант 3"],
  "correct_idx": 0,
  "explanation": "короткое объяснение правильного ответа и ловушки/манипуляции"
}

Правила:
1. category/level — как заданы; kind: для scale всегда choice (порядки величин); для остальных — на выбор.
2. expected — правильный ответ числом; для choice — correct_idx (0-based) на правильный вариант.
3. Вопрос и объяснение на русском; вопрос без подсказок на ответ.
4. Числа реалистичные; easy — классическая вероятность/простые доли, hard — условная вероятность (Байес/Монти Холл) или хитрые проценты.
5. Если kind=numeric, unit: percent (ответ в процентах) или amount (в штуках/единицах).
6. МАТЕМАТИКА ОБЯЗАНА БЫТЬ ТОЧНОЙ: пересчитай expected сам из чисел вопроса (например, 3 из 8 → 3/8 = 37.5). НИКОГДА не ставь ожидание, которое не соответствует вопросу; объяснение должно согласовываться с expected.
7. Без преамбул и markdown вокруг JSON.`

// generateTask — AI-генерация задачи с САМОПРОВЕРКОЙ: модель второй раз
// решает свою же задачу; если ответ расходится с expected — задача отклоняется
// (ok=false, reason — причина), чтобы в базу не попадали противоречивые задачи.
func generateTask(client *llm.Client, cat Category, lvl Level) (t *Task, ok bool, reason string) {
	if client == nil {
		return nil, false, "нет LLM-клиента"
	}
	msgs := []llm.Message{
		{Role: "system", Content: generationPrompt},
		{Role: "user", Content: fmt.Sprintf("Категория: %s\nУровень: %s", cat, lvl)},
	}
	res, err := client.ChatResult(msgs, &llm.Options{MaxTokens: 400})
	if err != nil {
		return nil, false, "LLM: " + err.Error()
	}
	t, err = parseGeneratedTask(res.Text)
	if err != nil {
		return nil, false, "невалидный JSON: " + err.Error()
	}
	// Категория/уровень — от запроса (AI может «потерять» их в JSON).
	t.Category, t.Level = cat, lvl
	t.Source = SourceGenerated

	// Самопроверка: решение AI должно совпасть с expected.
	v, okv := verifyAnswer(client, t)
	if !okv {
		return t, false, "не удалось проверить ответ AI"
	}
	tol := t.Tolerance
	if tol < 0.5 {
		tol = 0.5
	}
	if !mathClose(v, t.Expected, tol) {
		return t, false, fmt.Sprintf("AI противоречит себе: expected=%.4g, самопроверка=%.4g", t.Expected, v)
	}
	return t, true, ""
}

// verifyAnswer — LLM решает задачу и возвращает число (для numeric — значение
// в единицах задачи; для choice — номер правильного варианта, 1-based).
func verifyAnswer(client *llm.Client, t *Task) (float64, bool) {
	var sb strings.Builder
	if t.Kind == AnswerChoice {
		fmt.Fprintf(&sb, "Ответь ТОЛЬКО номером правильного варианта (1..%d), без пояснений.\n", len(t.Options))
	} else if t.Unit == UnitPercent {
		sb.WriteString("ОТВЕТ ТОЛЬКО ЧИСЛОМ В ПРОЦЕНТАХ (знак %% можно опустить; отрицательное, если значение уменьшилось), без пояснений.\n")
	} else {
		sb.WriteString("ОТВЕТ ТОЛЬКО ЧИСЛОМ, без пояснений.\n")
	}
	sb.WriteString("Задача:\n" + t.Question + "\n")
	msgs := []llm.Message{
		{Role: "system", Content: "Ты решаешь задачи по количественной грамотности. Следуй формату ответа из запроса."},
		{Role: "user", Content: sb.String()},
	}
	res, err := client.ChatResult(msgs, &llm.Options{MaxTokens: 20})
	if err != nil {
		return 0, false
	}
	if t.Kind == AnswerChoice {
		v, ok := parseIntFirst(res.Text)
		return float64(v), ok
	}
	v, ok := parseNumberInput(res.Text)
	return v, ok
}

// mathClose — |a-b| <= tol.
func mathClose(a, b, tol float64) bool { return a-b <= tol && b-a <= tol }

// parseIntFirst — первое целое в тексте (для номера варианта).
func parseIntFirst(s string) (int, bool) {
	m := numberRe.FindString(s)
	if m == "" {
		return 0, false
	}
	n, err := strconv.Atoi(m)
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseGeneratedTask разбирает JSON-задачу из ответа AI и валидирует структуру
// (категория/уровень/kind, заполненность вопроса и объяснения, варианты и т.п.).
func parseGeneratedTask(text string) (*Task, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("JSON не найден в ответе AI")
	}
	var raw struct {
		Category    string   `json:"category"`
		Level       string   `json:"level"`
		Kind        string   `json:"kind"`
		Unit        string   `json:"unit"`
		Expected    float64  `json:"expected"`
		Tolerance   float64  `json:"tolerance"`
		Question    string   `json:"question"`
		Options     []string `json:"options"`
		CorrectIdx  int      `json:"correct_idx"`
		Explanation string   `json:"explanation"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err != nil {
		return nil, err
	}
	kind, ok := kindFromString(raw.Kind)
	if !ok {
		return nil, fmt.Errorf("неизвестный kind %q", raw.Kind)
	}
	tol := raw.Tolerance
	if tol <= 0 {
		tol = 1
	}
	t := &Task{
		Category:   Category(raw.Category),
		Level:      Level(raw.Level),
		Kind:       kind,
		Unit:       Unit(raw.Unit),
		Expected:   raw.Expected,
		Tolerance:  tol,
		Question:   strings.TrimSpace(raw.Question),
		Options:    raw.Options,
		CorrectIdx: raw.CorrectIdx,
		Explain:    strings.TrimSpace(raw.Explanation),
	}
	if err := validateTask(t); err != nil {
		return nil, err
	}
	return t, nil
}

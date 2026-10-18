package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Category — категория задач тренажёра.
type Category string

const (
	CategoryPercent     Category = "percent"
	CategoryProbability Category = "probability"
	CategoryScale       Category = "scale"
)

// Level — уровень сложности.
type Level string

const (
	LevelEasy Level = "easy" // классическая вероятность/простые доли
	LevelHard Level = "hard" // условная вероятность (Байес, Монти Холл)
)

// AnswerKind — тип ожидаемого ответа.
type AnswerKind int

const (
	AnswerNumeric AnswerKind = iota // числовой ответ (допуск/дроби)
	AnswerChoice                    // multiple-choice (варианты)
)

// Unit — единица измерения ожидаемого числового ответа.
type Unit string

const (
	UnitPercent Unit = "percent" // ответ в процентах (доли «1/6» принимаются)
	UnitAmount  Unit = "amount"  // ответ в штуках/единицах
)

// Source — происхождение задачи.
type Source string

const (
	SourceSeed      Source = "seed"      // базовые задачи (seeds/tasks.json)
	SourceGenerated Source = "generated" // сгенерированы AI и сохранены в базу
)

// Task — задача тренажёра. Хранится в SQLite (таблица tasks):
// сначала AI генерирует задачу и сохраняет её; при неудаче генерации задача
// берётся из базы (предпочтительно ранее сгенерированные).
type Task struct {
	ID         int      // id в базе (0 — ещё не сохранено)
	Category   Category `json:"category"`
	Level      Level    `json:"level"`
	Kind       AnswerKind
	Unit       Unit     `json:"unit,omitempty"`      // для numeric
	Expected   float64  `json:"expected,omitempty"`  // для numeric
	Tolerance  float64  `json:"tolerance,omitempty"` // для numeric
	Question   string   `json:"question"`
	Options    []string `json:"options,omitempty"`     // для choice
	CorrectIdx int      `json:"correct_idx,omitempty"` // для choice
	Explain    string   `json:"explanation"`
	Source     Source   `json:"-"`
}

// categories — все категории для режима all.
var categories = []Category{CategoryPercent, CategoryProbability, CategoryScale}

// validCategory — известная категория (включая all).
func validCategory(c Category) bool {
	switch c {
	case CategoryPercent, CategoryProbability, CategoryScale, "all":
		return true
	}
	return false
}

// validLevel — известный уровень сложности (включая all).
func validLevel(l Level) bool {
	switch l {
	case LevelEasy, LevelHard, "all":
		return true
	}
	return false
}

// kindFromString — «numeric»/«choice» → AnswerKind.
func kindFromString(s string) (AnswerKind, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "numeric", "number":
		return AnswerNumeric, true
	case "choice", "mc", "options":
		return AnswerChoice, true
	}
	return AnswerNumeric, false
}

// kindString — AnswerKind → строка для БД.
func kindString(k AnswerKind) string {
	if k == AnswerChoice {
		return "choice"
	}
	return "numeric"
}

// validateTask — структурная валидация задачи (перед сохранением в базу).
func validateTask(t *Task) error {
	if !(t.Category == CategoryPercent || t.Category == CategoryProbability || t.Category == CategoryScale) {
		return fmt.Errorf("неизвестная категория %q", t.Category)
	}
	if !(t.Level == LevelEasy || t.Level == LevelHard) {
		return fmt.Errorf("неизвестный уровень %q", t.Level)
	}
	if len([]rune(strings.TrimSpace(t.Question))) < 10 {
		return fmt.Errorf("слишком короткий вопрос")
	}
	if len([]rune(strings.TrimSpace(t.Explain))) < 10 {
		return fmt.Errorf("слишком короткое объяснение")
	}
	switch t.Kind {
	case AnswerNumeric:
		if t.Tolerance <= 0 {
			return fmt.Errorf("tolerance должен быть > 0")
		}
		if t.Unit != UnitPercent && t.Unit != UnitAmount && t.Unit != "" {
			return fmt.Errorf("неизвестная единица %q", t.Unit)
		}
	case AnswerChoice:
		if len(t.Options) < 2 {
			return fmt.Errorf("нужно минимум 2 варианта ответа")
		}
		if t.CorrectIdx < 0 || t.CorrectIdx >= len(t.Options) {
			return fmt.Errorf("correct_idx вне диапазона вариантов")
		}
		for _, o := range t.Options {
			if strings.TrimSpace(o) == "" {
				return fmt.Errorf("пустой вариант ответа")
			}
		}
	default:
		return fmt.Errorf("неизвестный kind")
	}
	return nil
}

// optionsJSON — варианты в JSON (для БД).
func optionsJSON(opts []string) string {
	b, _ := json.Marshal(opts)
	return string(b)
}

// jsonOptions — варианты из JSON (из БД).
func jsonOptions(s string) []string {
	var opts []string
	_ = json.Unmarshal([]byte(s), &opts)
	return opts
}

package main

import (
	"math"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
)

func testRNG() *rand.Rand { return rand.New(rand.NewSource(42)) }

// --- SQLite-хранилище ---

func TestStoreSeed(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "numeracy.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close()

	// База засеяна базовыми задачами.
	task, err := st.Task(CategoryPercent, "all")
	if err != nil {
		t.Fatalf("Task(percent): %v", err)
	}
	if task.Source != SourceSeed {
		t.Errorf("первая задача должна быть из seed, получен источник %s", task.Source)
	}
	if task.Question == "" || task.Explain == "" {
		t.Error("seed-задача без вопроса/объяснения")
	}
	// Изоляция уровней.
	if _, err := st.Task(CategoryPercent, LevelHard); err == nil {
		t.Error("для percent/hard не должно быть задач (все seed — easy)")
	}
}

func TestStoreSaveAndPreferGenerated(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "numeracy.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close()

	gen := &Task{
		Category: CategoryPercent, Level: LevelEasy, Kind: AnswerNumeric, Unit: UnitPercent,
		Expected: 15, Tolerance: 1, Question: "Сгенерированная экспериментальная задача на скидку.", Explain: "Объяснение задачи.",
		Source: SourceGenerated,
	}
	id, err := st.Save(gen)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id == 0 {
		t.Fatal("id задачи не назначен")
	}

	// Задача из базы должна предпочитать сгенерированные (a не seed).
	got, err := st.Task(CategoryPercent, LevelEasy)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if got.Source != SourceGenerated || got.ID != gen.ID {
		t.Errorf("ожидали сгенерированную задачу, получили %s (id=%d)", got.Source, got.ID)
	}
}

// --- AI-генерация (парсинг и валидация) ---

func TestParseGeneratedTask(t *testing.T) {
	text := `Вот задача:
{"category":"probability","level":"easy","kind":"numeric","unit":"percent","expected":25,"tolerance":1,
 "question":"Монетку бросают дважды, какова вероятность двух орлов?","explanation":"P = 1/4 = 25%."}`
	task, err := parseGeneratedTask(text)
	if err != nil {
		t.Fatalf("parseGeneratedTask: %v", err)
	}
	if task.Category != CategoryProbability || task.Kind != AnswerNumeric || task.Expected != 25 {
		t.Errorf("задача: %+v", task)
	}
}

func TestParseGeneratedTaskInvalid(t *testing.T) {
	if _, err := parseGeneratedTask("не JSON"); err == nil {
		t.Error("ожидали ошибку для не-JSON")
	}
	if _, err := parseGeneratedTask(`{"category":"percent","level":"easy","question":""}`); err == nil {
		t.Error("ожидали ошибку для пустого вопроса")
	}
	if _, err := parseGeneratedTask(`{"category":"percent","level":"easy","kind":"choice","question":"Вопрос без вариантов","options":["один"],"correct_idx":0,"explanation":"Объяснение задачи длинное и понятное."}`); err == nil {
		t.Error("ожидали ошибку для MC с одним вариантом")
	}
}

// --- Проверка ответов ---

func TestCheckerPercent(t *testing.T) {
	task := &Task{Kind: AnswerNumeric, Unit: UnitPercent, Expected: 28, Tolerance: 1}
	for in, want := range map[string]bool{"28": true, "28%": true, "0.28": true, "30": false, "20": false} {
		if got := CheckAnswer(task, in).Correct; got != want {
			t.Errorf("CheckAnswer(%q) = %v, ожидали %v", in, got, want)
		}
	}
}

func TestCheckerBayesFraction(t *testing.T) {
	task := &Task{Kind: AnswerNumeric, Unit: UnitPercent, Expected: 16.7, Tolerance: 2}
	for _, in := range []string{"16.7", "16,7", "17", "1/6", "0.167"} {
		if !CheckAnswer(task, in).Correct {
			t.Errorf("CheckAnswer(%q) должно быть верным", in)
		}
	}
	if CheckAnswer(task, "50").Correct {
		t.Error("«50» не должно быть верным")
	}
}

func TestCheckerChoice(t *testing.T) {
	task := &Task{Kind: AnswerChoice, Options: []string{"Сменить дверь", "Остаться", "Без разницы"}, CorrectIdx: 0}
	if !CheckAnswer(task, "1").Correct {
		t.Error("номер 1 должен быть верным")
	}
	if !CheckAnswer(task, "Сменить дверь").Correct {
		t.Error("текст варианта должен быть верным")
	}
	if CheckAnswer(task, "Остаться").Correct {
		t.Error("«Остаться» не должно быть верным")
	}
}

func TestParseNumberInput(t *testing.T) {
	for in, want := range map[string]float64{"1/6": 0.1667, "28%": 28, "16,7": 16.7, "1 000": 1000} {
		got, ok := parseNumberInput(in)
		if !ok || math.Abs(got-want) > 0.001 {
			t.Errorf("parseNumberInput(%q) = %v,%v; ожидали %v", in, got, ok, want)
		}
	}
}

// --- Валидация задач ---

func TestValidateTask(t *testing.T) {
	valid := &Task{Category: CategoryPercent, Level: LevelEasy, Kind: AnswerNumeric,
		Expected: 25, Tolerance: 1, Question: "Вопрос достаточно длинный для задачи.", Explain: "Объяснение достаточно длинное."}
	if err := validateTask(valid); err != nil {
		t.Errorf("валидная задача отклонена: %v", err)
	}
	bad := *valid
	bad.Question = "короче"
	if err := validateTask(&bad); err == nil {
		t.Error("короткий вопрос должен быть отклонён")
	}
}

// --- Самопроверка генерации (без LLM) ---

func TestMathClose(t *testing.T) {
	if !mathClose(16.7, 16.65, 0.5) {
		t.Error("16.7 и 16.65 должны быть близки в пределах 0.5")
	}
	if mathClose(12.5, -4, 1) {
		t.Error("12.5 и −4 НЕ должны считаться близкими")
	}
}

func TestParseIntFirst(t *testing.T) {
	if n, ok := parseIntFirst("вариант 2"); !ok || n != 2 {
		t.Errorf("parseIntFirst(«вариант 2») = %d,%v; ожидали 2", n, ok)
	}
	if _, ok := parseIntFirst("нет цифр"); ok {
		t.Error("ожидали false без цифр")
	}
	if n, ok := parseIntFirst("-4%"); !ok || n != -4 {
		t.Errorf("parseIntFirst(«-4%%») = %d,%v; ожидали -4", n, ok)
	}
}

// --- nextTask: генерация падает → задача из базы, с логом ---

func TestNextTaskFromDBOnNoLLM(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "numeracy.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close()

	var logs []string
	task := nextTask(nil, st, CategoryScale, LevelEasy, testRNG(), func(msg string) { logs = append(logs, msg) })
	if task == nil {
		t.Fatal("задача должна быть взята из базы")
	}
	if task.Source != SourceSeed {
		t.Errorf("без LLM ожидали seed-задачу, получили %s", task.Source)
	}
	found := false
	for _, l := range logs {
		if strings.Contains(l, "[база]") && strings.Contains(l, "взята из базы") {
			found = true
		}
	}
	if !found {
		t.Errorf("в логах должна быть пометка «задача взята из базы»: %v", logs)
	}
}

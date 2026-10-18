package main

import (
	"bufio"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aichallenge/llm"
)

// Счёт и лог сессии.
type session struct {
	Correct int
	Total   int
	Lines   []string
}

func main() {
	category := flag.String("category", "all", "категория: percent|probability|scale|all")
	level := flag.String("level", "all", "сложность: easy|hard|all")
	sheet := flag.Int("sheet", 0, "режим листа: сгенерировать N задач с ответами и объяснениями в results/")
	dbPath := flag.String("db", "", "путь к SQLite-базе задач (по умолчанию results/numeracy.db)")
	flag.Parse()

	client, err := llm.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[llm недоступен: %v — задачи будут браться из базы]\n", err)
		client = nil
	} else {
		fmt.Printf("[модель: %s]\n", client.Model())
	}

	cat := Category(strings.ToLower(strings.TrimSpace(*category)))
	if !validCategory(cat) {
		fmt.Printf("Неизвестная категория %q; доступны: percent, probability, scale, all\n", cat)
		cat = CategoryPercent
	}
	lvl := Level(strings.ToLower(strings.TrimSpace(*level)))
	if !validLevel(lvl) {
		fmt.Printf("Неизвестный уровень %q; доступны: easy, hard, all\n", lvl)
		lvl = "all"
	}

	store, err := OpenStore(*dbPath)
	if err != nil {
		die(err)
	}
	defer store.Close()
	fmt.Printf("[база задач: %s]\n", store.Path())

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	if *sheet > 0 {
		runSheet(client, store, cat, lvl, *sheet, rng)
		return
	}
	runREPL(client, store, cat, lvl, rng)
}

// nextTask — сначала ПЫТАЕМСЯ сгенерировать задачу (AI) и сохранить её в базу;
// если генерация не произошла (нет LLM/невалидный JSON) — задача берётся из
// базы, и это отражается в логах (source=generated|seed).
func nextTask(client *llm.Client, store *Store, cat Category, lvl Level, rng *rand.Rand, logf func(string)) *Task {
	active := cat
	if active == "all" {
		active = categories[rng.Intn(len(categories))]
	}
	// Для генерации нужен конкретный уровень (в базе «all» — только фильтр).
	genLvl := lvl
	if genLvl == "all" {
		if rng.Intn(2) == 0 {
			genLvl = LevelEasy
		} else {
			genLvl = LevelHard
		}
	}
	if t, ok, reason := generateTask(client, active, genLvl); ok {
		id, err := store.Save(t)
		if err != nil {
			logf(fmt.Sprintf("[генерация] задача не сохранена в базу (%v), отдаю из памяти", err))
		} else {
			logf(fmt.Sprintf("[генерация] НОВАЯ задача %s/%s сгенерирована, проверена и сохранена в базу (id=%d)", t.Category, t.Level, id))
		}
		return t
	} else if reason != "" {
		logf(fmt.Sprintf("[генерация] отклонено: %s", reason))
	}
	t, err := store.Task(active, lvl)
	if err != nil {
		logf(fmt.Sprintf("[база] генерация не удалась и база пуста для %s/%s: %v", active, lvl, err))
		return nil
	}
	logf(fmt.Sprintf("[база] задача взята из базы (id=%d, источник=%s)", t.ID, t.Source))
	return t
}

// runREPL — интерактивный тренажёр: задача (сгенерированная или из базы) →
// ответ → проверка по задаче → объяснение → счёт.
func runREPL(client *llm.Client, store *Store, cat Category, lvl Level, rng *rand.Rand) {
	fmt.Println("Тренажёр количественной грамотности: проценты, вероятности, порядки величин.")
	fmt.Println("Команды: /stop — выйти, /skip — пропустить, /category <имя> — сменить категорию.")
	fmt.Println("Для вариантов ответа введите номер (1/2/3) или текст варианта.")

	sess := &session{}
	logf := sink(sess)

	sc := bufio.NewScanner(os.Stdin)
	for {
		t := nextTask(client, store, cat, lvl, rng, logf)
		if t == nil {
			break
		}
		fmt.Printf("\n[%s/%s] %s\n", t.Category, t.Level, t.Question)
		if t.Kind == AnswerChoice {
			for i, opt := range t.Options {
				fmt.Printf("  %d) %s\n", i+1, opt)
			}
		}
		fmt.Print("Ответ: ")
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "/stop" || line == "/exit":
			printScore(sess)
			saveSession(sess, cat)
			return
		case line == "/skip":
			sess.Total++
			exp := GenerateExplanation(client, t, "(пропущено)", false)
			fmt.Println("Правильный ответ:", fmtExpected(t))
			fmt.Println("Объяснение:", exp)
			sess.Lines = append(sess.Lines, "skip | "+t.Question+" | "+fmtExpected(t))
			continue
		case strings.HasPrefix(line, "/category"):
			parts := strings.Fields(line)
			if len(parts) > 1 {
				cat = Category(strings.ToLower(parts[1]))
				if !validCategory(cat) {
					fmt.Println("[неизвестная категория:", cat, "]")
					cat = CategoryPercent
				} else {
					fmt.Println("[категория:", cat, "]")
				}
			}
			continue
		}

		res := CheckAnswer(t, line)
		sess.Total++
		exp := GenerateExplanation(client, t, line, res.Correct)
		if res.Correct {
			sess.Correct++
			fmt.Println("✅ Верно!")
		} else {
			fmt.Println("❌ Неверно. Правильный ответ:", res.Expected)
		}
		fmt.Println("Объяснение:", exp)
		sess.Lines = append(sess.Lines, fmt.Sprintf("%v | %s | ответ: %q | правильный: %s",
			res.Correct, t.Question, line, res.Expected))
	}
}

// sink — обёртка, чтобы logf писал и в сессию (в runREPL).
func sink(s *session) func(string) {
	return func(msg string) {
		fmt.Println(msg)
		s.Lines = append(s.Lines, msg)
	}
}

// runSheet — batch: лист задач (сгенерированных или из базы) → results/ + печать.
func runSheet(client *llm.Client, store *Store, cat Category, lvl Level, n int, rng *rand.Rand) {
	if err := os.MkdirAll("results", 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "results:", err)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Лист задач: количественная грамотность (категория %s, уровень %s)\n\n", cat, lvl)

	logf := func(msg string) {
		fmt.Println(msg)
		sb.WriteString("> " + msg + "\n")
	}
	for i := 0; i < n; i++ {
		t := nextTask(client, store, cat, lvl, rng, logf)
		if t == nil {
			break
		}
		fmt.Fprintf(&sb, "## %d. [%s/%s] %s\n\n", i+1, t.Category, t.Level, t.Question)
		if t.Kind == AnswerChoice {
			for j, opt := range t.Options {
				fmt.Fprintf(&sb, "- %d) %s\n", j+1, opt)
			}
			fmt.Fprintln(&sb)
		}
		fmt.Fprintf(&sb, "**Ответ:** %s\n\n", fmtExpected(t))
		exp := GenerateExplanation(client, t, "(лист)", true)
		fmt.Fprintf(&sb, "**Объяснение:** %s\n\n", exp)
	}
	path := filepath.Join("results", "worksheet_"+string(cat)+".md")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "запись листа:", err)
	}
	fmt.Print(sb.String())
	fmt.Printf("Лист сохранён: %s\n", path)
}

// printScore печатает итог сессии.
func printScore(s *session) {
	fmt.Printf("\nИтог: %d/%d верных (%.0f%%)\n", s.Correct, s.Total, pct(s.Correct, s.Total))
}

// saveSession сохраняет лог сессии в results/ (gitignored).
func saveSession(s *session, cat Category) {
	_ = os.MkdirAll("results", 0o755)
	path := filepath.Join("results", fmt.Sprintf("session_%s_%d.log", cat, time.Now().Unix()))
	var sb strings.Builder
	fmt.Fprintf(&sb, "Сессия numeracy (категория %s): %d/%d верных\n", cat, s.Correct, s.Total)
	for _, l := range s.Lines {
		sb.WriteString(l + "\n")
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err == nil {
		fmt.Println("Лог сессии:", path)
	}
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) * 100 / float64(b)
}

// die печатает ошибку и выходит.
func die(err error) {
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	os.Exit(1)
}

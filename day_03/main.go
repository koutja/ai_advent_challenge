// Day 03 — Разные способы рассуждения.
//
// Одна аналитическая задача решается через API четырьмя способами:
//   - "--direct":      прямой ответ без доп. инструкций;
//   - "--step":        добавить «решай пошагово»;
//   - "--prompt-gen":  модель сначала составляет промпт, затем решает по нему;
//   - "--panel":       группа экспертов (аналитик, инженер, критик);
//   - "--run-all":     все четыре + сравнение.
//
// Клиент, чтение .env и вызов /chat/completions — в общем пакете llm (../llm).
package main

import (
	"fmt"
	"os"
	"strings"

	"aichallenge/llm"
)

const (
	// Аналитическая задача по умолчанию (переопределяется аргументом).
	defaultTask = "По описанию найди песню: культовый трек конца 60-х группы " +
		"из Ливерпуля, в названии — цитата о «не волнуйся», широко известный по " +
		"альбому 1970 года. Обоснуй выбор: название, исполнитель, жанр, год выхода."
)

// panelExpert описывает одного эксперта в панели.
type panelExpert struct {
	Name string
	Role string
}

var panelExperts = []panelExpert{
	{Name: "Аналитик", Role: "Ты — опытный аналитик музыкальной индустрии. Разбери подсказку по фактам: определи ключевые маркеры (группа, эпоха, название). Дай обоснованный вывод: название, исполнитель, жанр, год выхода."},
	{Name: "Инженер", Role: "Ты — технический инженер по данным о музыке. Сформулируй критерии проверки гипотезы, сопоставь с известными фактами и выдай финальный ответ структурно: song/artist/genre/year."},
	{Name: "Критик", Role: "Ты — придирчивый критик. Проверь, что вывод не противоречит описанию, укажи возможные альтернативы и подтверди или опровергни итоговую версию."},
}

type strategyResult struct {
	Name     string
	Text     string
	Song     string
	Artist   string
	Genre    string
	Year     string
	Detailed bool
}

func main() {
	client, err := llm.New()
	if err != nil {
		die("%v", err)
	}

	mode, task, err := parseArgs(os.Args[1:])
	if err != nil {
		die("Ошибка: %v", err)
	}

	switch mode {
	case "direct":
		runDirect(client, task)
	case "step":
		runStep(client, task)
	case "prompt-gen":
		runPromptGen(client, task)
	case "panel":
		runPanel(client, task)
	case "run-all":
		runAll(client, task)
	default:
		die("Неизвестный режим: %s", mode)
	}
}

func runDirect(client *llm.Client, task string) {
	fmt.Println("=== Стратегия: ПРЯМОЙ ОТВЕТ ===")
	fmt.Println("Задача:", task)
	fmt.Println("Доп. инструкций нет. ---")
	out, err := client.Chat([]llm.Message{{Role: "user", Content: task}}, nil)
	if err != nil {
		die("Ошибка (direct): %v", err)
	}
	fmt.Println(out)
}

func runStep(client *llm.Client, task string) {
	fmt.Println("=== Стратегия: ПОШАГОВОЕ РЕШЕНИЕ ===")
	fmt.Println("Задача:", task)
	fmt.Println("Добавлено: «решай пошагово». ---")
	prompt := task + "\n\nРешай пошагово: сначала определи факты, затем сделай вывод. Объясни каждый шаг."
	out, err := client.Chat([]llm.Message{{Role: "user", Content: prompt}}, nil)
	if err != nil {
		die("Ошибка (step): %v", err)
	}
	fmt.Println(out)
}

func runPromptGen(client *llm.Client, task string) {
	fmt.Println("=== Стратегия: МОДЕЛЬ СОСТАВЛЯЕТ ПРОМПТ ===")
	fmt.Println("Шаг 1: просим модель написать промпт для решения задачи.")

	genPrompt := "Составь подробный, самостоятельный промпт, который решит следующую задачу. " +
		"Промпт должен быть обращён ко второй модели и содержать всю информацию и требования к формату ответа (song/artist/genre/year). " +
		"Верни ТОЛЬКО текст промпта.\n\nЗадача:\n" + task

	gen, err := client.Chat([]llm.Message{{Role: "user", Content: genPrompt}}, nil)
	if err != nil {
		die("Ошибка (генерация промпта): %v", err)
	}
	fmt.Printf("\n--- Сгенерированный промпт ---\n%s\n\n", gen)

	fmt.Println("Шаг 2: отправляем сгенерированный промпт как запрос.")
	out, err := client.Chat([]llm.Message{{Role: "user", Content: gen}}, nil)
	if err != nil {
		die("Ошибка (prompt-gen): %v", err)
	}
	fmt.Println("--- Решение ---")
	fmt.Println(out)
}

func runPanel(client *llm.Client, task string) {
	fmt.Println("=== Стратегия: ГРУППА ЭКСПЕРТОВ (аналитик, инженер, критик) ===")
	fmt.Println("Задача:", task)

	out, err := client.Chat(panelMessages(task), &llm.Options{Temperature: float64Ptr(0.6)})
	if err != nil {
		die("Ошибка (panel): %v", err)
	}

	for _, s := range splitByRoles(out) {
		fmt.Println("---", s.title, "---")
		fmt.Println(s.body)
	}
}

func runAll(client *llm.Client, task string) {
	fmt.Println("=== ЗАПУСК ВСЕХ ЧЕТЫРЁХ СТРАТЕГИЙ ===")
	fmt.Println()

	results := make([]strategyResult, 0, 4)
	results = append(results, runStrategyQuiet(client, "direct", task))
	results = append(results, runStrategyQuiet(client, "step", task))
	results = append(results, runStrategyQuiet(client, "prompt-gen", task))
	results = append(results, runStrategyQuiet(client, "panel", task))

	for i := range results {
		extractFields(&results[i])
	}

	for _, r := range results {
		fmt.Printf("\n=== %s ===\n%s\n", r.Name, r.Text)
	}

	fmt.Println("\n--- Таблица сравнения ---")
	fmt.Printf("%-22s | %-28s | %-22s | %-22s | %-22s\n", "Стратегия", "song", "artist", "genre", "year")
	fmt.Println(strings.Repeat("-", 128))
	for _, r := range results {
		fmt.Printf("%-22s | %-28s | %-22s | %-22s | %-22s\n",
			r.Name, qd(r.Song), qd(r.Artist), qd(r.Genre), qd(r.Year))
	}

	fmt.Printf("\n%-22s | %-22s\n", "Стратегия", "Пошаговое рассуждение")
	fmt.Println(strings.Repeat("-", 48))
	for _, r := range results {
		fmt.Printf("%-22s | %-22s\n", r.Name, yesno(r.Detailed))
	}

	verdict(results)
}

// panelMessages собирает сообщения панели экспертов.
func panelMessages(task string) []llm.Message {
	messages := make([]llm.Message, 0, len(panelExperts)+1)
	for _, e := range panelExperts {
		messages = append(messages, llm.Message{Role: "system", Content: e.Role})
	}
	return append(messages, llm.Message{Role: "user", Content: task})
}

func verdict(results []strategyResult) {
	bestScore := -1
	bestName := ""
	for _, r := range results {
		score := 0
		if r.Song != "" {
			score++
		}
		if r.Artist != "" {
			score++
		}
		if r.Genre != "" {
			score++
		}
		if r.Year != "" {
			score++
		}
		if r.Detailed {
			score++
		}
		if score > bestScore {
			bestScore = score
			bestName = r.Name
		}
	}
	fmt.Printf("\nВердикт: наиболее точный результат дала стратегия %q (полнота ответа %d/5).\n",
		bestName, bestScore)
}

// runStrategyQuiet выполняет стратегию и возвращает структуру с текстом ответа.
func runStrategyQuiet(client *llm.Client, mode, task string) strategyResult {
	switch mode {
	case "direct":
		out, err := client.Chat([]llm.Message{{Role: "user", Content: task}}, nil)
		if err != nil {
			die("Ошибка (direct): %v", err)
		}
		return strategyResult{Name: "direct", Text: out, Detailed: false}
	case "step":
		prompt := task + "\n\nРешай пошагово: сначала определи факты, затем сделай вывод. Объясни каждый шаг."
		out, err := client.Chat([]llm.Message{{Role: "user", Content: prompt}}, nil)
		if err != nil {
			die("Ошибка (step): %v", err)
		}
		return strategyResult{Name: "step", Text: out, Detailed: true}
	case "prompt-gen":
		genPrompt := "Составь подробный, самостоятельный промпт, который решит следующую задачу. " +
			"Промпт должен быть обращён ко второй модели и содержать всю информацию и требования к формату ответа (song/artist/genre/year). " +
			"Верни ТОЛЬКО текст промпта.\n\nЗадача:\n" + task
		gen, err := client.Chat([]llm.Message{{Role: "user", Content: genPrompt}}, nil)
		if err != nil {
			die("Ошибка (генерация промпта): %v", err)
		}
		out, err := client.Chat([]llm.Message{{Role: "user", Content: gen}}, nil)
		if err != nil {
			die("Ошибка (prompt-gen): %v", err)
		}
		return strategyResult{Name: "prompt-gen", Text: out, Detailed: true}
	case "panel":
		out, err := client.Chat(panelMessages(task), &llm.Options{Temperature: float64Ptr(0.6)})
		if err != nil {
			die("Ошибка (panel): %v", err)
		}
		var b strings.Builder
		for _, s := range splitByRoles(out) {
			b.WriteString(s.title)
			b.WriteString(": ")
			b.WriteString(s.body)
			b.WriteString("\n")
		}
		return strategyResult{Name: "panel", Text: b.String(), Detailed: true}
	}
	return strategyResult{}
}

// extractFields пытается найти значения полей в тексте.
func extractFields(r *strategyResult) {
	r.Song = findValue(r.Text, "song")
	r.Artist = findValue(r.Text, "artist")
	r.Genre = findValue(r.Text, "genre")
	r.Year = findValue(r.Text, "year")
}

// findValue ищет "key": "value" или "key: value" в тексте.
func findValue(text, key string) string {
	patterns := []string{`"` + key + `"`, key + ":", key}
	for _, p := range patterns {
		if i := strings.Index(text, p); i >= 0 {
			rest := text[i+len(p):]
			rest = strings.TrimLeft(rest, ` ":`)
			if end := strings.IndexAny(rest, `",\n}`); end >= 0 {
				v := strings.TrimSpace(rest[:end])
				if v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// segment — заголовок и тело одной части ответа панели.
type segment struct {
	title string
	body  string
}

// splitByRoles разделяет ответ панели по заголовкам экспертов.
func splitByRoles(out string) []segment {
	lower := strings.ToLower(out)
	indexes := []int{}
	titles := []string{}
	for _, e := range panelExperts {
		for _, marker := range []string{e.Name, strings.ToLower(e.Name)} {
			if i := strings.Index(lower, strings.ToLower(marker)); i >= 0 && i < len(out) {
				indexes = append(indexes, i)
				titles = append(titles, e.Name)
				break
			}
		}
	}
	if len(indexes) == 0 {
		return []segment{{title: "Решение", body: out}}
	}
	order := argsort(indexes)
	segs := make([]segment, 0, len(order))
	for k, idx := range order {
		end := len(out)
		if k+1 < len(order) {
			end = indexes[order[k+1]]
		}
		segs = append(segs, segment{title: titles[idx], body: strings.TrimSpace(out[idx:end])})
	}
	return segs
}

func argsort(idxs []int) []int {
	res := make([]int, len(idxs))
	for i := range res {
		res[i] = i
	}
	for i := 1; i < len(res); i++ {
		for j := i; j > 0 && idxs[res[j]] < idxs[res[j-1]]; j-- {
			res[j], res[j-1] = res[j-1], res[j]
		}
	}
	return res
}

func float64Ptr(v float64) *float64 { return &v }

func qd(v string) string {
	if v == "" {
		return "-"
	}
	return "'" + v + "'"
}

func yesno(b bool) string {
	if b {
		return "да"
	}
	return "нет"
}

func parseArgs(args []string) (mode, task string, err error) {
	if len(args) == 0 {
		return "", "", fmt.Errorf("не указан режим. Используйте --direct | --step | --prompt-gen | --panel | --run-all")
	}
	first := args[0]
	rest := args[1:]
	switch first {
	case "--direct":
		mode = "direct"
	case "--step":
		mode = "step"
	case "--prompt-gen":
		mode = "prompt-gen"
	case "--panel":
		mode = "panel"
	case "--run-all":
		mode = "run-all"
	default:
		return "", "", fmt.Errorf("неизвестный флаг %q", first)
	}
	if len(rest) > 0 {
		task = strings.Join(rest, " ")
	} else {
		task = defaultTask
	}
	return mode, task, nil
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

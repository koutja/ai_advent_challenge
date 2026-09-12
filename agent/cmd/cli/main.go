// Команда cli — консольный (REPL) интерфейс к ядру агента.
//
// Запуск:  go run ./cmd/cli
// Флаги:   --config <путь>   путь к config.json
//
//	--reset           сбросить историю перед стартом
package main

import (
	"aichallenge/llm"
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"agent"
)

func main() {
	cfgPath := flag.String("config", "config.json", "путь к config.json")
	reset := flag.Bool("reset", false, "сбросить историю перед стартом")
	stats := flag.Bool("stats", false, "прогнать сравнение токенов: короткий/длинный/переполненный диалог")
	compressMode := flag.String("compress", "", "сжатие истории: on|off (перекрывает config.json)")
	compare := flag.Bool("compare", false, "сравнить ответ и токены со сжатием и без")
	flag.Parse()

	cfg, err := agent.LoadConfig(*cfgPath)
	if err != nil {
		die(err)
	}

	// Этап 2: история сохраняется в SQLite (cfg.HistoryFile) и переживает перезапуск.
	mem, err := agent.NewSQLiteMemory(cfg.HistoryFile)
	if err != nil {
		die(err)
	}
	defer mem.Close()
	if *reset {
		_ = mem.Reset()
	}

	ag, err := agent.New(cfg, mem)
	if err != nil {
		die(err)
	}

	if *compare {
		runCompare(ag)
		return
	}
	switch *compressMode {
	case "on":
		ag.SetCompress(true)
	case "off":
		ag.SetCompress(false)
	}
	if *stats {
		runStats(ag)
		return
	}

	comp := "выкл"
	if ag.CompressionEnabled() {
		comp = "вкл"
	}
	fmt.Printf("Агент запущен (модель %s, история: %s, сжатие: %s).\n", ag.Client().Model(), cfg.HistoryFile, comp)
	fmt.Println("Команды: /reset — очистить историю, /compress — переключить сжатие, /exit — выход.")

	printHistory(ag)
	sc := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			fmt.Print("> ")
			continue
		}
		switch line {
		case "/exit", "/quit", "/q":
			return
		case "/reset":
			_ = mem.Reset()
			fmt.Println("[история сброшена]")
			fmt.Print("> ")
			continue
		case "/compress":
			if ag.CompressionEnabled() {
				ag.SetCompress(false)
				fmt.Println("[сжатие: выкл]")
			} else {
				ag.SetCompress(true)
				fmt.Println("[сжатие: вкл]")
			}
			fmt.Print("> ")
			continue
		}
		// Показываем, что LLM готовит ответ (спиннер на той же строке).
		stop := make(chan struct{})
		go spin(stop)
		reply, err := ag.Say(line)
		close(stop)
		clearLine(40)

		if err != nil {
			fmt.Printf("ошибка: %v\n", err)
		} else {
			fmt.Println(reply.Text)
			printStats(reply.Stats)
		}
		fmt.Print("> ")
	}
	if err := sc.Err(); err != nil {
		die(err)
	}
}

// spin рисует индикатор «Агент думает <спиннер>», пока канал stop не закрыт.
func spin(stop <-chan struct{}) {
	frames := []string{"|", "/", "-", "\\"}
	i := 0
	for {
		select {
		case <-stop:
			return
		default:
		}
		fmt.Printf("\rАгент думает %s", frames[i%len(frames)])
		i++
		time.Sleep(120 * time.Millisecond)
	}
}

// clearLine затирает текущую строку консоли, чтобы убрать спиннер.
func clearLine(width int) {
	fmt.Printf("\r%s\r", strings.Repeat(" ", width))
}

// printStats выводит метрики токенов и стоимости одного хода.
func printStats(st *agent.TokenStats) {
	if st == nil {
		return
	}
	line := fmt.Sprintf("  [токены] история ~%d | запрос %d | ответ %d",
		st.HistoryTokens, st.RequestTokens, st.ResponseTokens)
	if st.TotalTokens > 0 {
		line += fmt.Sprintf(" | всего %d", st.TotalTokens)
	}
	if st.CostKnown {
		line += fmt.Sprintf(" | стоимость $%.6f", st.CostUSD)
	} else {
		line += " | стоимость —"
	}
	fmt.Println(line)
}

// printHistory выводит сохранённую в памяти историю диалога перед стартом REPL.
func printHistory(ag *agent.Agent) {
	hist, err := ag.Memory().Load()
	if err != nil || len(hist) == 0 {
		return
	}
	for _, m := range hist {
		who := "Агент"
		if m.Role == "user" {
			who = "Вы"
		}
		fmt.Printf("%s: %s\n", who, m.Content)
	}
	fmt.Println("---")
}

// runStats прогоняет три сценария (короткий/длинный/переполненный диалог) против
// реальной модели и печатает таблицу: как растут токены/стоимость по мере диалога
// и что происходит при превышении лимита (маленький max_tokens / огромная история).
func runStats(ag *agent.Agent) {
	short := []llm.Message{{Role: "user", Content: "Привет! Коротко: что такое тест?"}}
	long := buildDialog(24, 120)     // ~длинный диалог
	overflow := buildDialog(60, 400) // очень длинная история, давим на контекст

	scenarios := []struct {
		name string
		msgs []llm.Message
		opts *llm.Options
	}{
		{"короткий", short, nil},
		{"длинный", long, &llm.Options{MaxTokens: 64}},
		{"переполненный", overflow, &llm.Options{MaxTokens: 8}}, // жёсткий лимит вывода + большая история
	}

	fmt.Println("\n=== Сравнение токенов: короткий / длинный / переполненный ===")
	for _, s := range scenarios {
		histT := agent.MessagesTokens(s.msgs)
		res, err := ag.Client().ChatResult(s.msgs, s.opts)
		if err != nil {
			fmt.Printf("%-14s история ~%-7d статус: ОШИБКА — %v\n", s.name, histT, err)
			continue
		}
		costLine := "—"
		if p, ok := ag.Prices()[res.Model]; ok {
			costLine = fmt.Sprintf("$%.6f", agent.Cost(p, res.PromptTokens, res.CompletionTokens))
		}
		fmt.Printf("%-14s история ~%-7d запрос %-6d ответ %-6d всего %-6d стоимость %s\n",
			s.name, histT, res.PromptTokens, res.CompletionTokens, res.TotalTokens, costLine)
	}
	fmt.Println()
}

// buildDialog строит фиктивный диалог из turns реплик по ~words слов каждая.
func buildDialog(turns, words int) []llm.Message {
	part := strings.Repeat("слово ", words)
	var msgs []llm.Message
	for i := 0; i < turns; i++ {
		msgs = append(msgs,
			llm.Message{Role: "user", Content: part},
			llm.Message{Role: "assistant", Content: part},
		)
	}
	return msgs
}

// runCompare прогоняет один запрос на одной и той же истории дважды: без сжатия
// и со сжатием — и печатает, сколько токенов/стоимости экономит компрессия.
func runCompare(ag *agent.Agent) {
	const input = "Повтори кратко главную тему и ключевые факты нашего разговора."
	hist := buildDialog(40, 100) // достаточно большая история для наглядного сравнения

	client := ag.Client()
	prices := ag.Prices()

	fmt.Println("\n=== Сравнение: без сжатия / со сжатием ===")

	full := append(append([]llm.Message{}, hist...), llm.Message{Role: "user", Content: input})
	resFull, errFull := client.ChatResult(full, &llm.Options{MaxTokens: 64})

	cm := agent.NewContextManager(client, 10, 10)
	compressed := cm.Build(hist, input)
	resCmp, errCmp := client.ChatResult(compressed, &llm.Options{MaxTokens: 64})

	printCompRow("без сжатия", full, resFull, errFull, prices)
	printCompRow("со сжатием", compressed, resCmp, errCmp, prices)

	if errFull == nil && errCmp == nil {
		saved := resFull.TotalTokens - resCmp.TotalTokens
		pct := 0.0
		if resFull.TotalTokens > 0 {
			pct = float64(saved) / float64(resFull.TotalTokens) * 100
		}
		fmt.Printf("\nЭкономия токенов: %d (%.0f%%)\n", saved, pct)
	}
	fmt.Println()
}

// printCompRow печатает одну строку сравнения (запрос + метрики).
func printCompRow(label string, msgs []llm.Message, res *llm.Result, err error, prices map[string]agent.Price) {
	est := agent.MessagesTokens(msgs)
	if err != nil {
		fmt.Printf("%-12s история ~%-6d статус: ОШИБКА — %v\n", label, est, err)
		return
	}
	cost := "—"
	if p, ok := prices[res.Model]; ok {
		cost = fmt.Sprintf("$%.6f", agent.Cost(p, res.PromptTokens, res.CompletionTokens))
	}
	fmt.Printf("%-12s история ~%-6d запрос %-6d ответ %-6d всего %-6d стоимость %s\n",
		label, est, res.PromptTokens, res.CompletionTokens, res.TotalTokens, cost)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	os.Exit(1)
}

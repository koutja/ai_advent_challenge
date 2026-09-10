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

	if *stats {
		runStats(ag)
		return
	}

	fmt.Printf("Агент запущен (модель %s, история: %s).\n", ag.Client().Model(), cfg.HistoryFile)
	fmt.Println("Команды: /reset — очистить историю, /exit — выход.")

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

func die(err error) {
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	os.Exit(1)
}

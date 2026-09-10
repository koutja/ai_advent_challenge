// Команда cli — консольный (REPL) интерфейс к ядру агента.
//
// Запуск:  go run ./cmd/cli
// Флаги:   --config <путь>   путь к config.json
//
//	--reset           сбросить историю перед стартом
package main

import (
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

func die(err error) {
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	os.Exit(1)
}

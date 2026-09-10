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

	mem := agent.NewInMemory() // Этап 1: память в RAM; на Этапе 2 заменим на SQLite
	if *reset {
		_ = mem.Reset()
	}

	ag, err := agent.New(cfg, mem)
	if err != nil {
		die(err)
	}

	fmt.Printf("Агент запущен (модель %s). Введите сообщение или /exit.\n", ag.Client().Model())
	fmt.Println("Команды: /reset — очистить историю, /exit — выход.")

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
		reply, err := ag.Say(line)
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

func die(err error) {
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	os.Exit(1)
}

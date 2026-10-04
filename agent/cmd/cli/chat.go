package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"agent"
	"agent/feature/memory"
)

// runChat — мини-чат с RAG + памятью задачи (День 25, режим отладки в CLI).
//
// REPL: каждое сообщение пользователя → Agent.SayChat (ретрив контекста по
// текущему вопросу + история + память задачи + строгий формат с источниками).
// Под ответом печатается блок найденных источников. Команды:
//
//	/goal <текст>  — переопределить цель диалога
//	/facts         — показать факты сессии (память задачи)
//	/reset         — сбросить контекст (новый диалог, новая цель)
//	/exit          — выход
func runChat(cfg *agent.Config) {
	mem, err := memory.NewLayeredSQLite(cfg.HistoryFile, cfg.LongMemoryFile)
	if err != nil {
		die(err)
	}
	defer mem.Close()

	ag, err := agent.New(cfg, mem)
	if err != nil {
		die(err)
	}
	if ag.Retriever() == nil {
		die(fmt.Errorf("RAG не настроен: добавьте секцию rag в config.json"))
	}

	fmt.Printf("Мини-чат с RAG (модель %s). Цель: первое сообщение.\n", ag.Client().Model())
	fmt.Println("Команды: /goal <текст>, /facts, /reset, /exit.")
	printChatHistory(ag)

	sc := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			fmt.Print("> ")
			continue
		}
		switch {
		case line == "/exit":
			return
		case strings.HasPrefix(line, "/goal"):
			text := strings.TrimSpace(strings.TrimPrefix(line, "/goal"))
			if text == "" {
				fmt.Printf("[цель: %s]\n", orText(ag.Goal(), "(не задана)"))
			} else {
				ag.SetGoal(text)
				fmt.Printf("[цель обновлена: %s]\n", ag.Goal())
			}
			fmt.Print("> ")
			continue
		case line == "/facts":
			printSessionFacts(ag)
			fmt.Print("> ")
			continue
		case line == "/reset":
			if err := ag.ResetContext(); err != nil {
				fmt.Println("ошибка сброса:", err)
			} else {
				fmt.Println("[контекст сброшен, новый диалог]")
			}
			fmt.Print("> ")
			continue
		}

		reply, err := ag.SayChat(line)
		if err != nil {
			fmt.Println("ошибка:", err)
			fmt.Print("> ")
			continue
		}
		fmt.Println(reply.Text)
		printChatSources(reply)
		fmt.Print("> ")
	}
}

// printChatSources печатает блок найденных RAG-источников под ответом.
func printChatSources(reply *agent.Reply) {
	if reply.Engine == "" || len(reply.Sources) == 0 {
		return
	}
	fmt.Printf("\n[RAG: движок %s, источников: %d", reply.Engine, len(reply.Sources))
	if reply.FormatOK {
		fmt.Print(", формат: ✓")
	}
	if reply.Unknown {
		fmt.Print(", режим «не знаю»")
	}
	fmt.Println("]")
	for i, s := range reply.Sources {
		fmt.Printf("  %d. %s | секция: %s | score=%.3f\n", i+1, s.Source, orText(s.Section, "—"), s.Score)
	}
}

// printSessionFacts печатает факты текущей сессии (память задачи).
func printSessionFacts(ag *agent.Agent) {
	facts := ag.SessionFacts()
	if len(facts) == 0 {
		fmt.Println("[память задачи пуста]")
		return
	}
	fmt.Println("[память задачи:]")
	for k, v := range facts {
		fmt.Printf("  %s: %s\n", k, v)
	}
}

// printChatHistory печатает сохранённую историю диалога (если есть).
func printChatHistory(ag *agent.Agent) {
	hist, err := ag.Memory().Load()
	if err != nil || len(hist) == 0 {
		return
	}
	fmt.Println("[история диалога:]")
	for _, m := range hist {
		role := "Пользователь"
		if m.Role == "assistant" {
			role = "Ассистент"
		}
		fmt.Printf("  %s: %s\n", role, truncateLine(m.Content, 100))
	}
}

func truncateLine(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// runChatScenarios — прогон 2 длинных сценариев мини-чата с автопроверкой
// (источники, формат, факты, «цель не потеряна»). Отчёт: results/chat_scenarios.md.
func runChatScenarios(cfg *agent.Config) {
	mem, err := memory.NewLayeredSQLite(cfg.HistoryFile, cfg.LongMemoryFile)
	if err != nil {
		die(err)
	}
	defer mem.Close()

	ag, err := agent.New(cfg, mem)
	if err != nil {
		die(err)
	}
	if ag.Retriever() == nil {
		die(fmt.Errorf("RAG не настроен: добавьте секцию rag в config.json"))
	}

	scenarios, err := agent.LoadChatScenarios("chat_scenarios.json")
	if err != nil {
		die(err)
	}
	fmt.Printf("Сценариев: %d (ходов: %v)\n", len(scenarios), turnCounts(scenarios))

	results := agent.RunChatScenarios(nil, ag, scenarios)
	report := agent.RenderChatScenarioReport(results)

	if err := os.MkdirAll("results", 0o755); err != nil {
		die(err)
	}
	path := "results/chat_scenarios.md"
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		die(err)
	}

	// Короткая сводка в консоль.
	for _, r := range results {
		fmt.Printf("%s (%s): ходов %d, источники %v, формат %v, факты %d/%d, цель %v\n",
			r.Scenario.ID, r.Scenario.Title, len(r.Turns),
			markStr(r.SourcesAllTurns), markStr(r.FormatAllTurns),
			r.FactsHits, r.FactsTotal, markStr(r.GoalPreserved))
	}
	fmt.Printf("Полный отчёт: %s\n", path)
}

func turnCounts(scenarios []agent.ChatScenario) []int {
	out := make([]int, len(scenarios))
	for i, s := range scenarios {
		out[i] = len(s.Turns)
	}
	return out
}

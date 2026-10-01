// Команда cli — консольный (REPL) интерфейс к ядру агента.
//
// Запуск:  go run ./cmd/cli
// Флаги:   --config <путь>   путь к config.json
//
//	--reset           сбросить историю перед стартом
package main

import (
	stdctx "context"

	"aichallenge/llm"
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"agent"
	"agent/feature/context"
	"agent/feature/dialog"
	"agent/feature/invariants"
	mcpx "agent/feature/mcp"
	"agent/feature/memory"
	"agent/feature/profile"
	"agent/feature/task"
)

func main() {
	cfgPath := flag.String("config", "config.json", "путь к config.json")
	reset := flag.Bool("reset", false, "сбросить историю перед стартом")
	stats := flag.Bool("stats", false, "прогнать сравнение токенов: короткий/длинный/переполненный диалог")
	compressMode := flag.String("compress", "", "сжатие истории: on|off (перекрывает config.json)")
	compare := flag.Bool("compare", false, "сравнить ответ и токены со сжатием и без")
	compareStrategies := flag.Bool("compare-strategies", false, "прогнать сценарий «собираем ТЗ» на всех 3 стратегиях контекста")
	compareMemory := flag.Bool("compare-memory", false, "сравнить ответы агента с долговременной памятью и без неё")
	compareProfiles := flag.Bool("compare-profiles", false, "сравнить ответы агента под разными профилями (terse vs detailed)")
	checkInvariants := flag.Bool("check-invariants", false, "прогнать демо: запрос конфликтует с инвариантом и отказ")
	mcpTools := flag.Bool("mcp-tools", false, "подключиться к MCP-серверу (bin/mcp-server) и вывести список инструментов")
	mcpCall := flag.String("mcp-call", "", "вызвать MCP-инструмент по имени (например get_task)")
	mcpArgs := flag.String("mcp-args", "", "JSON-аргументы инструмента для --mcp-call (опционально)")
	mcpDemo := flag.Bool("mcp-demo", false, "демо планировщика: напоминание + периодический сбор данных + сводка")
	mcpPipeline := flag.String("mcp-pipeline", "", "пайплайн: search → summarize → save_to_file по запросу")
	mcpOrchestrate := flag.String("mcp-orchestrate", "", "длинный флоу через несколько MCP-серверов по теме")
	ragQ := flag.String("rag", "", "RAG-ответ на вопрос: поиск чанков в индексе index_service + LLM с источниками")
	ragEval := flag.Bool("rag-eval", false, "сравнить качество без RAG и с RAG на контрольных вопросах (results/rag_comparison.md)")
	ragJudge := flag.Bool("judge", false, "добавить LLM-as-judge к --rag-eval")
	flag.Parse()

	cfg, err := agent.LoadConfig(*cfgPath)
	if err != nil {
		die(err)
	}

	if *mcpTools {
		runMCPTools(cfg)
		return
	}
	if *mcpCall != "" {
		runMCPCall(cfg, *mcpCall, *mcpArgs)
		return
	}
	if *mcpDemo {
		runMCPDemo(cfg)
		return
	}
	if *mcpPipeline != "" {
		runMCPPipeline(cfg, *mcpPipeline)
		return
	}
	if *mcpOrchestrate != "" {
		runMCPOrchestrate(cfg, *mcpOrchestrate)
		return
	}

	// --- RAG (День 22: первый RAG-запрос) ---
	if *ragQ != "" {
		runRAGAnswer(cfg, *ragQ)
		return
	}
	if *ragEval {
		runRAGEval(cfg, *ragJudge)
		return
	}

	// Многослойная память: short- и long-слои в SQLite (переживают перезапуск),
	// working — в RAM (жизнь одной задачи).
	mem, err := memory.NewLayeredSQLite(cfg.HistoryFile, cfg.LongMemoryFile)
	if err != nil {
		die(err)
	}
	defer mem.Close()
	if *reset {
		_ = mem.ResetAll()
	}

	ag, err := agent.New(cfg, mem)
	if err != nil {
		die(err)
	}

	if *compareMemory {
		runCompareMemory(cfg)
		return
	}
	if *compareProfiles {
		runCompareProfiles(cfg)
		return
	}
	if *compareStrategies {
		runCompareStrategies(cfg)
		return
	}
	if *compare {
		runCompare(ag)
		return
	}
	if *checkInvariants {
		runCheckInvariants(ag)
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

	strat := ag.StrategyName()
	if strat == "" {
		if ag.CompressionEnabled() {
			strat = "legacy-сжатие"
		} else {
			strat = "off"
		}
	}
	fmt.Printf("Агент запущен (модель %s, история: %s, стратегия: %s).\n", ag.Client().Model(), cfg.HistoryFile, strat)
	fmt.Println("Команды: /strategy [window|facts|branch], /checkpoint <имя>, /branch <имя>, /switch <имя>,")
	fmt.Println("         /facts, /memory, /remember <тип> <ключ> <значение>, /newtask — начать новую задачу,")
	fmt.Println("         /reset — очистить короткий+рабочий слои, /reset-all — очистить всё, /compress, /exit.")
	fmt.Println("Задача (FSM): /task — состояние, /begin <цель> (авто-план через LLM), /plan — перегенерировать план,")
	fmt.Println("         /expected <действие>, /step <итог>, /approve — утвердить план (до реализации), /next — следующий этап,")
	fmt.Println("         /run — выполнить текущий шаг плана через LLM, /validate — проверить результат,")
	fmt.Println("         /accept — принять после валидации (validation→done), /rework <причина>,")
	fmt.Println("         /pause — пауза, /resume — продолжить.")

	printHistory(ag)
	sc := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			fmt.Print("> ")
			continue
		}
		switch {
		case strings.HasPrefix(line, "/strategy"):
			parts := strings.Fields(line)
			if len(parts) < 2 {
				fmt.Printf("[стратегия: %s]\n", strategyLabel(ag))
				fmt.Print("> ")
				continue
			}
			if err := ag.SetStrategy(parts[1]); err != nil {
				fmt.Printf("ошибка: %v\n", err)
			} else {
				fmt.Printf("[стратегия: %s]\n", parts[1])
			}
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/mcp-tools"):
			c, err := replConnect(cfg)
			if err != nil {
				fmt.Println("ошибка:", err)
				fmt.Print("> ")
				continue
			}
			tools, err := c.ListTools(stdctx.Background())
			if err != nil {
				fmt.Println("ошибка:", err)
				fmt.Print("> ")
				continue
			}
			for _, t := range tools {
				fmt.Printf("  • %-12s %s\n", t.Name, t.Description)
			}
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/mcp-call"):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "/mcp-call"))
			parts := strings.SplitN(rest, " ", 2)
			if len(parts) < 1 || parts[0] == "" {
				fmt.Println(`использование: /mcp-call <имя> <json-args>`)
				fmt.Println(`  например: /mcp-call create_task {"title":"Написать отчёт","priority":"high"}`)
				fmt.Print("> ")
				continue
			}
			name := parts[0]
			var args map[string]any
			if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
				if err := json.Unmarshal([]byte(parts[1]), &args); err != nil {
					fmt.Println("ошибка разбора JSON-аргументов:", err)
					fmt.Print("> ")
					continue
				}
			}
			c, err := replConnect(cfg)
			if err != nil {
				fmt.Println("ошибка:", err)
				fmt.Print("> ")
				continue
			}
			out, err := c.CallTool(stdctx.Background(), name, args)
			if err != nil {
				fmt.Println("ошибка:", err)
				fmt.Print("> ")
				continue
			}
			fmt.Println(out)
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/mcp-pipeline"):
			query := strings.TrimSpace(strings.TrimPrefix(line, "/mcp-pipeline"))
			if query == "" {
				fmt.Println("использование: /mcp-pipeline <запрос>")
				fmt.Print("> ")
				continue
			}
			c, err := replConnect(cfg)
			if err != nil {
				fmt.Println("ошибка:", err)
				fmt.Print("> ")
				continue
			}
			out, err := execPipeline(stdctx.Background(), c, query, false)
			if err != nil {
				fmt.Println("ошибка:", err)
			} else {
				fmt.Println("== Пайплайн: search → summarize → save_to_file ==")
				fmt.Print(out)
			}
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/mcp-orchestrate"):
			topic := strings.TrimSpace(strings.TrimPrefix(line, "/mcp-orchestrate"))
			if topic == "" {
				fmt.Println("использование: /mcp-orchestrate <тема>")
				fmt.Print("> ")
				continue
			}
			reg := mcpx.NewRegistry(mcpSpecs(cfg))
			if err := reg.ConnectAll(stdctx.Background()); err != nil {
				fmt.Println("ошибка:", err)
				fmt.Print("> ")
				continue
			}
			out, err := orchestrateFlow(stdctx.Background(), reg, topic)
			reg.Close()
			if err != nil {
				fmt.Println("ошибка:", err)
			} else {
				fmt.Println("== Длинный флоу: 3 MCP-сервера ==")
				fmt.Print(out)
			}
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/rag"):
			query := strings.TrimSpace(strings.TrimPrefix(line, "/rag"))
			if query == "" {
				fmt.Println("использование: /rag <вопрос>")
				fmt.Print("> ")
				continue
			}
			reply, err := ag.SayRAG(query)
			if err != nil {
				fmt.Println("ошибка:", err)
			} else {
				fmt.Println(reply.Text)
				if reply.Engine != "" {
					fmt.Printf("[RAG: движок %s, источников: %d]\n", reply.Engine, len(reply.Sources))
				}
			}
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/checkpoint"):
			name := strings.TrimSpace(strings.TrimPrefix(line, "/checkpoint"))
			br, ok := ag.Strategy().(*context.Branching)
			if !ok {
				fmt.Println("[ветвление не активно — включите /strategy branch]")
				fmt.Print("> ")
				continue
			}
			if err := br.Checkpoint(name); err != nil {
				fmt.Printf("ошибка: %v\n", err)
			} else {
				fmt.Printf("[контрольная точка: %s, активна: %s]\n", br.Active(), br.State())
			}
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/branch"):
			name := strings.TrimSpace(strings.TrimPrefix(line, "/branch"))
			br, ok := ag.Strategy().(*context.Branching)
			if !ok {
				fmt.Println("[ветвление не активно — включите /strategy branch]")
				fmt.Print("> ")
				continue
			}
			if err := br.Branch(name); err != nil {
				fmt.Printf("ошибка: %v\n", err)
			} else {
				fmt.Printf("[новая ветка: %s]\n", br.Active())
			}
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/switch"):
			name := strings.TrimSpace(strings.TrimPrefix(line, "/switch"))
			br, ok := ag.Strategy().(*context.Branching)
			if !ok {
				fmt.Println("[ветвление не активно — включите /strategy branch]")
				fmt.Print("> ")
				continue
			}
			if err := br.Switch(name); err != nil {
				fmt.Printf("ошибка: %v\n", err)
			} else {
				fmt.Printf("[активная ветка: %s]\n", br.Active())
			}
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/profile"):
			handleProfileCmd(ag, line)
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/facts"):
			printMemory(ag)
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/memory"):
			printMemory(ag)
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/remember"):
			parts := strings.Fields(line)
			if len(parts) < 4 {
				fmt.Println("использование: /remember <тип> <ключ> <значение>  (тип: profile|decision|knowledge|preference)")
				fmt.Print("> ")
				continue
			}
			kind, key, value := parts[1], parts[2], strings.Join(parts[3:], " ")
			if err := ag.Remember(kind, key, value); err != nil {
				fmt.Printf("ошибка: %v\n", err)
			} else {
				fmt.Printf("[long-память: %s: %s = %s]\n", kind, key, value)
			}
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/invariants"), strings.HasPrefix(line, "/invariant"):
			handleInvariantCmd(ag, line)
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/newtask"):
			_ = ag.ResetContext()
			fmt.Println("[новая задача: короткий и рабочий слои очищены, долговременная память сохранена]")
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/task"):
			printTaskState(ag)
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/begin"):
			handleTaskCmd(ag, "begin", strings.TrimSpace(strings.TrimPrefix(line, "/begin")))
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/approve"):
			handleTaskCmd(ag, "approve", "")
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/plan"):
			handleTaskCmd(ag, "plan", "")
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/run"):
			handleTaskCmd(ag, "run", "")
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/validate"):
			handleTaskCmd(ag, "validate", "")
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/finalize"):
			handleTaskCmd(ag, "finalize", "")
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/expected"):
			handleTaskCmd(ag, "expected", strings.TrimSpace(strings.TrimPrefix(line, "/expected")))
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/step"):
			handleTaskCmd(ag, "step", strings.TrimSpace(strings.TrimPrefix(line, "/step")))
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/next"):
			handleTaskCmd(ag, "next", "")
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/accept"):
			handleTaskCmd(ag, "accept", "")
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/rework"):
			handleTaskCmd(ag, "rework", strings.TrimSpace(strings.TrimPrefix(line, "/rework")))
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/pause"):
			handleTaskCmd(ag, "pause", "")
			fmt.Print("> ")
			continue
		case strings.HasPrefix(line, "/resume"):
			handleTaskCmd(ag, "resume", "")
			fmt.Print("> ")
			continue
		}

		switch line {
		case "/exit", "/quit", "/q":
			return
		case "/reset":
			_ = ag.ResetContext()
			fmt.Println("[история и рабочая память сброшены (long сохранён)]")
			fmt.Print("> ")
			continue
		case "/reset-all":
			_ = ag.ResetAll()
			fmt.Println("[вся память, включая долговременную, сброшена]")
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

// taskStageLabel — человекочитаемая подпись этапа задачи.
func taskStageLabel(stage string) string {
	switch stage {
	case "planning":
		return "планирование"
	case "execution":
		return "исполнение"
	case "validation":
		return "валидация"
	case "done":
		return "выполнено"
	default:
		return stage
	}
}

// printTaskState выводит текущее состояние задачи (FSM) или сообщение, что оно
// не настроено.
func printTaskState(ag *agent.Agent) {
	if !ag.TaskEnabled() {
		fmt.Println("[состояние задачи не настроено — добавьте task_file в config.json]")
		return
	}
	st := ag.TaskState()
	if !st.IsActive() {
		fmt.Println("[активной задачи нет — начните /begin <цель>]")
		return
	}
	fmt.Printf("[задача] цель: %s | этап: %s (%s) | шаг: %d | статус: %s\n",
		st.Goal, taskStageLabel(st.Stage), st.Stage, st.Step, statusLabel(st))
	if st.Expected != "" {
		fmt.Printf("  ожидаемое действие: %s\n", st.Expected)
	}
	fmt.Printf("  план утверждён: %v | валидация пройдена: %v\n", st.PlanApproved, st.Validated)
	if st.Plan != "" {
		fmt.Println("  план реализации (черновик):")
		for _, line := range strings.Split(st.Plan, "\n") {
			fmt.Printf("    - %s\n", line)
		}
	}
	if len(st.Log) > 0 {
		fmt.Println("  итоги шагов:")
		for _, line := range st.Log {
			fmt.Printf("    - %s\n", line)
		}
	}
}

// statusLabel возвращает подпись статуса задачи (пауза / в работе).
func statusLabel(st task.State) string {
	if st.Paused {
		return "ПАУЗА"
	}
	return "в работе"
}

// handleTaskCmd выполняет команду управления состоянием задачи (FSM).
// Неизвестная команда или ошибка перехода выводятся пользователю.
func handleTaskCmd(ag *agent.Agent, cmd, arg string) {
	if !ag.TaskEnabled() {
		fmt.Println("[состояние задачи не настроено — добавьте task_file в config.json]")
		return
	}
	m := ag.Task()
	var err error
	switch cmd {
	case "begin":
		err = ag.BeginTask(arg)
		if err == nil {
			// Авто-генерация плана реализации через LLM при старте задачи.
			if perr := ag.GeneratePlan(); perr != nil {
				fmt.Printf("[план не сгенерирован: %v]\n", perr)
			}
		}
	case "plan":
		err = ag.GeneratePlan()
	case "run":
		var text string
		text, err = ag.ExecuteCurrentStep()
		if err == nil {
			fmt.Println(text)
			return
		}
	case "validate":
		var verdict agent.Verdict
		var review string
		verdict, review, err = ag.ValidateWork()
		if err == nil {
			fmt.Printf("[вердикт] %s\n%s\n", verdict, review)
			return
		}
	case "finalize":
		var summary string
		summary, err = ag.SummarizeDone()
		if err == nil {
			fmt.Println(summary)
			return
		}
	case "approve":
		err = ag.ApproveTask()
	case "expected":
		err = m.SetExpected(arg)
	case "step":
		err = m.Advance(arg)
	case "next":
		err = m.NextStage()
	case "accept":
		// Финал — только после валидации (Accept). NextStage из validation запрещён.
		err = ag.AcceptTask()
	case "rework":
		err = m.Rework(arg)
	case "pause":
		err = m.Pause()
	case "resume":
		err = m.Resume()
	}
	if err != nil {
		printTaskErr(err)
		return
	}
	printTaskState(ag)
}

// printTaskErr выводит структурированный отказ на недопустимый переход
// (*task.TransitionError) с объяснением и подсказкой; прочие ошибки — как есть.
func printTaskErr(err error) {
	if te, ok := task.IsTransitionError(err); ok {
		fmt.Println(te.Explanation())
		return
	}
	fmt.Printf("ошибка: %v\n", err)
}

// printMemory выводит снапшот трёх слоёв памяти агента.
func printMemory(ag *agent.Agent) {
	m := ag.Memories()
	hist, _ := m.Short().Load()
	fmt.Printf("[память] short: %d сообщений | working: %d фактов | long: %d записей\n",
		len(hist), m.Working().Count(), longCount(m))

	if n := m.Working().Count(); n > 0 {
		fmt.Println("рабочая память (текущая задача):")
		keys := make([]string, 0, n)
		for k := range m.Working().All() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v, _ := m.Working().Get(k)
			fmt.Printf("  %s: %s\n", k, v)
		}
	}

	all, _ := m.Long().All()
	if len(all) > 0 {
		fmt.Println("долговременная память (профиль/решения/знания):")
		for _, e := range all {
			fmt.Printf("  [%s] %s: %s\n", e.Kind, e.Key, e.Value)
		}
	}
}

func longCount(m *memory.LayeredMemory) int {
	all, _ := m.Long().All()
	return len(all)
}

// handleProfileCmd обрабатывает команду /profile: show|list|new|use|set.
func handleProfileCmd(ag *agent.Agent, line string) {
	parts := strings.Fields(line)
	sub := ""
	if len(parts) >= 2 {
		sub = parts[1]
	}
	switch sub {
	case "", "show":
		printProfile(ag, ag.ActiveProfile())
	case "list":
		if ag.Profiles() == nil {
			fmt.Println("[хранилище профилей не настроено (profile_file)]")
			return
		}
		list, _ := ag.Profiles().List()
		if len(list) == 0 {
			fmt.Println("[профилей нет — создайте: /profile new kutyakin]")
			return
		}
		active := ""
		if ap := ag.ActiveProfile(); ap != nil {
			active = ap.ID
		}
		fmt.Println("профили:")
		for _, p := range list {
			mark := " "
			if p.ID == active {
				mark = "*"
			}
			fmt.Printf("  %s %-12s %s\n", mark, p.ID, p.Name)
		}
	case "new":
		if len(parts) < 3 {
			fmt.Printf("использование: /profile new <template> (%s)\n", strings.Join(profile.TemplateNames(), "|"))
			return
		}
		tpl, ok := profile.Templates()[parts[2]]
		if !ok {
			fmt.Printf("нет заготовки %q (есть: %s)\n", parts[2], strings.Join(profile.TemplateNames(), ", "))
			return
		}
		if err := ag.SaveProfile(tpl); err != nil {
			fmt.Printf("ошибка: %v\n", err)
			return
		}
		_ = ag.SetActiveProfile(tpl.ID)
		fmt.Printf("[профиль %q создан и активирован]\n", tpl.ID)
	case "use":
		if len(parts) < 3 {
			fmt.Println("использование: /profile use <id>")
			return
		}
		if err := ag.SetActiveProfile(parts[2]); err != nil {
			fmt.Printf("ошибка: %v\n", err)
			return
		}
		fmt.Printf("[активный профиль: %s]\n", parts[2])
	case "set":
		if len(parts) < 4 {
			fmt.Println("использование: /profile set <поле> <значение>  (поля: style|format|name|role|language|constraint|expertise)")
			return
		}
		p := ag.ActiveProfile()
		if p == nil {
			fmt.Println("[активного профиля нет — сначала /profile new kutyakin]")
			return
		}
		if !p.SetField(parts[2], strings.Join(parts[3:], " ")) {
			fmt.Println("неизвестное поле: " + parts[2])
			return
		}
		if err := ag.SaveProfile(*p); err != nil {
			fmt.Printf("ошибка: %v\n", err)
			return
		}
		fmt.Println("[профиль обновлён]")
	default:
		fmt.Println("подкоманды: show | list | new <template> | use <id> | set <поле> <значение>")
	}
}

// printProfile печатает активный профиль (или сообщение, что его нет).
func printProfile(ag *agent.Agent, p *profile.Profile) {
	if p == nil {
		fmt.Println("[активного профиля нет — включите персонализацию: /profile new kutyakin]")
		return
	}
	fmt.Printf("[профиль: %s]\n", p.SystemBlock())
}

// runCompareProfiles — проверка влияния персонализации: один вопрос задаётся
// агенту под двумя разными профилями (terse vs detailed), выводится длина ответов
// и делается эвристический вывод о том, что профиль учтён автоматически.
func runCompareProfiles(cfg *agent.Config) {
	fmt.Println("\n=== Сравнение персонализации: профили terse vs detailed ===")
	const q = "Как выбрать state-менеджер для Flutter-приложения?"
	ids := []string{"terse", "detailed"}

	answers := map[string]string{}
	for _, id := range ids {
		ag, err := agent.New(cfg, memory.NewLayeredRAM())
		if err != nil {
			fmt.Printf("профиль %s: не удалось создать агента: %v\n", id, err)
			continue
		}
		tpl := profile.Templates()[id]
		if err := ag.SaveProfile(tpl); err != nil {
			fmt.Printf("профиль %s: ошибка сохранения: %v\n", id, err)
			continue
		}
		_ = ag.SetActiveProfile(id)
		reply, err := ag.Say(q)
		if err != nil {
			fmt.Printf("профиль %s: ошибка хода: %v\n", id, err)
			continue
		}
		answers[id] = reply.Text
		fmt.Printf("\n--- Ответ с профилем %q ---\n%s\n", id, reply.Text)
	}

	if len(answers) == 2 {
		l1, l2 := len([]rune(answers["terse"])), len([]rune(answers["detailed"]))
		fmt.Printf("\nИтог: terse=%d симв., detailed=%d симв. (профиль учтён, если стиль/объём различаются)\n", l1, l2)
	}
	fmt.Println()
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

	cm := context.NewContextManager(client, 10, 10)
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

// strategyLabel возвращает человекочитаемое имя активной стратегии агента.
func strategyLabel(ag *agent.Agent) string {
	if n := ag.StrategyName(); n != "" {
		return n
	}
	if ag.CompressionEnabled() {
		return "legacy-сжатие"
	}
	return "off"
}

// runCompareStrategies прогоняет сценарий «собираем ТЗ» на всех трёх стратегиях
// (свежий агент на каждую), печатает таблицу и анализ, сохраняет отчёт
// plans/compare-context.md. Логика сравнения вынесена в пакет agent.
func runCompareStrategies(cfg *agent.Config) {
	fmt.Println("\n=== Сравнение стратегий контекста: window / facts / branch ===")
	fmt.Println("Сценарий «собираем ТЗ» (12 ходов) + проверочный вопрос.")

	results, err := agent.CompareStrategies(cfg, nil)
	if err != nil {
		fmt.Printf("ошибка сравнения: %v\n", err)
		fmt.Println()
		return
	}

	for _, r := range results {
		printStratResult(r)
	}
	printComparisonTable(results)
	writeCompareReport(results)

	fmt.Print(agent.AnalyzeStrategies(results))
	fmt.Println("\nОтчёт сохранён: plans/compare-context.md")
	fmt.Println()
}

// runCompareMemory сравнивает ответы агента с долговременной памятью и без неё:
// в long-слой заранее кладутся профиль/решение/знание, затем задаётся вопрос,
// требующий их вспомнить.
func runCompareMemory(cfg *agent.Config) {
	fmt.Println("\n=== Проверка влияния долговременной памяти (long) ===")

	longMem := memory.NewInMemoryLong()
	_ = longMem.Put(memory.LongEntry{Kind: memory.KindProfile, Key: "имя", Value: "Анна"})
	_ = longMem.Put(memory.LongEntry{Kind: memory.KindDecision, Key: "стек", Value: "Go"})
	_ = longMem.Put(memory.LongEntry{Kind: memory.KindKnowledge, Key: "домен", Value: "финансовые приложения"})

	withLong, err := agent.New(cfg, memory.NewLayered(dialog.NewInMemory(), memory.NewInMemoryWorking(), longMem))
	if err != nil {
		fmt.Printf("не удалось создать агента с long: %v\n", err)
		return
	}
	withoutLong, err := agent.New(cfg, memory.NewLayeredRAM())
	if err != nil {
		fmt.Printf("не удалось создать агента без long: %v\n", err)
		return
	}

	const q = "Как меня зовут, какой стек мы выбрали и в каком домене работаем?"
	aWith, _ := withLong.Say(q)
	aWithout, _ := withoutLong.Say(q)

	score := func(text string) float64 {
		t := strings.ToLower(text)
		terms := []string{"анна", "go", "финанс"}
		hit := 0
		for _, term := range terms {
			if strings.Contains(t, term) {
				hit++
			}
		}
		return float64(hit) / float64(len(terms))
	}

	fmt.Printf("\nС долговременной памятью (recall %.0f%%):\n%s\n", score(aWith.Text)*100, aWith.Text)
	fmt.Printf("\nБез долговременной памяти (recall %.0f%%):\n%s\n", score(aWithout.Text)*100, aWithout.Text)
	fmt.Println()
}

// printStratResult выводит метрики одной стратегии.
func printStratResult(r agent.StrategyResult) {
	if r.Error != "" {
		fmt.Printf("  %-8s ошибка: %s\n", r.Name, r.Error)
		return
	}
	fmt.Printf("  %-8s токены ~%-7d качество %.0f%%  стабильность %.0f%%  ходов %d\n",
		r.Name, r.Tokens, r.Quality*100, r.Stability*100, r.Commands)
}

// printComparisonTable выводит сводную таблицу сравнения.
func printComparisonTable(results []agent.StrategyResult) {
	fmt.Println("\n--- Сводная таблица ---")
	fmt.Printf("%-8s %-10s %-10s %-10s %-8s\n", "стратегия", "токены", "качество", "стабильность", "ходов")
	for _, r := range results {
		if r.Error != "" {
			fmt.Printf("%-8s %-10s %-9s %-9s %-8s\n", r.Name, "—", "—", "—", "—")
			continue
		}
		fmt.Printf("%-8s %-10d %-9.0f%% %-9.0f%% %-8d\n",
			r.Name, r.Tokens, r.Quality*100, r.Stability*100, r.Commands)
	}
}

// writeCompareReport формирует markdown-отчёт plans/compare-context.md.
func writeCompareReport(results []agent.StrategyResult) {
	var sb strings.Builder
	sb.WriteString("# Сравнение стратегий управления контекстом\n\n")
	sb.WriteString("Сценарий «собираем ТЗ» (~12 ходов) + финальный проверочный вопрос ")
	sb.WriteString("«перечисли цель, ограничения и дедлайн». Оценка качества/стабильности — ")
	sb.WriteString("эвристическая по ключевым терминам; расход токенов — сумма request+completion из Usage.\n\n")
	sb.WriteString("| Стратегия | Токены | Качество | Стабильность | Ходов |\n")
	sb.WriteString("|---|---|---|---|---|\n")
	for _, r := range results {
		if r.Error != "" {
			fmt.Fprintf(&sb, "| %s | — | — | — | — |\n", r.Name)
			continue
		}
		fmt.Fprintf(&sb, "| %s | %d | %.0f%% | %.0f%% | %d |\n",
			r.Name, r.Tokens, r.Quality*100, r.Stability*100, r.Commands)
	}
	sb.WriteString("\n## Ожидаемые тенденции\n\n")
	sb.WriteString("- **window** — минимум токенов, риск потери ранних фактов при маленьком N.\n")
	sb.WriteString("- **facts** — баланс: рабочий контекст держит детали задачи.\n")
	sb.WriteString("- **branch** — максимум токенов и стабильности в ветке, гибкий UX.\n")

	sb.WriteString("\n## Ответы на проверочный вопрос\n\n")
	for _, r := range results {
		fmt.Fprintf(&sb, "### %s\n\n", r.Name)
		for i, a := range r.Answers {
			fmt.Fprintf(&sb, "<details><summary>Ответ %d</summary>\n\n```\n%s\n```\n</details>\n\n", i+1, a)
		}
	}

	sb.WriteString("\n" + agent.AnalyzeStrategies(results) + "\n")

	if err := os.MkdirAll("plans", 0o755); err == nil {
		if err := os.WriteFile("plans/compare-context.md", []byte(sb.String()), 0o644); err != nil {
			fmt.Printf("предупреждение: не удалось записать отчёт: %v\n", err)
		}
	} else {
		fmt.Printf("предупреждение: не удалось создать plans/: %v\n", err)
	}
}

// runCheckInvariants — демонстрация поведения при конфликте запроса и инварианта:
// показывает, как ассистент детерминированно отказывается (без обращения к LLM)
// и как объясняет отказ.
func runCheckInvariants(ag *agent.Agent) {
	mgr := ag.Invariants()
	fmt.Println("\n=== Проверка инвариантов: конфликт запроса и правила ===")
	if mgr == nil {
		fmt.Println("инварианты не настроены (в config.json пустой invariants_file)")
		fmt.Println()
		return
	}
	fmt.Println("Активные инварианты:")
	for _, i := range mgr.Active() {
		fmt.Printf("  - [%s] %s: %s\n", invariants.CategoryLabel(i.Category), i.Title, i.Text)
	}

	conflictInput := "перепишите сервис на python"
	fmt.Printf("\nДемо-запрос, конфликтующий с инвариантом: %q\n", conflictInput)
	cs := mgr.CheckConflict(conflictInput)
	hard := invariants.HardConflicts(cs)
	if len(hard) > 0 {
		fmt.Println("\nОтвет ассистента (детерминированный отказ, без LLM):")
		fmt.Println(mgr.RefusalTextAll(hard))
	} else if len(cs) > 0 {
		fmt.Println("\nОбнаружен мягкий конфликт (предупреждение, ответ не блокируется):")
		for _, c := range cs {
			fmt.Printf("  - [%s] %s\n", invariants.CategoryLabel(c.Invariant.Category), c.Invariant.Title)
		}
	} else {
		fmt.Println("\nКонфликт не обнаружен — запрос обрабатывается обычным путём.")
	}
	fmt.Println()
}

// handleInvariantCmd обрабатывает REPL-команды управления инвариантами:
//
//	/invariants             — список активных
//	/invariant add <cat>|<title>|<text>   — добавить правило
//	/invariant rm <id>      — удалить по ID
//	/invariant show <id>    — показать подробно
func handleInvariantCmd(ag *agent.Agent, line string) {
	mgr := ag.Invariants()
	if mgr == nil {
		fmt.Println("[инварианты не настроены: в config.json пустой invariants_file]")
		return
	}
	fields := strings.Fields(line)
	switch {
	case fields[0] == "/invariants" || (len(fields) >= 2 && fields[1] == "list"):
		active := mgr.Active()
		if len(active) == 0 {
			fmt.Println("[активных инвариантов нет]")
			return
		}
		fmt.Println("[активные инварианты:]")
		for _, i := range active {
			fmt.Printf("  %s [%s] %s: %s\n", i.ID, invariants.CategoryLabel(i.Category), i.Title, i.Text)
		}
	case len(fields) >= 2 && fields[1] == "add":
		if len(fields) < 5 {
			fmt.Println("использование: /invariant add <категория>|<заголовок>|<текст>  (категория: архитектура|техническое решение|стек|бизнес-правило)")
			return
		}
		id := "inv_" + fields[2]
		i := invariants.Invariant{
			ID: id, Title: fields[3], Category: fields[2],
			Text: strings.Join(fields[4:], " "), Severity: invariants.SeverityHard, Enabled: true,
		}
		if err := mgr.Add(i); err != nil {
			fmt.Printf("[ошибка: %v]\n", err)
			return
		}
		fmt.Printf("[инвариант добавлен: %s]\n", id)
	case len(fields) >= 2 && fields[1] == "rm":
		if err := mgr.Delete(fields[2]); err != nil {
			fmt.Printf("[ошибка: %v]\n", err)
			return
		}
		fmt.Printf("[инвариант удалён: %s]\n", fields[2])
	case len(fields) >= 2 && fields[1] == "show":
		i, _ := mgr.Get(fields[2])
		if i == nil {
			fmt.Printf("[инвариант %s не найден]\n", fields[2])
			return
		}
		fmt.Printf("%s [%s] severity=%s enabled=%v\n  %s\n", i.ID, invariants.CategoryLabel(i.Category), i.Severity, i.Enabled, i.Text)
	default:
		fmt.Println("подкоманды: list | add <cat>|<title>|<text> | rm <id> | show <id>")
	}
}

// replMCP — кэшированное MCP-соединение для REPL-сессии (создаётся лениво).
var replMCP *mcpx.Client

// replConnect возвращает MCP-клиента для REPL, создавая соединение при первом
// обращении (replMCP). REPL однопоточный, поэтому гонки нет.
func replConnect(cfg *agent.Config) (*mcpx.Client, error) {
	if replMCP != nil {
		return replMCP, nil
	}
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 15*time.Second)
	defer cancel()
	c, err := mcpx.Connect(ctx, cfg.MCPCommand, cfg.MCPArgs...)
	if err != nil {
		return nil, err
	}
	replMCP = c
	return c, nil
}

// runMCPCall подключается к MCP-серверу, вызывает инструмент по имени и печатает
// результат. Аргументы передаются как JSON-строка (--mcp-args).
func runMCPCall(cfg *agent.Config, name, argsJSON string) {
	// 60 с: generate_document обращается к LLM.
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 60*time.Second)
	defer cancel()

	c, err := mcpx.Connect(ctx, cfg.MCPCommand, cfg.MCPArgs...)
	if err != nil {
		die(fmt.Errorf("MCP-соединение: %w", err))
	}
	defer c.Close()

	var args map[string]any
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			die(fmt.Errorf("разбор --mcp-args: %w", err))
		}
	}

	out, err := c.CallTool(ctx, name, args)
	if err != nil {
		die(err)
	}
	fmt.Println(out)
}

// runMCPDemo показывает планировщик в действии: ставит напоминание, запускает
// периодический сбор данных, ждёт несколько тактов тикера и печатает
// агрегированную сводку. Все действия — через MCP-инструменты (CallTool).
func runMCPDemo(cfg *agent.Config) {
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 30*time.Second)
	defer cancel()

	c, err := mcpx.Connect(ctx, cfg.MCPCommand, cfg.MCPArgs...)
	if err != nil {
		die(fmt.Errorf("MCP-соединение: %w", err))
	}
	defer c.Close()

	fmt.Println("== Планировщик: демо ==")

	out, err := c.CallTool(ctx, "reminder_add", map[string]any{"text": "Собрать стенд перед встречей", "in_minutes": 0})
	if err != nil {
		die(fmt.Errorf("reminder_add: %w", err))
	}
	fmt.Println("reminder_add  ->", out)

	out, err = c.CallTool(ctx, "collect_start", map[string]any{"metric": "cpu", "interval_seconds": 1, "iterations": 3})
	if err != nil {
		die(fmt.Errorf("collect_start: %w", err))
	}
	fmt.Println("collect_start ->", out)

	fmt.Println("Жду 4 с, пока тикер соберёт точки и сработает напоминание...")
	time.Sleep(4 * time.Second)

	out, err = c.CallTool(ctx, "reminders_status", nil)
	if err != nil {
		die(fmt.Errorf("reminders_status: %w", err))
	}
	fmt.Println("reminders_status ->", out)

	out, err = c.CallTool(ctx, "collect_status", nil)
	if err != nil {
		die(fmt.Errorf("collect_status: %w", err))
	}
	fmt.Println("collect_status  ->", out)

	out, err = c.CallTool(ctx, "get_summary", nil)
	if err != nil {
		die(fmt.Errorf("get_summary: %w", err))
	}
	fmt.Println("get_summary      ->", out)
}

// mcpSpecs собирает список MCP-серверов для оркестратора из config.json
// (по умолчанию — три домена одного бинарника).
func mcpSpecs(cfg *agent.Config) []mcpx.ServerSpec {
	if len(cfg.MCPServers) == 0 {
		return mcpx.DefaultServerSpecs(cfg.MCPCommand)
	}
	specs := make([]mcpx.ServerSpec, 0, len(cfg.MCPServers))
	for _, s := range cfg.MCPServers {
		specs = append(specs, mcpx.ServerSpec{Name: s.Name, Command: s.Command, Args: s.Args})
	}
	return specs
}

// orchestrateFlow выполняет длинный флоу с инструментами РАЗНЫХ серверов
// (knowledge → tasks → scheduler → knowledge) и печатает маршрут каждого вызова.
func orchestrateFlow(ctx stdctx.Context, reg *mcpx.Registry, topic string) (string, error) {
	var b strings.Builder

	// 1) search (knowledge) — получить данные.
	sr, err := reg.CallTool(ctx, "search", map[string]any{"query": topic, "limit": 2})
	if err != nil {
		return "", fmt.Errorf("шаг search: %w", err)
	}
	var searchRes mcpx.SearchResult
	if err := json.Unmarshal([]byte(sr.Output), &searchRes); err != nil {
		return "", fmt.Errorf("разбор search: %w", err)
	}
	fmt.Fprintf(&b, "1) search(%q) → %s\n", topic, sr.Server)
	if len(searchRes.Docs) == 0 {
		fmt.Fprintln(&b, "   документов не найдено, сводка будет по заметке")
	}
	for _, d := range searchRes.Docs {
		fmt.Fprintf(&b, "   • [%s] %s — %s\n", d.ID, d.Title, d.Snippet)
	}

	// 2) summarize (knowledge) — обработать данные.
	var parts []string
	for _, d := range searchRes.Docs {
		parts = append(parts, d.Title+". "+d.Snippet)
	}
	if len(parts) == 0 {
		parts = []string{fmt.Sprintf("Документов по теме %q нет.", topic)}
	}
	su, err := reg.CallTool(ctx, "summarize", map[string]any{"text": strings.Join(parts, "\n\n"), "max_words": 40})
	if err != nil {
		return "", fmt.Errorf("шаг summarize: %w", err)
	}
	var sumRes mcpx.SummarizeResult
	if err := json.Unmarshal([]byte(su.Output), &sumRes); err != nil {
		return "", fmt.Errorf("разбор summarize: %w", err)
	}
	fmt.Fprintf(&b, "2) summarize → %s: %s\n", su.Server, sumRes.Summary)

	// 3) create_task (tasks) — создать задачу из сводки.
	title := sumRes.Summary
	if len(title) > 60 {
		title = title[:60]
	}
	tk, err := reg.CallTool(ctx, "create_task", map[string]any{"title": title, "priority": "high", "assignee": "agent"})
	if err != nil {
		return "", fmt.Errorf("шаг create_task: %w", err)
	}
	var taskRes mcpx.Task
	if err := json.Unmarshal([]byte(tk.Output), &taskRes); err != nil {
		return "", fmt.Errorf("разбор create_task: %w", err)
	}
	fmt.Fprintf(&b, "3) create_task → %s: задача %s создана\n", tk.Server, taskRes.ID)

	// 4) reminder_add (scheduler) — напомнить про задачу.
	rm, err := reg.CallTool(ctx, "reminder_add", map[string]any{"text": "Проверить задачу " + taskRes.ID, "in_minutes": 0})
	if err != nil {
		return "", fmt.Errorf("шаг reminder_add: %w", err)
	}
	var remRes struct {
		ID    string `json:"id"`
		DueAt string `json:"due_at"`
	}
	if err := json.Unmarshal([]byte(rm.Output), &remRes); err != nil {
		return "", fmt.Errorf("разбор reminder_add: %w", err)
	}
	fmt.Fprintf(&b, "4) reminder_add → %s: напоминание %s до %s\n", rm.Server, remRes.ID, remRes.DueAt)

	// 5) collect_start (scheduler) — периодический сбор данных.
	cs, err := reg.CallTool(ctx, "collect_start", map[string]any{"metric": "orchestrated", "interval_seconds": 1, "iterations": 3})
	if err != nil {
		return "", fmt.Errorf("шаг collect_start: %w", err)
	}
	fmt.Fprintf(&b, "5) collect_start → %s: %s\n", cs.Server, cs.Output)

	// 6) get_summary (scheduler) — агрегированный итог планировщика.
	gs, err := reg.CallTool(ctx, "get_summary", nil)
	if err != nil {
		return "", fmt.Errorf("шаг get_summary: %w", err)
	}
	fmt.Fprintf(&b, "6) get_summary → %s: %s\n", gs.Server, gs.Output)

	// 7) save_to_file (knowledge) — сохранить итоговый отчёт.
	report := fmt.Sprintf(
		"# Оркестрация по теме: %s\n\n## Сводка\n%s\n\n## Задача\n%s (%s)\n\n## Итог планировщика\n%s\n",
		topic, sumRes.Summary, taskRes.Title, taskRes.ID, gs.Output)
	sf, err := reg.CallTool(ctx, "save_to_file", map[string]any{"filename": "orchestration_" + slugify(topic) + ".md", "content": report})
	if err != nil {
		return "", fmt.Errorf("шаг save_to_file: %w", err)
	}
	var saveRes mcpx.SaveFileResult
	if err := json.Unmarshal([]byte(sf.Output), &saveRes); err != nil {
		return "", fmt.Errorf("разбор save_to_file: %w", err)
	}
	fmt.Fprintf(&b, "7) save_to_file → %s: %s (%d байт)\n", sf.Server, saveRes.Path, saveRes.Bytes)

	return b.String(), nil
}

// runMCPOrchestrate подключается к нескольким MCP-серверам и прогоняет длинный флоу.
func runMCPOrchestrate(cfg *agent.Config, topic string) {
	// 120 с: generate_document ходит в LLM, плюс N процессов.
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 120*time.Second)
	defer cancel()

	reg := mcpx.NewRegistry(mcpSpecs(cfg))
	if err := reg.ConnectAll(ctx); err != nil {
		die(fmt.Errorf("оркестратор: %w", err))
	}
	defer reg.Close()

	out, err := orchestrateFlow(ctx, reg, topic)
	if err != nil {
		die(err)
	}
	fmt.Println("== Длинный флоу: 3 MCP-сервера ==")
	fmt.Print(out)
}

// execPipeline выполняет цепочку MCP-инструментов:
//
//  1. search      — найти документы;
//  2. summarize   — построить сводку по содержимому найденного (передаём
//     результат шага 1 как вход шага 2);
//  3. save_to_file— сохранить сводку в файл (вход шага 3 — вывод шага 2).
//
// Данные между инструментами передаются через CallTool (JSON в JSON).
func execPipeline(ctx stdctx.Context, c *mcpx.Client, query string, interactive bool) (string, error) {
	var b strings.Builder

	// Шаг 1: search
	sr, err := c.CallTool(ctx, "search", map[string]any{"query": query, "limit": 3})
	if err != nil {
		return "", fmt.Errorf("search: %w", err)
	}
	var searchRes mcpx.SearchResult
	if err := json.Unmarshal([]byte(sr), &searchRes); err != nil {
		return "", fmt.Errorf("разбор результата search: %w", err)
	}

	var parts []string
	if len(searchRes.Docs) == 0 {
		fmt.Fprintf(&b, "1) search(%q): 0 документов\n", query)
		if interactive {
			// Запрос вне корпуса: спрашиваем, не сформировать ли документ через LLM.
			fmt.Print("   Документ не найден. Сформировать его через LLM и добавить в корпус? [y/N] ")
			if askYesNo() {
				gd, err := c.CallTool(ctx, "generate_document", map[string]any{"topic": query})
				if err != nil {
					return "", fmt.Errorf("generate_document: %w", err)
				}
				var genRes mcpx.GeneratedDoc
				if err := json.Unmarshal([]byte(gd), &genRes); err != nil {
					return "", fmt.Errorf("разбор результата generate_document: %w", err)
				}
				fmt.Fprintf(&b, "   создан документ [%s] %s — %s (источник: %s)\n", genRes.ID, genRes.Title, genRes.Snippet, genRes.Source)
				parts = []string{genRes.Title + ". " + genRes.Snippet}
			} else {
				fmt.Fprintln(&b, "   пропускаю генерацию, сохраняю заметку")
				parts = []string{fmt.Sprintf("По запросу %q в локальном корпусе ничего не найдено.", query)}
			}
		} else {
			fmt.Fprintln(&b, "   тема не в локальном корпусе — сохраняю заметку (для пополнения вызовите generate_document)")
			parts = []string{fmt.Sprintf("По запросу %q в локальном корпусе ничего не найдено. Вызовите generate_document, чтобы пополнить корпус.", query)}
		}
	} else {
		fmt.Fprintf(&b, "1) search(%q): %d документов\n", query, searchRes.Total)
		for _, d := range searchRes.Docs {
			fmt.Fprintf(&b, "   • [%s] %s — %s\n", d.ID, d.Title, d.Snippet)
		}
		// Шаг 2: summarize — на вход подаётся контент из шага 1.
		for _, d := range searchRes.Docs {
			parts = append(parts, d.Title+". "+d.Snippet)
		}
	}
	su, err := c.CallTool(ctx, "summarize", map[string]any{"text": strings.Join(parts, "\n\n"), "max_words": 40})
	if err != nil {
		return "", fmt.Errorf("summarize: %w", err)
	}
	var sumRes mcpx.SummarizeResult
	if err := json.Unmarshal([]byte(su), &sumRes); err != nil {
		return "", fmt.Errorf("разбор результата summarize: %w", err)
	}
	fmt.Fprintf(&b, "2) summarize: %d слов, %d источников\n", sumRes.Words, sumRes.Sources)
	fmt.Fprintf(&b, "   %s\n", sumRes.Summary)

	// Шаг 3: save_to_file — на вход подаётся сводка из шага 2.
	fname := "pipeline_" + slugify(query) + ".md"
	sf, err := c.CallTool(ctx, "save_to_file", map[string]any{"filename": fname, "content": sumRes.Summary + "\n"})
	if err != nil {
		return "", fmt.Errorf("save_to_file: %w", err)
	}
	var saveRes mcpx.SaveFileResult
	if err := json.Unmarshal([]byte(sf), &saveRes); err != nil {
		return "", fmt.Errorf("разбор результата save_to_file: %w", err)
	}
	fmt.Fprintf(&b, "3) save_to_file: %s (%d байт)\n", saveRes.Path, saveRes.Bytes)

	return b.String(), nil
}

// slugify приводит строку к безопасному имени файла: латиница и цифры
// сохраняются, любые разделители схлопываются в один дефис.
func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash {
				b.WriteRune('-')
				dash = true
			}
		}
	}
	if name := strings.Trim(b.String(), "-"); name != "" {
		return name
	}
	return "result"
}

// askYesNo читает ответ пользователя (y/yes/да/д/1 — true).
func askYesNo() bool {
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(sc.Text())) {
	case "y", "yes", "да", "д", "1":
		return true
	}
	return false
}

// runMCPPipeline подключается к MCP-серверу и прогоняет пайплайн по запросу.
func runMCPPipeline(cfg *agent.Config, query string) {
	// 120 с: generate_document ходит в LLM (у llm.Chat внутренний таймаут 45 с).
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 120*time.Second)
	defer cancel()

	c, err := mcpx.Connect(ctx, cfg.MCPCommand, cfg.MCPArgs...)
	if err != nil {
		die(fmt.Errorf("MCP-соединение: %w", err))
	}
	defer c.Close()

	out, err := execPipeline(ctx, c, query, true)
	if err != nil {
		die(err)
	}
	fmt.Println("== Пайплайн: search → summarize → save_to_file ==")
	fmt.Print(out)
}

// runMCPTools подключается к локальному MCP-серверу (отдельный процесс
// bin/mcp-server) по stdio, устанавливает соединение (Initialize) и печатает
// список доступных инструментов (tools/list). Это проверка того, что
// соединение устанавливается и список инструментов корректно возвращается.
func runMCPTools(cfg *agent.Config) {
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 15*time.Second)
	defer cancel()

	c, err := mcpx.Connect(ctx, cfg.MCPCommand, cfg.MCPArgs...)
	if err != nil {
		die(fmt.Errorf("MCP-соединение: %w", err))
	}
	defer c.Close()

	tools, err := c.ListTools(ctx)
	if err != nil {
		die(fmt.Errorf("MCP tools/list: %w", err))
	}

	fmt.Printf("MCP-соединение установлено (сервер: %s). Инструментов: %d\n", cfg.MCPCommand, len(tools))
	for _, t := range tools {
		fmt.Printf("  • %-12s %s\n", t.Name, t.Description)
	}
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	os.Exit(1)
}

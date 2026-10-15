package main

import (
	stdctx "context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agent"
	mcpx "agent/feature/mcp"
	"agent/feature/memory"
)

// devHelpTimeout — таймаут на сбор git-контекста и ответ ассистента.
const devHelpTimeout = 60 * time.Second

// runDevHelp — одноразовый ответ ассистента разработчика (флаг --dev-help):
// вопрос → git-контекст через MCP (ветка/статус/diff/файлы) + RAG по индексу
// index_service (README, docs, код) → ответ LLM с источниками.
func runDevHelp(cfg *agent.Config, query string) {
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
		die(errors.New("RAG не настроен: добавьте секцию rag в config.json"))
	}
	runDevHelpAnswer(ag, cfg, query)
}

// devHelpREPL — обработчик команды /help в REPL-чате.
//
//	/help            — справка по командам + текущая git-ветка (через MCP git)
//	/help <вопрос>   — ответ ассистента разработчика (RAG + git-контекст)
func devHelpREPL(ag *agent.Agent, cfg *agent.Config, line string) {
	query := strings.TrimSpace(strings.TrimPrefix(line, "/help"))
	if query == "" {
		printHelpCommands()
		ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 15*time.Second)
		defer cancel()
		if branch, err := gitBranch(ctx, cfg); err == nil && branch != "" {
			fmt.Printf("[git: ветка %s]\n", branch)
		}
		return
	}
	runDevHelpAnswer(ag, cfg, query)
}

// runDevHelpAnswer — общий путь ответа: git-контекст → SayHelp → вывод с источниками.
func runDevHelpAnswer(ag *agent.Agent, cfg *agent.Config, query string) {
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), devHelpTimeout)
	defer cancel()

	gitCtx, branch, err := fetchGitContext(ctx, cfg)
	if err != nil {
		fmt.Printf("[MCP git недоступен: %v — отвечаю только по документации]\n", err)
		gitCtx = ""
	} else if branch != "" {
		fmt.Printf("[git: ветка %s]\n", branch)
	}

	reply, err := ag.SayHelp(query, gitCtx)
	if err != nil {
		fmt.Println("ошибка:", err)
		return
	}
	fmt.Println(reply.Text)
	printChatSources(reply)
}

// printHelpCommands — справка по командам REPL (вызывается /help без аргументов).
func printHelpCommands() {
	fmt.Println("Ассистент разработчика. Команды:")
	fmt.Println("  /help <вопрос>   — ответ о проекте: RAG (README/docs) + git-контекст (ветка/статус/diff)")
	fmt.Println("  /help            — эта справка + текущая git-ветка")
	fmt.Println("  /goal <текст>    — переопределить цель диалога")
	fmt.Println("  /facts           — показать факты сессии (память задачи)")
	fmt.Println("  /reset           — сбросить контекст (новый диалог, новая цель)")
	fmt.Println("  /exit            — выход")
	fmt.Println("Пример: /help как устроен агент и какие MCP-инструменты есть")
}

// gitBranch возвращает текущую git-ветку через MCP-инструмент git_branch.
func gitBranch(ctx stdctx.Context, cfg *agent.Config) (string, error) {
	c, err := mcpx.Connect(ctx, cfg.MCPCommand, "--server", mcpx.ServerGit)
	if err != nil {
		return "", err
	}
	defer c.Close()

	out, err := c.CallTool(ctx, "git_branch", nil)
	if err != nil {
		return "", err
	}
	var b struct {
		Branch string `json:"branch"`
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		return "", err
	}
	return b.Branch, nil
}

// fetchGitContext собирает git-контекст проекта через MCP-инструменты домена git
// (общая реализация — mcpx.GitContext). Возвращает текстовую сводку и имя ветки.
func fetchGitContext(ctx stdctx.Context, cfg *agent.Config) (string, string, error) {
	return mcpx.GitContext(ctx, cfg.MCPCommand)
}

package main

import (
	stdctx "context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent"
	mcpx "agent/feature/mcp"
	"agent/feature/memory"
)

// runFiles — ассистент файловых операций (флаг --files "<задача>").
// Plan-then-execute: LLM строит план (сценарий, поиски, чтения, выходной файл),
// CLI детерминированно исполняет его через MCP-инструменты домена files
// (search_files/read_file), затем LLM готовит результат: отчёт (find/check/diff)
// или содержимое файла (generate/docs) → write_file + git diff.
func runFiles(cfg *agent.Config, goal string) {
	mem, err := memory.NewLayeredSQLite(cfg.HistoryFile, cfg.LongMemoryFile)
	if err != nil {
		die(err)
	}
	defer mem.Close()

	ag, err := agent.New(cfg, mem)
	if err != nil {
		die(err)
	}

	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 120*time.Second)
	defer cancel()

	// 1. План (LLM → JSON, fallback по ключевым словам) + валидация.
	pr, err := ag.FilePlan(goal)
	if err != nil {
		die(err)
	}
	sanitizeFilePlan(pr.Plan)
	fmt.Printf("[план: сценарий %s%s]\n", pr.Plan.Scenario, fallbackNote(pr.Fallback))

	// 2. Исполнение плана через MCP files.
	c, err := mcpx.Connect(ctx, cfg.MCPCommand, "--server", mcpx.ServerFiles)
	if err != nil {
		die(fmt.Errorf("MCP files недоступен: %w", err))
	}
	defer c.Close()

	context := executeFilePlan(ctx, c, pr.Plan)

	// 3. Генерация результата.
	out, err := ag.FileGenerate(goal, pr.Plan, context)
	if err != nil {
		die(err)
	}

	// 4. Запись файла (generate/docs) или отчёт (find/check/diff).
	if pr.Plan.OutputFile != "" {
		if _, err := c.CallTool(ctx, "write_file", map[string]any{
			"path": pr.Plan.OutputFile, "content": out,
		}); err != nil {
			die(err)
		}
		fmt.Printf("[записан файл: %s]\n", pr.Plan.OutputFile)
		showFileDiff(ctx, cfg, pr.Plan.OutputFile)
	} else {
		fmt.Println(out)
		if err := os.MkdirAll("results", 0o755); err != nil {
			die(err)
		}
		path := filepath.Join("results", "filetask_"+safeFileLabel(pr.Plan.Scenario)+".md")
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			die(err)
		}
		fmt.Printf("Отчёт: %s\n", path)
	}
}

// executeFilePlan исполняет поиски и чтения плана через MCP files и собирает
// текстовый контекст для генерации.
func executeFilePlan(ctx stdctx.Context, c *mcpx.Client, plan *agent.FilePlan) string {
	var sb strings.Builder
	for _, s := range plan.Searches {
		args := map[string]any{"pattern": s.Pattern, "max_results": 50}
		if s.Path != "" {
			args["path"] = s.Path
		}
		if s.FilePattern != "" {
			args["file_pattern"] = s.FilePattern
		}
		out, err := c.CallTool(ctx, "search_files", args)
		if err != nil {
			fmt.Fprintf(&sb, "## Поиск: %s (ошибка: %v)\n", s.Pattern, err)
			continue
		}
		var res struct {
			Total   int `json:"total"`
			Matches []struct {
				File    string `json:"file"`
				Line    int    `json:"line"`
				Content string `json:"content"`
			} `json:"matches"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			continue
		}
		fmt.Fprintf(&sb, "## Поиск: %s (совпадений: %d)\n", s.Pattern, res.Total)
		for _, m := range res.Matches {
			fmt.Fprintf(&sb, "- %s:%d: %s\n", m.File, m.Line, m.Content)
		}
	}
	for _, p := range plan.Reads {
		out, err := c.CallTool(ctx, "read_file", map[string]any{"path": p, "max_lines": 200})
		if err != nil {
			fmt.Fprintf(&sb, "## Файл: %s (ошибка: %v)\n", p, err)
			continue
		}
		var r struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			continue
		}
		fmt.Fprintf(&sb, "\n## Файл: %s\n```\n%s\n```\n", r.Path, r.Content)
	}
	return sb.String()
}

// showFileDiff показывает git diff по записанному файлу (через MCP git).
// Для нового (untracked) файла git diff пуст — показываем содержимое.
func showFileDiff(ctx stdctx.Context, cfg *agent.Config, path string) {
	c, err := mcpx.Connect(ctx, cfg.MCPCommand, "--server", mcpx.ServerGit)
	if err != nil {
		return
	}
	defer c.Close()
	out, err := c.CallTool(ctx, "git_diff", map[string]any{"path": path, "full": true, "max_lines": 100})
	if err != nil {
		return
	}
	var d struct {
		Diff string `json:"diff"`
	}
	if json.Unmarshal([]byte(out), &d) == nil && strings.TrimSpace(d.Diff) != "" {
		fmt.Println("\n--- diff ---")
		fmt.Println(d.Diff)
		return
	}

	// Новый файл: показываем содержимое (первые 40 строк).
	fc, err := mcpx.Connect(ctx, cfg.MCPCommand, "--server", mcpx.ServerFiles)
	if err != nil {
		return
	}
	defer fc.Close()
	ro, err := fc.CallTool(ctx, "read_file", map[string]any{"path": path, "max_lines": 40})
	if err != nil {
		return
	}
	var r struct {
		Content string `json:"content"`
	}
	if json.Unmarshal([]byte(ro), &r) == nil && strings.TrimSpace(r.Content) != "" {
		fmt.Println("\n--- новый файл (содержимое) ---")
		fmt.Println(r.Content)
	}
}

// sanitizeFilePlan — защита от мусорного плана LLM: неизвестный сценарий →
// отчёт (find), плейсхолдеры в output_file отбрасываются, generate/docs
// получают файл по умолчанию.
func sanitizeFilePlan(plan *agent.FilePlan) {
	switch plan.Scenario {
	case "find", "check", "diff":
		plan.OutputFile = ""
	case "generate", "docs":
		// выходной файл валидируем ниже
	default:
		plan.Scenario = "find"
		plan.OutputFile = ""
	}

	of := strings.TrimLeft(plan.OutputFile, "/")
	if of == "" || strings.Contains(of, " ") || strings.Contains(of, "путь") ||
		strings.Contains(of, "path") || strings.Contains(of, "файл") {
		of = ""
	}
	if plan.Scenario == "generate" || plan.Scenario == "docs" {
		if of == "" {
			of = "agent/docs/generated.md"
		}
	} else {
		of = ""
	}
	plan.OutputFile = of
}

// fallbackNote — пометка, что план построен fallback-парсером.
func fallbackNote(fallback bool) string {
	if fallback {
		return " (fallback-план)"
	}
	return ""
}

// safeFileLabel — безопасное имя файла отчёта из сценария.
func safeFileLabel(s string) string {
	if strings.TrimSpace(s) == "" {
		return "task"
	}
	return s
}

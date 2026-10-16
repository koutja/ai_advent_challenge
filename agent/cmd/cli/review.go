package main

import (
	stdctx "context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agent"
	mcpx "agent/feature/mcp"
	"agent/feature/memory"
)

// reviewDiff — ответ MCP-инструмента git_diff (нужные поля).
type reviewDiff struct {
	Diff         string   `json:"diff"`
	Stat         string   `json:"stat"`
	NameStatus   []string `json:"name_status"`
	FilesChanged int      `json:"files_changed"`
	Insertions   int      `json:"insertions"`
	Deletions    int      `json:"deletions"`
	Truncated    bool     `json:"truncated"`
}

// runReview — ручное AI-ревью кода (флаг --review [base]):
// diff и изменённые файлы через MCP git → Agent.Review (RAG-контекст проекта
// + LLM) → отчёт «баги / архитектура / рекомендации» в консоль и results/.
//
//	--review         — незакоммиченные изменения (рабочая копия)
//	--review main    — ревью PR: git diff main...HEAD
func runReview(cfg *agent.Config, base string) {
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

	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 120*time.Second)
	defer cancel()

	// 1. Diff + изменённые файлы через MCP git.
	label := "working"
	args := map[string]any{"full": true, "name_status": true, "max_lines": reviewMaxDiffLines}
	if base != "" {
		args["base"] = base
		label = safeReviewLabel(base)
	}

	c, err := mcpx.Connect(ctx, cfg.MCPCommand, "--server", mcpx.ServerGit)
	if err != nil {
		die(fmt.Errorf("MCP git недоступен: %w", err))
	}
	defer c.Close()

	out, err := c.CallTool(ctx, "git_diff", args)
	if err != nil {
		die(err)
	}
	var d reviewDiff
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		die(fmt.Errorf("git_diff: результат не JSON: %w", err))
	}

	if strings.TrimSpace(d.Diff) == "" {
		fmt.Printf("Изменений нет (%s) — ревьюить нечего.\n", reviewBaseLabel(base))
		return
	}

	fmt.Printf("[review: база %s, файлов: %d, +%d −%d%s]\n",
		reviewBaseLabel(base), d.FilesChanged, d.Insertions, d.Deletions, reviewTruncNote(d.Truncated))
	for _, f := range d.NameStatus {
		fmt.Printf("  %s\n", f)
	}

	// 2. Ревью: RAG-контекст проекта + LLM.
	rr, err := ag.Review(d.Diff, strings.Join(d.NameStatus, "\n"))
	if err != nil {
		die(err)
	}

	// 3. Печать отчёта.
	printReview(rr)

	// 4. Сохранение отчёта в results/.
	if err := os.MkdirAll("results", 0o755); err != nil {
		die(err)
	}
	path := filepath.Join("results", "review_"+label+".md")
	if err := os.WriteFile(path, []byte(renderReviewMarkdown(reviewBaseLabel(base), d, rr)), 0o644); err != nil {
		die(err)
	}
	fmt.Printf("Отчёт: %s\n", path)
}

// printReview печатает отчёт ревью в консоль (секции или сырой текст) + RAG-источники.
func printReview(rr *agent.ReviewResult) {
	fmt.Println()
	if rr.FormatOK {
		printReviewSection("Потенциальные баги", rr.Bugs)
		printReviewSection("Архитектурные проблемы", rr.Architecture)
		printReviewSection("Рекомендации", rr.Recommendations)
	} else {
		fmt.Println(rr.Text)
	}
	if rr.Engine != "" {
		fmt.Printf("\n[RAG: движок %s, источников: %d", rr.Engine, len(rr.Sources))
		if rr.FormatOK {
			fmt.Print(", формат секций: ✓")
		}
		if rr.Stats != nil && rr.Stats.TotalTokens > 0 {
			fmt.Printf(", токенов: %d", rr.Stats.TotalTokens)
		}
		fmt.Println("]")
		for i, s := range rr.Sources {
			fmt.Printf("  %d. %s | секция: %s\n", i+1, s.Source, orText(s.Section, "—"))
		}
	}
}

// printReviewSection печатает одну секцию отчёта.
func printReviewSection(title string, items []string) {
	fmt.Printf("### %s\n", title)
	if len(items) == 0 {
		fmt.Println("  —")
		return
	}
	for _, it := range items {
		fmt.Printf("- %s\n", it)
	}
	fmt.Println()
}

// renderReviewMarkdown собирает итоговый markdown-отчёт для results/.
func renderReviewMarkdown(base string, d reviewDiff, rr *agent.ReviewResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# AI-ревью кода (база: %s)\n\n", base)
	fmt.Fprintf(&sb, "Файлов: %d | +%d −%d%s\n\n", d.FilesChanged, d.Insertions, d.Deletions, reviewTruncNote(d.Truncated))
	if len(d.NameStatus) > 0 {
		sb.WriteString("Изменённые файлы:\n")
		for _, f := range d.NameStatus {
			fmt.Fprintf(&sb, "- %s\n", f)
		}
		sb.WriteString("\n")
	}
	if rr.FormatOK {
		sb.WriteString("## Потенциальные баги\n")
		for _, it := range rr.Bugs {
			fmt.Fprintf(&sb, "- %s\n", it)
		}
		sb.WriteString("\n## Архитектурные проблемы\n")
		for _, it := range rr.Architecture {
			fmt.Fprintf(&sb, "- %s\n", it)
		}
		sb.WriteString("\n## Рекомендации\n")
		for _, it := range rr.Recommendations {
			fmt.Fprintf(&sb, "- %s\n", it)
		}
	} else {
		sb.WriteString("## Полный текст отчёта\n\n")
		sb.WriteString(rr.Text + "\n")
	}
	sb.WriteString("\n---\n")
	if rr.Engine != "" {
		fmt.Fprintf(&sb, "RAG-контекст: движок %s, источников %d\n", rr.Engine, len(rr.Sources))
		for _, s := range rr.Sources {
			fmt.Fprintf(&sb, "- %s | секция: %s\n", s.Source, orText(s.Section, "—"))
		}
	}
	if rr.Stats != nil {
		fmt.Fprintf(&sb, "Токены: prompt=%d completion=%d total=%d, модель: %s\n",
			rr.Stats.RequestTokens, rr.Stats.ResponseTokens, rr.Stats.TotalTokens, rr.Stats.Model)
	}
	sb.WriteString("Сгенерировано: " + time.Now().Format("2006-01-02 15:04:05") + "\n")
	return sb.String()
}

// reviewMaxDiffLines — лимит строк diff, отдаваемых LLM.
const reviewMaxDiffLines = 600

// reviewBaseLabel — человекочитаемое название базы.
func reviewBaseLabel(base string) string {
	if strings.TrimSpace(base) == "" {
		return "рабочая копия"
	}
	return base
}

// reviewTruncNote — пометка об обрезке diff.
func reviewTruncNote(truncated bool) string {
	if truncated {
		return ", diff обрезан"
	}
	return ""
}

// safeReviewLabel — безопасное имя файла отчёта из базы.
var reviewSafeRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeReviewLabel(base string) string {
	return reviewSafeRe.ReplaceAllString(strings.TrimSpace(base), "_")
}

package mcpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// gitRunner — обёртка над локальным git CLI для MCP-инструментов домена git.
//
// Инструменты выполняются через os/exec (без внешних Go-зависимостей):
//   - git_branch — текущая ветка + краткий статус рабочей копии;
//   - git_status — статус рабочей копии (краткий --short или полный);
//   - git_diff   — незакоммиченные изменения: сводка --stat и/или полный diff;
//   - git_files  — список отслеживаемых файлов (с фильтром по подстроке).
//
// Корень репозитория резолвится один раз через `git rev-parse --show-toplevel`
// (относительно рабочей папки процесса), дальше все команды идут с -C.
// Если git недоступен или папка не репозиторий — инструменты возвращают
// понятную ошибку, сервер не падает.
type gitRunner struct {
	mu   sync.Mutex
	root string // корень репозитория (кэш)
	dir  string // рабочая папка для git (пусто — cwd процесса; задаётся в тестах)
}

// repoRoot возвращает корень git-репозитория (кэшируется после первого вызова).
func (g *gitRunner) repoRoot(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.root != "" {
		return g.root, nil
	}
	args := []string{"rev-parse", "--show-toplevel"}
	if g.dir != "" {
		args = append([]string{"-C", g.dir}, args...)
	}
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("git недоступен или папка не репозиторий: %w", err)
	}
	g.root = strings.TrimSpace(string(out))
	return g.root, nil
}

// run выполняет git-команду в корне репозитория и возвращает stdout.
func (g *gitRunner) run(ctx context.Context, args ...string) (string, error) {
	root, err := g.repoRoot(ctx)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return string(out), nil
}

// --- Инструменты ---

// gitBranchOutput — результат git_branch.
type gitBranchOutput struct {
	Branch string   `json:"branch"` // текущая ветка (например main)
	Status []string `json:"status"` // изменённые файлы (краткий статус)
	Clean  bool     `json:"clean"`  // рабочая копия чистая
}

// branch возвращает текущую ветку и краткий статус (git status --short --branch).
func (g *gitRunner) branch(ctx context.Context) (gitBranchOutput, error) {
	out, err := g.run(ctx, "status", "--short", "--branch")
	if err != nil {
		return gitBranchOutput{}, err
	}
	res := gitBranchOutput{Status: []string{}}
	for _, l := range nonEmptyLines(out) {
		if strings.HasPrefix(l, "## ") {
			res.Branch = strings.TrimPrefix(l, "## ")
			continue
		}
		res.Status = append(res.Status, l)
	}
	res.Clean = len(res.Status) == 0
	return res, nil
}

// gitStatusOutput — результат git_status.
type gitStatusOutput struct {
	Branch string   `json:"branch"` // текущая ветка
	Clean  bool     `json:"clean"`  // рабочая копия чистая
	Lines  []string `json:"lines"`  // строки статуса
}

// status возвращает статус рабочей копии: краткий (--short --branch) или полный.
func (g *gitRunner) status(ctx context.Context, porcelain bool) (gitStatusOutput, error) {
	args := []string{"status"}
	if porcelain {
		args = append(args, "--short", "--branch")
	}
	out, err := g.run(ctx, args...)
	if err != nil {
		return gitStatusOutput{}, err
	}
	lines := nonEmptyLines(out)
	res := gitStatusOutput{Lines: lines}
	if len(lines) > 0 {
		first := lines[0]
		if porcelain {
			res.Branch = strings.TrimPrefix(first, "## ")
		} else {
			res.Branch = strings.TrimPrefix(first, "On branch ")
		}
	}
	res.Clean = len(lines) <= 1 || strings.Contains(out, "nothing to commit")
	return res, nil
}

// gitDiffOutput — результат git_diff.
type gitDiffOutput struct {
	Stat         string `json:"stat"`           // сводка git diff --stat
	Diff         string `json:"diff,omitempty"` // полный diff (если запрошен)
	FilesChanged int    `json:"files_changed"`
	Insertions   int    `json:"insertions"`
	Deletions    int    `json:"deletions"`
	Truncated    bool   `json:"truncated"`
}

// diff возвращает незакоммиченные изменения: всегда сводку --stat, а при
// full/path — полный diff (по пути или по всему репозиторию), ограниченный
// maxLines строками (0 — без лимита).
func (g *gitRunner) diff(ctx context.Context, path string, full bool, maxLines int) (gitDiffOutput, error) {
	res := gitDiffOutput{}
	out, err := g.run(ctx, "diff", "--stat")
	if err != nil {
		return res, err
	}
	res.Stat = strings.TrimSpace(out)
	res.FilesChanged, res.Insertions, res.Deletions = parseDiffStat(res.Stat)

	if full || path != "" {
		args := []string{"diff"}
		if path != "" {
			args = append(args, "--", path)
		}
		out, err := g.run(ctx, args...)
		if err != nil {
			return res, err
		}
		res.Diff, res.Truncated = limitLines(out, maxLines)
	}
	return res, nil
}

// gitFilesOutput — результат git_files.
type gitFilesOutput struct {
	Total int      `json:"total"`
	Files []string `json:"files"`
}

// files возвращает список отслеживаемых файлов (git ls-files), опционально
// отфильтрованный по подстроке и ограниченный top элементами.
func (g *gitRunner) files(ctx context.Context, pattern string, top int) (gitFilesOutput, error) {
	out, err := g.run(ctx, "ls-files")
	if err != nil {
		return gitFilesOutput{}, err
	}
	all := nonEmptyLines(out)
	res := gitFilesOutput{Files: []string{}}
	if pattern != "" {
		pat := strings.ToLower(pattern)
		for _, f := range all {
			if strings.Contains(strings.ToLower(f), pat) {
				res.Files = append(res.Files, f)
			}
		}
	} else {
		res.Files = all
	}
	if top > 0 && len(res.Files) > top {
		res.Files = res.Files[:top]
	}
	res.Total = len(res.Files)
	return res, nil
}

// GitContext собирает git-контекст проекта через MCP-инструменты домена git
// (git_branch, git_diff, git_files) и возвращает текстовую сводку и имя ветки.
// Используется ассистентом разработчика (/help) в CLI и web: сводка попадает
// в системный промпт SayHelp как «Git-контекст проекта».
func GitContext(ctx context.Context, command string) (string, string, error) {
	c, err := Connect(ctx, command, "--server", ServerGit)
	if err != nil {
		return "", "", err
	}
	defer c.Close()

	var sb strings.Builder
	branch := ""

	// git_branch — ветка + краткий статус.
	if out, err := c.CallTool(ctx, "git_branch", nil); err == nil {
		var b gitBranchOutput
		if json.Unmarshal([]byte(out), &b) == nil {
			branch = b.Branch
			fmt.Fprintf(&sb, "Ветка: %s\n", b.Branch)
			if b.Clean {
				sb.WriteString("Рабочая копия чистая.\n")
			} else {
				fmt.Fprintf(&sb, "Изменённые файлы (%d):\n", len(b.Status))
				for _, s := range b.Status {
					fmt.Fprintf(&sb, "  %s\n", s)
				}
			}
		}
	}

	// git_diff — сводка незакоммиченных изменений (--stat).
	if out, err := c.CallTool(ctx, "git_diff", nil); err == nil {
		var d gitDiffOutput
		if json.Unmarshal([]byte(out), &d) == nil && strings.TrimSpace(d.Stat) != "" {
			fmt.Fprintf(&sb, "Незакоммиченные изменения:\n%s\n", d.Stat)
		}
	}

	// git_files — первые файлы репозитория (для вопросов о структуре).
	if out, err := c.CallTool(ctx, "git_files", map[string]any{"top": 20}); err == nil {
		var f gitFilesOutput
		if json.Unmarshal([]byte(out), &f) == nil && len(f.Files) > 0 {
			fmt.Fprintf(&sb, "Файлы репозитория (первые %d из %d):\n", len(f.Files), f.Total)
			for _, fn := range f.Files {
				fmt.Fprintf(&sb, "  %s\n", fn)
			}
		}
	}

	return strings.TrimSpace(sb.String()), branch, nil
}

// --- Регистрация инструментов ---

// addGitTools регистрирует инструменты домена git (локальный git CLI).
func addGitTools(s *mcp.Server) {
	g := &gitRunner{}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "git_branch",
		Description: "Возвращает текущую git-ветку и краткий статус рабочей копии (изменённые файлы).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, gitBranchOutput, error) {
		res, err := g.branch(ctx)
		if err != nil {
			return nil, gitBranchOutput{}, err
		}
		return nil, res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "git_status",
		Description: "Возвращает статус рабочей копии git: краткий (--short) или полный.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitStatusInput) (*mcp.CallToolResult, gitStatusOutput, error) {
		res, err := g.status(ctx, in.Porcelain)
		if err != nil {
			return nil, gitStatusOutput{}, err
		}
		return nil, res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "git_diff",
		Description: "Возвращает незакоммиченные изменения: сводку git diff --stat и/или полный diff по пути.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitDiffInput) (*mcp.CallToolResult, gitDiffOutput, error) {
		res, err := g.diff(ctx, in.Path, in.Full, in.MaxLines)
		if err != nil {
			return nil, gitDiffOutput{}, err
		}
		return nil, res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "git_files",
		Description: "Возвращает список отслеживаемых git-файлов репозитория (опционально фильтр по подстроке и лимит).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitFilesInput) (*mcp.CallToolResult, gitFilesOutput, error) {
		res, err := g.files(ctx, in.Pattern, in.Top)
		if err != nil {
			return nil, gitFilesOutput{}, err
		}
		return nil, res, nil
	})
}

// --- Входные структуры (jsonschema-теги попадают в схему инструмента) ---

type gitStatusInput struct {
	// Porcelain — краткий формат (--short); по умолчанию false (полный статус).
	Porcelain bool `json:"porcelain,omitempty" jsonschema:"краткий формат --short; по умолчанию false (полный статус)"`
}

type gitDiffInput struct {
	// Path — ограничить diff одним путём (например agent/agent.go); пусто — весь репозиторий.
	Path string `json:"path,omitempty" jsonschema:"ограничить diff одним путём, например agent/agent.go; пусто — весь репозиторий"`
	// Full — вернуть полный diff (не только сводку --stat); по умолчанию false.
	Full bool `json:"full,omitempty" jsonschema:"вернуть полный diff (не только сводку --stat); по умолчанию false"`
	// MaxLines — лимит строк полного diff; 0 — без лимита.
	MaxLines int `json:"max_lines,omitempty" jsonschema:"лимит строк полного diff; 0 — без лимита"`
}

type gitFilesInput struct {
	// Pattern — фильтр по подстроке имени файла (регистронезависимо).
	Pattern string `json:"pattern,omitempty" jsonschema:"фильтр по подстроке имени файла (регистронезависимо)"`
	// Top — максимум файлов в ответе; 0 — все.
	Top int `json:"top,omitempty" jsonschema:"максимум файлов в ответе; 0 — все"`
}

// --- Вспомогательные функции ---

// nonEmptyLines разбивает вывод на непустые строки.
func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimRight(l, "\r"))
		}
	}
	return out
}

// limitLines обрезает текст до max строк (0 — без лимита) и сообщает об обрезке.
func limitLines(s string, max int) (string, bool) {
	if max <= 0 {
		return strings.TrimSpace(s), false
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return strings.TrimSpace(s), false
	}
	return strings.Join(lines[:max], "\n") + "\n… (обрезано)", true
}

// diffStatRe — итоговая строка git diff --stat:
// " N files changed, X insertions(+), Y deletions(-)".
var diffStatRe = regexp.MustCompile(`(\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?`)

// parseDiffStat извлекает из сводки --stat числа изменённых файлов/вставок/удалений.
func parseDiffStat(stat string) (files, insertions, deletions int) {
	lines := strings.Split(stat, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		m := diffStatRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		files = atoi(m[1])
		if m[2] != "" {
			insertions = atoi(m[2])
		}
		if m[3] != "" {
			deletions = atoi(m[3])
		}
		return
	}
	return 0, 0, 0
}

// atoi — простой парсер неотрицательного целого (без ошибок).
func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

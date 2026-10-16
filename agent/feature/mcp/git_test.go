package mcpx

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestGitHelperServer — helper-процесс для домена git (см. runHelperServer в mcp_test.go).
// Имя НЕ начинается с TestHelperServer, чтобы не попадать под -test.run=TestHelperServer
// (иначе в subprocess первым запускался бы git-домен вместо домена all).
func TestGitHelperServer(t *testing.T) { runHelperServer(t, ServerGit) }

// TestGitToolsViaMCP проверяет домен git через MCP: tools/list содержит 4
// git-инструмента, git_branch возвращает текущую ветку репозитория.
func TestGitToolsViaMCP(t *testing.T) {
	t.Setenv("MCP_HELPER", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	c, err := Connect(ctx, os.Args[0], "-test.run=TestGitHelperServer")
	if err != nil {
		t.Fatalf("Connect (MCP-соединение): %v", err)
	}
	defer c.Close()

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := make(map[string]bool, len(tools))
	for _, ti := range tools {
		got[ti.Name] = true
	}
	for _, name := range []string{"git_branch", "git_status", "git_diff", "git_files"} {
		if !got[name] {
			t.Errorf("инструмент %q не в списке; получено: %v", name, got)
		}
	}

	out, err := c.CallTool(ctx, "git_branch", nil)
	if err != nil {
		t.Fatalf("CallTool git_branch: %v", err)
	}
	var b gitBranchOutput
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("git_branch не JSON: %v (%s)", err, out)
	}
	if b.Branch == "" {
		t.Errorf("git_branch вернул пустую ветку: %s", out)
	}
}

// --- Прямые тесты gitRunner (реальный git в текущем репозитории) ---

func TestGitRunnerBranch(t *testing.T) {
	g := &gitRunner{}
	b, err := g.branch(context.Background())
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	if b.Branch == "" {
		t.Error("branch: пустая ветка")
	}
}

func TestGitRunnerStatus(t *testing.T) {
	g := &gitRunner{}
	s, err := g.status(context.Background(), true)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if s.Branch == "" {
		t.Error("status: пустая ветка")
	}
}

func TestGitRunnerDiff(t *testing.T) {
	g := &gitRunner{}
	d, err := g.diff(context.Background(), "", "", false, false, 0)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	// stat может быть пустым (чистая рабочая копия), но ошибки быть не должно.
	_ = d
}

// TestGitRunnerDiffBase — diff диапазона <base>...HEAD (ревью PR).
func TestGitRunnerDiffBase(t *testing.T) {
	g := &gitRunner{}
	// HEAD~1...HEAD — diff последнего коммита (в репозитории есть история).
	d, err := g.diff(context.Background(), "HEAD~1", "", false, true, 0)
	if err != nil {
		t.Fatalf("diff(base=HEAD~1): %v", err)
	}
	if d.Diff == "" {
		t.Error("diff(base=HEAD~1): ожидали непустой полный diff последнего коммита")
	}
}

// TestGitRunnerDiffNameStatus — список изменённых файлов (M/A/D).
func TestGitRunnerDiffNameStatus(t *testing.T) {
	g := &gitRunner{}
	d, err := g.diff(context.Background(), "", "", true, false, 0)
	if err != nil {
		t.Fatalf("diff(name_status): %v", err)
	}
	for _, ns := range d.NameStatus {
		if len(ns) < 3 || (ns[0] != 'M' && ns[0] != 'A' && ns[0] != 'D' && ns[0] != 'R') {
			t.Errorf("неожиданная строка name-status: %q", ns)
		}
	}
}

func TestGitRunnerFiles(t *testing.T) {
	g := &gitRunner{}
	f, err := g.files(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	if f.Total == 0 {
		t.Error("files: пустой список")
	}
	found := false
	for _, fn := range f.Files {
		if strings.Contains(fn, "README.md") {
			found = true
			break
		}
	}
	if !found {
		t.Error("files: README.md не найден в списке файлов")
	}
}

// TestGitRunnerNotARepo — папка вне репозитория → понятная ошибка, без паники.
func TestGitRunnerNotARepo(t *testing.T) {
	g := &gitRunner{dir: t.TempDir()}
	if _, err := g.repoRoot(context.Background()); err == nil {
		t.Error("ожидали ошибку для папки вне репозитория")
	}
}

// TestGitContextError — несуществующая команда сервера → ошибка.
func TestGitContextError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, _, err := GitContext(ctx, "/nonexistent/mcp-server"); err == nil {
		t.Error("ожидали ошибку для несуществующей команды сервера")
	}
}

// --- Чистые функции ---

func TestParseDiffStat(t *testing.T) {
	cases := []struct {
		stat    string
		f, i, d int
	}{
		{" 1 file changed, 2 insertions(+), 1 deletion(-)", 1, 2, 1},
		{" 3 files changed, 12 insertions(+), 4 deletions(-)", 3, 12, 4},
		{" 1 file changed, 1 insertion(+)", 1, 1, 0},
		{" 1 file changed", 1, 0, 0},
		{"", 0, 0, 0},
	}
	for _, c := range cases {
		f, i, d := parseDiffStat(c.stat)
		if f != c.f || i != c.i || d != c.d {
			t.Errorf("parseDiffStat(%q) = %d,%d,%d; want %d,%d,%d", c.stat, f, i, d, c.f, c.i, c.d)
		}
	}
}

func TestLimitLines(t *testing.T) {
	text := "a\nb\nc\nd"
	got, truncated := limitLines(text, 2)
	if !truncated || !strings.Contains(got, "…") {
		t.Errorf("limitLines(4 строки, 2) = %q, truncated=%v; ожидали обрезку", got, truncated)
	}
	got, truncated = limitLines(text, 0)
	if truncated || got != text {
		t.Errorf("limitLines(4 строки, 0) = %q, truncated=%v; ожидали без обрезки", got, truncated)
	}
}

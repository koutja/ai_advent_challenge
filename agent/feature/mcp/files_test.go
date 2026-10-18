package mcpx

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestFilesHelperServer — helper-процесс для домена files (см. runHelperServer).
// Имя НЕ начинается с TestHelperServer, чтобы не попадать под -test.run=TestHelperServer.
func TestFilesHelperServer(t *testing.T) { runHelperServer(t, ServerFiles) }

// TestFilesToolsViaMCP — домен files через MCP: tools/list, read_file,
// search_files, write_file round-trip.
func TestFilesToolsViaMCP(t *testing.T) {
	t.Setenv("MCP_HELPER", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	c, err := Connect(ctx, os.Args[0], "-test.run=TestFilesHelperServer")
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
	for _, name := range []string{"read_file", "search_files", "list_files", "write_file"} {
		if !got[name] {
			t.Errorf("инструмент %q не в списке; получено: %v", name, got)
		}
	}

	// read_file: README агента не пуст.
	out, err := c.CallTool(ctx, "read_file", map[string]any{"path": "agent/README.md", "max_lines": 5})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	var rd readFileOutput
	if err := json.Unmarshal([]byte(out), &rd); err != nil {
		t.Fatalf("read_file не JSON: %v (%s)", err, out)
	}
	if rd.Content == "" || rd.Lines < 1 {
		t.Errorf("read_file: пустой результат: %+v", rd)
	}

	// search_files: llm.New встречается в коде.
	out, err = c.CallTool(ctx, "search_files", map[string]any{"pattern": `llm\.New`, "max_results": 10})
	if err != nil {
		t.Fatalf("search_files: %v", err)
	}
	var sf searchFilesOutput
	if err := json.Unmarshal([]byte(out), &sf); err != nil {
		t.Fatalf("search_files не JSON: %v (%s)", err, out)
	}
	if sf.Total == 0 {
		t.Error("search_files: не найдено ни одного совпадения llm.New")
	}

	// write_file → read_file round-trip (в results/, gitignored).
	tmp := "results/files_test_tmp.md"
	if _, err := c.CallTool(ctx, "write_file", map[string]any{"path": tmp, "content": "# Тест\n\nсодержимое"}); err != nil {
		t.Fatalf("write_file: %v", err)
	}
	out, err = c.CallTool(ctx, "read_file", map[string]any{"path": tmp})
	if err != nil {
		t.Fatalf("read_file (round-trip): %v", err)
	}
	var rd2 readFileOutput
	if err := json.Unmarshal([]byte(out), &rd2); err != nil {
		t.Fatalf("read_file (round-trip) не JSON: %v", err)
	}
	if !strings.Contains(rd2.Content, "содержимое") {
		t.Errorf("write_file/read_file round-trip: %+v", rd2)
	}
}

// TestFilesRunnerSafety — защита: пути вне репозитория и .env запрещены.
func TestFilesRunnerSafety(t *testing.T) {
	f := &filesRunner{}
	ctx := context.Background()

	if _, err := f.resolve(ctx, "../.env"); err == nil {
		t.Error("ожидали ошибку для пути вне репозитория")
	}
	if _, err := f.writeFile(ctx, ".env", "LLM_API_KEY=secret"); err == nil {
		t.Error("ожидали запрет записи в .env")
	}
	if _, err := f.writeFile(ctx, ".git/config", "x"); err == nil {
		t.Error("ожидали запрет записи в .git/")
	}
}

// TestFilesRunnerReadSearch — прямые вызовы runner: чтение и поиск.
func TestFilesRunnerReadSearch(t *testing.T) {
	f := &filesRunner{}
	ctx := context.Background()

	rd, err := f.readFile(ctx, "agent/README.md", 10)
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if rd.Content == "" {
		t.Error("readFile: пустой README")
	}

	// llm.New вызывается в коде агента/дней (в llm/ — сама библиотека).
	sf, err := f.searchFiles(ctx, `llm\.New`, "agent", "", 5)
	if err != nil {
		t.Fatalf("searchFiles: %v", err)
	}
	if sf.Total == 0 {
		t.Error("searchFiles: llm.New не найден в agent/")
	}
	for _, m := range sf.Matches {
		if !strings.HasPrefix(m.File, "agent/") {
			t.Errorf("searchFiles: неожиданный файл: %q", m.File)
		}
	}
}

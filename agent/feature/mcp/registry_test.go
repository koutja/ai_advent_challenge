package mcpx

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helperSpecs — спецификации трёх тестовых MCP-серверов (по доменам).
func helperSpecs() []ServerSpec {
	return []ServerSpec{
		{Name: ServerTasks, Command: os.Args[0], Args: []string{"-test.run=TestHelperServerTasks"}},
		{Name: ServerScheduler, Command: os.Args[0], Args: []string{"-test.run=TestHelperServerScheduler"}},
		{Name: ServerKnowledge, Command: os.Args[0], Args: []string{"-test.run=TestHelperServerKnowledge"}},
	}
}

// TestRegistryRouting: оркестратор собирает инструменты со всех серверов и
// корректно маршрутизирует вызовы к владельцу.
func TestRegistryRouting(t *testing.T) {
	t.Setenv("MCP_HELPER", "1")
	t.Setenv("MCP_DATA_FILE", filepath.Join(t.TempDir(), "data.json"))
	isolateCorpus(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reg := NewRegistry(helperSpecs())
	if err := reg.ConnectAll(ctx); err != nil {
		t.Fatalf("ConnectAll: %v", err)
	}
	defer reg.Close()

	// Выбор сервера по владельцу инструмента.
	wantOwner := map[string]string{
		"get_task":     ServerTasks,
		"create_task":  ServerTasks,
		"search":       ServerKnowledge,
		"summarize":    ServerKnowledge,
		"save_to_file": ServerKnowledge,
		"reminder_add": ServerScheduler,
		"get_summary":  ServerScheduler,
	}
	for tool, want := range wantOwner {
		got, ok := reg.Owner(tool)
		if !ok || got != want {
			t.Errorf("owner(%s) = %q (ok=%v), ожидали %q", tool, got, ok, want)
		}
	}

	// Маршрутизированный вызов попадает на правильный сервер.
	res, err := reg.CallTool(ctx, "create_task", map[string]any{"title": "Задача через оркестратор", "priority": "high"})
	if err != nil {
		t.Fatalf("CallTool create_task: %v", err)
	}
	if res.Server != ServerTasks {
		t.Errorf("маршрут create_task: %q, ожидали %q", res.Server, ServerTasks)
	}

	// Инструмента нет ни на одном сервере -> ошибка.
	if _, err := reg.CallTool(ctx, "get_time", nil); err == nil {
		t.Errorf("ожидали ошибку для get_time (его нет в доменах)")
	}
}

// TestRegistryDuplicateTools: один и тот же инструмент в двух серверах —
// неоднозначная маршрутизация, ConnectAll обязан упасть.
func TestRegistryDuplicateTools(t *testing.T) {
	t.Setenv("MCP_HELPER", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	specs := []ServerSpec{
		{Name: "k1", Command: os.Args[0], Args: []string{"-test.run=TestHelperServerKnowledge"}},
		{Name: "k2", Command: os.Args[0], Args: []string{"-test.run=TestHelperServerKnowledge"}},
	}
	reg := NewRegistry(specs)
	err := reg.ConnectAll(ctx)
	reg.Close()
	if err == nil {
		t.Fatalf("ожидали ошибку о дубликате инструментов")
	}
	if !strings.Contains(err.Error(), "двух серверах") {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
}

// TestOrchestrateFlow: длинный флоу с инструментами РАЗНЫХ серверов.
// Проверяются и выбор сервера, и порядок вызовов, и передача данных между шагами.
func TestOrchestrateFlow(t *testing.T) {
	t.Setenv("MCP_HELPER", "1")
	t.Setenv("MCP_DATA_FILE", filepath.Join(t.TempDir(), "data.json"))
	outDir := t.TempDir()
	t.Setenv("MCP_OUTPUT_DIR", outDir)
	isolateCorpus(t)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	reg := NewRegistry(helperSpecs())
	if err := reg.ConnectAll(ctx); err != nil {
		t.Fatalf("ConnectAll: %v", err)
	}
	defer reg.Close()

	// 1) search (knowledge) — получить данные.
	sr, err := reg.CallTool(ctx, "search", map[string]any{"query": "mcp", "limit": 2})
	if err != nil || sr.Server != ServerKnowledge {
		t.Fatalf("search: server=%q err=%v", sr.Server, err)
	}
	var searchRes SearchResult
	if err := json.Unmarshal([]byte(sr.Output), &searchRes); err != nil || len(searchRes.Docs) == 0 {
		t.Fatalf("search не вернул документы: %+v err=%v", searchRes, err)
	}

	// 2) summarize (knowledge) — на вход данные из шага 1.
	var parts []string
	for _, d := range searchRes.Docs {
		parts = append(parts, d.Title+". "+d.Snippet)
	}
	su, err := reg.CallTool(ctx, "summarize", map[string]any{"text": strings.Join(parts, "\n\n"), "max_words": 40})
	if err != nil || su.Server != ServerKnowledge {
		t.Fatalf("summarize: server=%q err=%v", su.Server, err)
	}
	var sumRes SummarizeResult
	if err := json.Unmarshal([]byte(su.Output), &sumRes); err != nil {
		t.Fatalf("summarize не JSON: %v", err)
	}

	// 3) create_task (tasks) — сводка из шага 2 становится задачей.
	tk, err := reg.CallTool(ctx, "create_task", map[string]any{"title": sumRes.Summary, "priority": "high"})
	if err != nil || tk.Server != ServerTasks {
		t.Fatalf("create_task: server=%q err=%v", tk.Server, err)
	}
	var taskRes Task
	if err := json.Unmarshal([]byte(tk.Output), &taskRes); err != nil || taskRes.ID == "" {
		t.Fatalf("create_task не вернул задачу: %+v err=%v", taskRes, err)
	}

	// 4) reminder_add (scheduler) — ссылка на задачу из шага 3.
	rm, err := reg.CallTool(ctx, "reminder_add", map[string]any{"text": "Проверить " + taskRes.ID, "in_minutes": 0})
	if err != nil || rm.Server != ServerScheduler {
		t.Fatalf("reminder_add: server=%q err=%v", rm.Server, err)
	}

	// 5) get_summary (scheduler) — напоминание видно в агрегате.
	gs, err := reg.CallTool(ctx, "get_summary", nil)
	if err != nil || gs.Server != ServerScheduler {
		t.Fatalf("get_summary: server=%q err=%v", gs.Server, err)
	}
	var summary getSummaryOutput
	if err := json.Unmarshal([]byte(gs.Output), &summary); err != nil {
		t.Fatalf("get_summary не JSON: %v", err)
	}
	if summary.Reminders.Total < 1 {
		t.Errorf("в сводке нет напоминаний: %+v", summary.Reminders)
	}

	// 6) save_to_file (knowledge) — отчёт из всего флоу.
	sf, err := reg.CallTool(ctx, "save_to_file", map[string]any{"filename": "flow_report.md", "content": "# Отчёт\n" + sumRes.Summary + "\n"})
	if err != nil || sf.Server != ServerKnowledge {
		t.Fatalf("save_to_file: server=%q err=%v", sf.Server, err)
	}
	var saveRes SaveFileResult
	if err := json.Unmarshal([]byte(sf.Output), &saveRes); err != nil {
		t.Fatalf("save_to_file не JSON: %v", err)
	}
	data, err := os.ReadFile(saveRes.Path)
	if err != nil {
		t.Fatalf("чтение отчёта: %v", err)
	}
	if !strings.Contains(string(data), sumRes.Summary) {
		t.Fatalf("отчёт не содержит сводку: %q", data)
	}
}

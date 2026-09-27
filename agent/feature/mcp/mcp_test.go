package mcpx

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestHelperServer — не настоящий тест, а сервер-помощник. Когда тестовый
// бинарник запускается как subprocess с MCP_HELPER=1, этот тест превращается
// в stdio MCP-сервер. Это позволяет проверить полный цикл «клиент по stdio ->
// отдельный серверный процесс» без внешних бинарников.
func TestHelperServer(t *testing.T) {
	if os.Getenv("MCP_HELPER") != "1" {
		t.Skip("helper-процесс для запуска in-process MCP-сервера")
	}
	server := NewServer()
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		t.Fatalf("helper server: %v", err)
	}
}

// TestConnectAndListTools проверяет оба обязательных требования:
//   - соединение с MCP устанавливается;
//   - список инструментов корректно возвращается.
//
// Клиент (Connect) сам запускает тестовый бинарник как сервер по stdio.
func TestConnectAndListTools(t *testing.T) {
	t.Setenv("MCP_HELPER", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	c, err := Connect(ctx, os.Args[0], "-test.run=TestHelperServer")
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

	want := []string{"get_time", "echo", "get_task", "create_task"}
	for _, name := range want {
		if !got[name] {
			t.Errorf("инструмент %q не в списке; получено: %v", name, got)
		}
	}
}

// TestCallTool проверяет полный цикл вызова MCP-инструмента:
// клиент вызывает create_task и get_task (инструменты вокруг mock HTTP API),
// получает и разбирает результат.
func TestCallTool(t *testing.T) {
	t.Setenv("MCP_HELPER", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	c, err := Connect(ctx, os.Args[0], "-test.run=TestHelperServer")
	if err != nil {
		t.Fatalf("Connect (MCP-соединение): %v", err)
	}
	defer c.Close()

	// create_task -> возвращает созданную задачу как JSON.
	created, err := c.CallTool(ctx, "create_task", map[string]any{
		"title":    "Тестовая задача",
		"priority": "high",
		"assignee": "bot",
	})
	if err != nil {
		t.Fatalf("CallTool create_task: %v", err)
	}
	var createdTask Task
	if err := json.Unmarshal([]byte(created), &createdTask); err != nil {
		t.Fatalf("результат create_task не JSON: %v (%s)", err, created)
	}
	if createdTask.ID == "" || createdTask.Title != "Тестовая задача" || createdTask.Priority != "high" {
		t.Errorf("create_task вернул некорректную задачу: %+v", createdTask)
	}

	// get_task по созданному id -> возвращает ту же задачу.
	got, err := c.CallTool(ctx, "get_task", map[string]any{"task_id": createdTask.ID})
	if err != nil {
		t.Fatalf("CallTool get_task: %v", err)
	}
	var gotTask Task
	if err := json.Unmarshal([]byte(got), &gotTask); err != nil {
		t.Fatalf("результат get_task не JSON: %v (%s)", err, got)
	}
	if gotTask.ID != createdTask.ID || gotTask.Title != createdTask.Title {
		t.Errorf("get_task вернул не ту задачу: got=%+v want=%+v", gotTask, createdTask)
	}

	// Несуществующая задача -> инструмент вернул ошибку (IsError).
	if _, err := c.CallTool(ctx, "get_task", map[string]any{"task_id": "T-999"}); err == nil {
		t.Errorf("ожидали ошибку для несуществующей задачи T-999")
	} else if !strings.Contains(err.Error(), "T-999") {
		t.Errorf("ошибка не содержит id задачи: %v", err)
	}
}

// TestSchedulerTools проверяет планировщик через MCP: напоминание срабатывает
// по расписанию, периодический сбор собирает точки, get_summary возвращает
// агрегированную сводку. Хранилище — во временном файле (MCP_DATA_FILE).
func TestSchedulerTools(t *testing.T) {
	t.Setenv("MCP_HELPER", "1")
	t.Setenv("MCP_DATA_FILE", filepath.Join(t.TempDir(), "mcp_data.json"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := Connect(ctx, os.Args[0], "-test.run=TestHelperServer")
	if err != nil {
		t.Fatalf("Connect (MCP-соединение): %v", err)
	}
	defer c.Close()

	// Напоминание на ближайший тик.
	rem, err := c.CallTool(ctx, "reminder_add", map[string]any{"text": "Пора в спортзал", "in_minutes": 0})
	if err != nil {
		t.Fatalf("reminder_add: %v", err)
	}
	if !strings.Contains(rem, "R-") || !strings.Contains(rem, "Пора в спортзал") {
		t.Fatalf("reminder_add: неожиданный ответ: %s", rem)
	}

	// Периодический сбор: 3 точки с интервалом 1 с.
	if _, err := c.CallTool(ctx, "collect_start", map[string]any{"metric": "cpu", "interval_seconds": 1, "iterations": 3}); err != nil {
		t.Fatalf("collect_start: %v", err)
	}

	// Ждём, пока тикер отработает несколько интервалов.
	time.Sleep(3500 * time.Millisecond)

	sum, err := c.CallTool(ctx, "get_summary", nil)
	if err != nil {
		t.Fatalf("get_summary: %v", err)
	}
	var sv getSummaryOutput
	if err := json.Unmarshal([]byte(sum), &sv); err != nil {
		t.Fatalf("get_summary не JSON: %v (%s)", err, sum)
	}
	if sv.Reminders.Fired < 1 {
		t.Errorf("напоминание не сработало: %+v", sv.Reminders)
	}
	m, ok := sv.Metrics["cpu"]
	if !ok || m.Count < 2 {
		t.Errorf("сбор данных не отработал: %+v", sv.Metrics)
	}
	if m.Count > 3 || m.Done > 3 {
		t.Errorf("iterations=3 не соблюдено: %+v", m)
	}

	// collect_status: агрегат по метрике.
	cs, err := c.CallTool(ctx, "collect_status", map[string]any{"metric": "cpu"})
	if err != nil {
		t.Fatalf("collect_status: %v", err)
	}
	var cv map[string]metricView
	if err := json.Unmarshal([]byte(cs), &cv); err != nil {
		t.Fatalf("collect_status не JSON: %v (%s)", err, cs)
	}
	v, ok := cv["cpu"]
	if !ok || v.Count != m.Count {
		t.Errorf("collect_status disagrees: %+v vs %+v", cv, m)
	}
}

// TestPipelineComposition проверяет автоматическую цепочку MCP-инструментов:
// search → summarize → save_to_file. Корректность передачи данных: сводка
// строится по найденным документам (шаг 2 потребляет вывод шага 1), а файл
// содержит сводку (шаг 3 потребляет вывод шага 2).
func TestPipelineComposition(t *testing.T) {
	t.Setenv("MCP_HELPER", "1")
	outDir := t.TempDir()
	t.Setenv("MCP_OUTPUT_DIR", outDir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := Connect(ctx, os.Args[0], "-test.run=TestHelperServer")
	if err != nil {
		t.Fatalf("Connect (MCP-соединение): %v", err)
	}
	defer c.Close()

	// Шаг 1: search — получает данные.
	sr, err := c.CallTool(ctx, "search", map[string]any{"query": "mcp", "limit": 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var searchRes SearchResult
	if err := json.Unmarshal([]byte(sr), &searchRes); err != nil {
		t.Fatalf("search не JSON: %v (%s)", err, sr)
	}
	if len(searchRes.Docs) == 0 {
		t.Fatalf("search вернул пустой список")
	}

	// Шаг 2: summarize — на вход подаём контент из шага 1.
	var parts []string
	for _, d := range searchRes.Docs {
		parts = append(parts, d.Title+". "+d.Snippet)
	}
	su, err := c.CallTool(ctx, "summarize", map[string]any{"text": strings.Join(parts, "\n\n"), "max_words": 40})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	var sumRes SummarizeResult
	if err := json.Unmarshal([]byte(su), &sumRes); err != nil {
		t.Fatalf("summarize не JSON: %v (%s)", err, su)
	}
	if sumRes.Summary == "" || !strings.Contains(strings.ToLower(sumRes.Summary), "mcp") {
		t.Fatalf("сводка не содержит данных из шага 1: %+v", sumRes)
	}
	if sumRes.Sources != len(searchRes.Docs) {
		t.Fatalf("sources=%d, ожидали %d документов", sumRes.Sources, len(searchRes.Docs))
	}

	// Шаг 3: save_to_file — сохраняем сводку из шага 2.
	sf, err := c.CallTool(ctx, "save_to_file", map[string]any{"filename": "pipeline_mcp.md", "content": sumRes.Summary})
	if err != nil {
		t.Fatalf("save_to_file: %v", err)
	}
	var saveRes SaveFileResult
	if err := json.Unmarshal([]byte(sf), &saveRes); err != nil {
		t.Fatalf("save_to_file не JSON: %v (%s)", err, sf)
	}
	if !strings.HasPrefix(filepath.Clean(saveRes.Path), filepath.Clean(outDir)) {
		t.Fatalf("файл записан вне MCP_OUTPUT_DIR: %s", saveRes.Path)
	}
	data, err := os.ReadFile(saveRes.Path)
	if err != nil {
		t.Fatalf("чтение сохранённого файла: %v", err)
	}
	if string(data) != sumRes.Summary {
		t.Fatalf("файл не содержит сводку: %q != %q", data, sumRes.Summary)
	}
}

// TestGenerateDocumentTool: инструмент generate_document через клиент создаёт
// документ (fallback при недоступном LLM), а search потом его находит — так
// пайплайн «дозаполняет» корпус для неизвестных тем.
func TestGenerateDocumentTool(t *testing.T) {
	t.Setenv("MCP_HELPER", "1")
	t.Setenv("MCP_CORPUS_FILE", filepath.Join(t.TempDir(), "corpus.json"))
	t.Setenv("LLM_API_KEY", "test-key")
	t.Setenv("LLM_BASE_URL", "http://127.0.0.1:9") // гарантированно недоступен

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := Connect(ctx, os.Args[0], "-test.run=TestHelperServer")
	if err != nil {
		t.Fatalf("Connect (MCP-соединение): %v", err)
	}
	defer c.Close()

	gd, err := c.CallTool(ctx, "generate_document", map[string]any{"topic": "ruby"})
	if err != nil {
		t.Fatalf("generate_document: %v", err)
	}
	var gen GeneratedDoc
	if err := json.Unmarshal([]byte(gd), &gen); err != nil {
		t.Fatalf("generate_document не JSON: %v (%s)", err, gd)
	}
	if gen.Source != "fallback" || gen.ID == "" {
		t.Fatalf("неожиданный документ: %+v", gen)
	}

	sr, err := c.CallTool(ctx, "search", map[string]any{"query": "ruby"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var searchRes SearchResult
	if err := json.Unmarshal([]byte(sr), &searchRes); err != nil {
		t.Fatalf("search не JSON: %v (%s)", err, sr)
	}
	found := false
	for _, d := range searchRes.Docs {
		if d.ID == gen.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("search не нашёл сгенерированный документ %s: %+v", gen.ID, searchRes.Docs)
	}
}

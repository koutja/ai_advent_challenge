package mcpx

import (
	"context"
	"encoding/json"
	"os"
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

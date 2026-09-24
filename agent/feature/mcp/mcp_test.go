package mcpx

import (
	"context"
	"os"
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

	want := []string{"get_time", "echo"}
	for _, name := range want {
		if !got[name] {
			t.Errorf("инструмент %q не в списке; получено: %v", name, got)
		}
	}
}

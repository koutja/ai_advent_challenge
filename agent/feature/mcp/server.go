// Package mcpx — минимальная интеграция Model Context Protocol (MCP) в ядро агента.
//
// Пакет построен на официальном Go SDK: github.com/modelcontextprotocol/go-sdk.
// Здесь два компонента:
//
//   - NewServer — переиспользуемый MCP-сервер с парой демо-инструментов.
//     Из него собирается отдельный бинарник cmd/mcp-server (запускается как
//     отдельный процесс), а также используется в тестах.
//   - Connect — stdio-клиент, который запускает сервер как дочерний процесс
//     и устанавливает соединение (handshake Initialize).
//
// Такой расклад («сервер отдельным процессом, клиент подключается по stdio»)
// близок к реальному деплою MCP-серверов.
package mcpx

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Идентификация демо-сервера, который сообщается клиенту при Initialize.
const (
	ServerName    = "agent-mcp-demo"
	ServerVersion = "1.0.0"
)

// NewServer собирает MCP-сервер с набором демо-инструментов.
// Используется cmd/mcp-server и тестами (сервер гоняется как subprocess).
func NewServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: ServerVersion}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_time",
		Description: "Возвращает текущее время сервера в формате RFC3339.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, string, error) {
		return nil, time.Now().Format(time.RFC3339), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "echo",
		Description: "Возвращает переданное сообщение без изменений.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, echoOutput, error) {
		return nil, echoOutput{Echo: in.Message}, nil
	})

	return s
}

// echoInput — аргументы инструмента echo (схема выводится из типа автоматически).
type echoInput struct {
	// Message — текст для возврата.
	Message string `json:"message" jsonschema:"текст, который нужно вернуть"`
}

// echoOutput — результат инструмента echo.
type echoOutput struct {
	// Echo — текст, полученный от клиента.
	Echo string `json:"echo" jsonschema:"возвращённый текст"`
}

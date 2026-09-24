// Package mcpx — интеграция Model Context Protocol (MCP) в ядро агента.
//
// Пакет построен на официальном Go SDK: github.com/modelcontextprotocol/go-sdk.
// Здесь два компонента:
//
//   - NewServer — переиспользуемый MCP-сервер с инструментами. Из него собирается
//     отдельный бинарник cmd/mcp-server (запускается как отдельный процесс),
//     а также используется в тестах.
//   - Connect — stdio-клиент, который запускает сервер как дочерний процесс
//     и устанавливает соединение (handshake Initialize). Клиент умеет не только
//     перечислять инструменты (ListTools), но и вызывать их (CallTool).
//
// Инструменты get_task и create_task оборачивают встроенный mock HTTP-API
// (см. api.go): хендлеры инструмента обращаются к нему обычным HTTP-клиентом,
// как к внешнему сервису, и возвращают структурированный результат.
//
// Такой расклад («сервер отдельным процессом, клиент подключается по stdio»)
// близок к реальному деплою MCP-серверов.
package mcpx

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Идентификация сервера, который сообщается клиенту при Initialize.
const (
	ServerName    = "agent-mcp-demo"
	ServerVersion = "1.0.0"
)

// NewServer собирает MCP-сервер с набором инструментов, включая инструменты
// поверх встроенного mock HTTP-API.
func NewServer() *mcp.Server {
	api := newMockAPI()
	return newServerWithAPI(api)
}

// newServerWithAPI собирает сервер с заданным mock API. Разделено, чтобы
// тесты могли управлять жизненным циклом API, если нужно.
func newServerWithAPI(api *mockAPI) *mcp.Server {
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

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_task",
		Description: "Возвращает задачу из mock API по её id.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in getTaskInput) (*mcp.CallToolResult, Task, error) {
		t, err := api.getTask(in.TaskID)
		if err != nil {
			return nil, Task{}, fmt.Errorf("get_task: %w", err)
		}
		return nil, t, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_task",
		Description: "Создаёт задачу в mock API и возвращает её полную запись.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in createTaskInput) (*mcp.CallToolResult, Task, error) {
		t, err := api.createTask(in.Title, in.Priority, in.Assignee)
		if err != nil {
			return nil, Task{}, fmt.Errorf("create_task: %w", err)
		}
		return nil, t, nil
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

// getTaskInput — аргументы инструмента get_task: описание входных параметров
// попадает в JSON-схему инструмента автоматически (из jsonschema-тегов).
type getTaskInput struct {
	// TaskID — идентификатор задачи в mock API (например T-001).
	TaskID string `json:"task_id" jsonschema:"идентификатор задачи, например T-001"`
}

// createTaskInput — аргументы инструмента create_task.
type createTaskInput struct {
	// Title — заголовок создаваемой задачи (обязательно).
	Title string `json:"title" jsonschema:"заголовок задачи (обязательное поле)"`
	// Priority — приоритет: low|medium|high (по умолчанию medium).
	Priority string `json:"priority,omitempty" jsonschema:"приоритет: low|medium|high (по умолчанию medium)"`
	// Assignee — исполнитель задачи (необязательно).
	Assignee string `json:"assignee,omitempty" jsonschema:"исполнитель задачи (необязательно)"`
}

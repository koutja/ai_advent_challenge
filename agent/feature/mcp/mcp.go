package mcpx

import (
	"context"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolInfo — краткое описание инструмента, которое клиент отдаёт наружу.
// Не привязывает вызывающий код к типам go-sdk.
type ToolInfo struct {
	Name        string
	Description string
}

// Client — обёртка над stdio-клиентом MCP. Держит открытую сессию к
// отдельно запущенному серверному процессу (cmd/mcp-server).
type Client struct {
	session *mcp.ClientSession
}

// Connect запускает указанную команду сервера как дочерний процесс и
// подключается к ней по stdio (CommandTransport), выполняя handshake
// Initialize. Возвращает ошибку, если сервер не ответил.
//
//	command: бинарник/команда MCP-сервера (например "bin/mcp-server")
//	args:    аргументы команды (необязательно)
func Connect(ctx context.Context, command string, args ...string) (*Client, error) {
	cmd := exec.Command(command, args...)
	transport := &mcp.CommandTransport{Command: cmd}
	client := mcp.NewClient(&mcp.Implementation{Name: "agent-mcp-client", Version: "1.0.0"}, nil)

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	return &Client{session: session}, nil
}

// ListTools возвращает список инструментов, которые объявил подключённый
// MCP-сервер (запрос tools/list).
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	res, err := c.session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	infos := make([]ToolInfo, 0, len(res.Tools))
	for _, t := range res.Tools {
		infos = append(infos, ToolInfo{Name: t.Name, Description: t.Description})
	}
	return infos, nil
}

// Close завершает соединение и дожидается выхода серверного процесса.
func (c *Client) Close() error {
	return c.session.Close()
}

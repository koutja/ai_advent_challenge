// Команда mcp-server — автономный MCP-сервер демо-инструментов.
//
// Запускается как отдельный процесс и общается с клиентом по stdio
// (newline-delimited JSON). Клиент (например, cli --mcp-tools) поднимает этот
// бинарник через CommandTransport и запрашивает tools/list.
//
// Запуск:  go run ./cmd/mcp-server   (или собранный bin/mcp-server)
package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpx "agent/feature/mcp"
)

func main() {
	server := mcpx.NewServer()
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("mcp-server: %v", err)
	}
}

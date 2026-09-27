// Команда mcp-server — автономный MCP-сервер.
//
// Запускается как отдельный процесс и общается с клиентом по stdio
// (newline-delimited JSON). Один бинарник может обслуживать разные «домены»
// инструментов через флаг --server:
//
//	--server tasks       — get_task / create_task (mock HTTP API)
//	--server scheduler   — напоминания, периодический сбор, сводки
//	--server knowledge   — search / summarize / save_to_file / generate_document
//	--server all         — демо + все домены (по умолчанию)
//
// Оркестратор (feature/mcp/registry.go) запускает несколько таких процессов
// и маршрутизирует вызовы инструментов между ними.
//
// Запуск:  go run ./cmd/mcp-server [--server <domain>]
package main

import (
	"context"
	"flag"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpx "agent/feature/mcp"
)

func main() {
	domain := flag.String("server", "all", "домен инструментов: tasks|scheduler|knowledge|all")
	flag.Parse()

	server := mcpx.BuildServer(*domain)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("mcp-server (%s): %v", *domain, err)
	}
}

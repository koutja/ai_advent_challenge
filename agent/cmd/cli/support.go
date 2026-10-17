package main

import (
	stdctx "context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent"
	mcpx "agent/feature/mcp"
	"agent/feature/memory"
)

// supportTicket — ответ MCP get_ticket (нужные поля: тикет + пользователь).
type supportTicket struct {
	Ticket struct {
		ID       string `json:"id"`
		UserID   string `json:"user_id"`
		Subject  string `json:"subject"`
		Message  string `json:"message"`
		Status   string `json:"status"`
		Priority string `json:"priority"`
		Reply    string `json:"reply"`
	} `json:"ticket"`
	User struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
		Plan  string `json:"plan"`
	} `json:"user"`
}

// runSupport — ассистент поддержки (флаг --support "<вопрос>" [--ticket <id>]):
// контекст тикета/пользователя через MCP (домен support, JSON-CRM) + RAG по
// FAQ/документации (индекс index_service) → ответ с источниками.
func runSupport(cfg *agent.Config, question, ticketID string) {
	mem, err := memory.NewLayeredSQLite(cfg.HistoryFile, cfg.LongMemoryFile)
	if err != nil {
		die(err)
	}
	defer mem.Close()

	ag, err := agent.New(cfg, mem)
	if err != nil {
		die(err)
	}
	if ag.Retriever() == nil {
		die(fmt.Errorf("RAG не настроен: добавьте секцию rag в config.json"))
	}

	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 90*time.Second)
	defer cancel()

	// Контекст тикета через MCP (JSON-CRM).
	ticketCtx := ""
	if strings.TrimSpace(ticketID) != "" {
		ticketCtx = fetchTicketContext(ctx, cfg, ticketID)
	}

	reply, err := ag.SupportAnswer(question, ticketCtx)
	if err != nil {
		die(err)
	}
	fmt.Println(reply.Text)
	printChatSources(reply)
}

// fetchTicketContext получает тикет + пользователя через MCP get_ticket и
// собирает текстовый контекст для промпта. При недоступности CRM отвечает
// без контекста (graceful degradation).
func fetchTicketContext(ctx stdctx.Context, cfg *agent.Config, ticketID string) string {
	c, err := mcpx.Connect(ctx, cfg.MCPCommand, "--server", mcpx.ServerSupport)
	if err != nil {
		fmt.Printf("[MCP support недоступен: %v — отвечаю без контекста тикета]\n", err)
		return ""
	}
	defer c.Close()

	out, err := c.CallTool(ctx, "get_ticket", map[string]any{"ticket_id": ticketID})
	if err != nil {
		fmt.Printf("[get_ticket %s: %v — отвечаю без контекста тикета]\n", ticketID, err)
		return ""
	}
	var t supportTicket
	if err := json.Unmarshal([]byte(out), &t); err != nil {
		fmt.Printf("[get_ticket: результат не JSON: %v]\n", err)
		return ""
	}

	fmt.Printf("[тикет %s: %s | статус: %s | приоритет: %s | пользователь: %s (%s, план %s)]\n",
		t.Ticket.ID, t.Ticket.Subject, t.Ticket.Status, t.Ticket.Priority,
		t.User.Name, t.User.Email, orText(t.User.Plan, "—"))

	var sb strings.Builder
	fmt.Fprintf(&sb, "Тикет %s: %s\n", t.Ticket.ID, t.Ticket.Subject)
	fmt.Fprintf(&sb, "Статус: %s, приоритет: %s\n", t.Ticket.Status, t.Ticket.Priority)
	fmt.Fprintf(&sb, "Обращение: %s\n", t.Ticket.Message)
	if t.Ticket.Reply != "" {
		fmt.Fprintf(&sb, "Предыдущий ответ: %s\n", t.Ticket.Reply)
	}
	fmt.Fprintf(&sb, "Пользователь: %s (%s), план: %s\n", t.User.Name, t.User.Email, orText(t.User.Plan, "—"))
	return sb.String()
}

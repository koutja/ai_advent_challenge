package mcpx

import (
	"context"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// openDefaultCRM открывает JSON-CRM: путь из env SUPPORT_CRM_FILE, иначе
// results/support_crm.json (git игнорирует **/results/). При любой ошибке
// продолжает работу в памяти (без падения сервера).
func openDefaultCRM() *CRMStore {
	path := defaultCRMFile
	if p := os.Getenv("SUPPORT_CRM_FILE"); p != "" {
		path = p
	}
	st, err := OpenCRM(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "support: CRM %s недоступен (%v), работаю в памяти\n", path, err)
		st, _ = OpenCRM("")
	}
	return st
}

// ticketWithUser — тикет с контекстом пользователя (результат get_ticket).
type ticketWithUser struct {
	Ticket CRMTicket `json:"ticket"`
	User   CRMUser   `json:"user"`
}

// addSupportTools регистрирует инструменты домена support (JSON-CRM):
// чтение пользователей/тикетов + создание и обновление тикетов.
func addSupportTools(s *mcp.Server, crm *CRMStore) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_user",
		Description: "Возвращает пользователя CRM по id (имя, email, план).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in getCRMUserInput) (*mcp.CallToolResult, CRMUser, error) {
		u, err := crm.User(in.UserID)
		if err != nil {
			return nil, CRMUser{}, err
		}
		return nil, u, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_ticket",
		Description: "Возвращает тикет поддержки по id вместе с контекстом пользователя (для ответа с учётом данных тикета).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in getCRMTicketInput) (*mcp.CallToolResult, ticketWithUser, error) {
		t, err := crm.Ticket(in.TicketID)
		if err != nil {
			return nil, ticketWithUser{}, err
		}
		u, err := crm.User(t.UserID)
		if err != nil {
			return nil, ticketWithUser{}, err
		}
		return nil, ticketWithUser{Ticket: t, User: u}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_tickets",
		Description: "Возвращает тикеты поддержки (опционально по статусу open|in_progress|resolved).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in listTicketsInput) (*mcp.CallToolResult, []CRMTicket, error) {
		return nil, crm.Tickets(in.Status), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_ticket",
		Description: "Создаёт тикет поддержки в JSON-CRM (пользователь должен существовать) и возвращает его запись.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in createTicketInput) (*mcp.CallToolResult, CRMTicket, error) {
		t, err := crm.CreateTicket(in.UserID, in.Subject, in.Message, in.Priority)
		if err != nil {
			return nil, CRMTicket{}, err
		}
		return nil, t, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_ticket",
		Description: "Обновляет тикет: статус (open|in_progress|resolved) и/или ответ поддержки.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in updateTicketInput) (*mcp.CallToolResult, CRMTicket, error) {
		t, err := crm.UpdateTicket(in.TicketID, in.Status, in.Reply)
		if err != nil {
			return nil, CRMTicket{}, err
		}
		return nil, t, nil
	})
}

// --- Входные структуры (jsonschema-теги попадают в схему инструмента) ---

type getCRMUserInput struct {
	// UserID — идентификатор пользователя (например U-001).
	UserID string `json:"user_id" jsonschema:"идентификатор пользователя, например U-001"`
}

type getCRMTicketInput struct {
	// TicketID — идентификатор тикета (например T-001).
	TicketID string `json:"ticket_id" jsonschema:"идентификатор тикета, например T-001"`
}

type listTicketsInput struct {
	// Status — фильтр по статусу: open|in_progress|resolved; пусто — все.
	Status string `json:"status,omitempty" jsonschema:"фильтр по статусу: open|in_progress|resolved; пусто — все"`
}

type createTicketInput struct {
	// UserID — пользователь, создающий тикет (обязательно).
	UserID string `json:"user_id" jsonschema:"пользователь, создающий тикет (обязательное поле)"`
	// Subject — тема тикета (обязательно).
	Subject string `json:"subject" jsonschema:"тема тикета (обязательное поле)"`
	// Message — текст обращения (обязательно).
	Message string `json:"message" jsonschema:"текст обращения (обязательное поле)"`
	// Priority — приоритет: low|medium|high (по умолчанию medium).
	Priority string `json:"priority,omitempty" jsonschema:"приоритет: low|medium|high (по умолчанию medium)"`
}

type updateTicketInput struct {
	// TicketID — идентификатор тикета (обязательно).
	TicketID string `json:"ticket_id" jsonschema:"идентификатор тикета (обязательное поле)"`
	// Status — новый статус: open|in_progress|resolved.
	Status string `json:"status,omitempty" jsonschema:"новый статус: open|in_progress|resolved"`
	// Reply — ответ поддержки (добавляется в тикет).
	Reply string `json:"reply,omitempty" jsonschema:"ответ поддержки (добавляется в тикет)"`
}

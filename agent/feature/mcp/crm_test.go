package mcpx

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSupportHelperServer — helper-процесс для домена support (см. runHelperServer).
// Имя НЕ начинается с TestHelperServer, чтобы не попадать под -test.run=TestHelperServer.
func TestSupportHelperServer(t *testing.T) { runHelperServer(t, ServerSupport) }

// TestCRMStoreSeed — при первом запуске CRM создаётся с демо-данными.
func TestCRMStoreSeed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.json")
	st, err := OpenCRM(path)
	if err != nil {
		t.Fatalf("OpenCRM: %v", err)
	}
	if len(st.Tickets("")) != 3 {
		t.Errorf("ожидали 3 seed-тикета, получили %d", len(st.Tickets("")))
	}
	if _, err := st.User("U-001"); err != nil {
		t.Errorf("seed-пользователь U-001 не найден: %v", err)
	}
	if _, err := st.Ticket("T-001"); err != nil {
		t.Errorf("seed-тикет T-001 не найден: %v", err)
	}
}

// TestCRMStoreCreateTicketPersists — созданный тикет сохраняется в JSON и
// переживает перезапуск (round-trip).
func TestCRMStoreCreateTicketPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.json")
	st, err := OpenCRM(path)
	if err != nil {
		t.Fatalf("OpenCRM: %v", err)
	}

	created, err := st.CreateTicket("U-002", "Новый вопрос", "Как запустить web-чат?", "low")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if created.ID == "" || created.Status != "open" || created.Priority != "low" {
		t.Errorf("некорректный тикет: %+v", created)
	}

	// Переоткрываем файл — тикет на месте.
	st2, err := OpenCRM(path)
	if err != nil {
		t.Fatalf("OpenCRM (reopen): %v", err)
	}
	got, err := st2.Ticket(created.ID)
	if err != nil {
		t.Fatalf("Ticket после перезапуска: %v", err)
	}
	if got.Subject != "Новый вопрос" || got.UserID != "U-002" {
		t.Errorf("тикет после round-trip: %+v", got)
	}
}

// TestCRMStoreUpdateTicket — обновление статуса и ответа.
func TestCRMStoreUpdateTicket(t *testing.T) {
	st, err := OpenCRM(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatalf("OpenCRM: %v", err)
	}
	upd, err := st.UpdateTicket("T-001", "resolved", "Проверьте LLM_API_KEY в .env")
	if err != nil {
		t.Fatalf("UpdateTicket: %v", err)
	}
	if upd.Status != "resolved" || !strings.Contains(upd.Reply, "LLM_API_KEY") {
		t.Errorf("обновлённый тикет: %+v", upd)
	}
}

// TestCRMStoreErrors — понятные ошибки: нет пользователя/тикета, пустые поля.
func TestCRMStoreErrors(t *testing.T) {
	st, err := OpenCRM(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatalf("OpenCRM: %v", err)
	}
	if _, err := st.User("U-999"); err == nil {
		t.Error("ожидали ошибку для несуществующего пользователя")
	}
	if _, err := st.Ticket("T-999"); err == nil {
		t.Error("ожидали ошибку для несуществующего тикета")
	}
	if _, err := st.CreateTicket("U-999", "x", "y", ""); err == nil {
		t.Error("ожидали ошибку для несуществующего пользователя при создании")
	}
	if _, err := st.CreateTicket("U-001", "  ", "y", ""); err == nil {
		t.Error("ожидали ошибку для пустой темы")
	}
}

// TestSupportToolsViaMCP — домен support через MCP: tools/list, get_ticket
// (тикет + пользователь), create_ticket, update_ticket.
func TestSupportToolsViaMCP(t *testing.T) {
	t.Setenv("MCP_HELPER", "1")
	t.Setenv("SUPPORT_CRM_FILE", filepath.Join(t.TempDir(), "crm.json"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	c, err := Connect(ctx, os.Args[0], "-test.run=TestSupportHelperServer")
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
	for _, name := range []string{"get_user", "get_ticket", "list_tickets", "create_ticket", "update_ticket"} {
		if !got[name] {
			t.Errorf("инструмент %q не в списке; получено: %v", name, got)
		}
	}

	// get_ticket: тикет + контекст пользователя.
	out, err := c.CallTool(ctx, "get_ticket", map[string]any{"ticket_id": "T-001"})
	if err != nil {
		t.Fatalf("get_ticket: %v", err)
	}
	var tw ticketWithUser
	if err := json.Unmarshal([]byte(out), &tw); err != nil {
		t.Fatalf("get_ticket не JSON: %v (%s)", err, out)
	}
	if tw.Ticket.ID != "T-001" || tw.User.ID != "U-001" || tw.User.Name == "" {
		t.Errorf("get_ticket: %+v", tw)
	}

	// create_ticket: создание в JSON-CRM.
	created, err := c.CallTool(ctx, "create_ticket", map[string]any{
		"user_id": "U-003", "subject": "Тест", "message": "Проверка создания", "priority": "high",
	})
	if err != nil {
		t.Fatalf("create_ticket: %v", err)
	}
	var ct CRMTicket
	if err := json.Unmarshal([]byte(created), &ct); err != nil {
		t.Fatalf("create_ticket не JSON: %v (%s)", err, created)
	}
	if ct.ID == "" || ct.Status != "open" || ct.Priority != "high" {
		t.Errorf("create_ticket: %+v", ct)
	}

	// update_ticket: статус + ответ.
	upd, err := c.CallTool(ctx, "update_ticket", map[string]any{
		"ticket_id": ct.ID, "status": "resolved", "reply": "Готово",
	})
	if err != nil {
		t.Fatalf("update_ticket: %v", err)
	}
	var ut CRMTicket
	if err := json.Unmarshal([]byte(upd), &ut); err != nil {
		t.Fatalf("update_ticket не JSON: %v (%s)", err, upd)
	}
	if ut.Status != "resolved" || ut.Reply != "Готово" {
		t.Errorf("update_ticket: %+v", ut)
	}

	// Несуществующий тикет → ошибка.
	if _, err := c.CallTool(ctx, "get_ticket", map[string]any{"ticket_id": "T-999"}); err == nil {
		t.Error("ожидали ошибку для несуществующего тикета")
	}
}

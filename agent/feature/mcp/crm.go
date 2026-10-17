package mcpx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// defaultCRMFile — путь JSON-CRM по умолчанию (git игнорирует **/results/).
const defaultCRMFile = "results/support_crm.json"

// CRMUser — пользователь продукта.
type CRMUser struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Plan      string `json:"plan,omitempty"`
	CreatedAt string `json:"created_at"`
}

// CRMTicket — тикет поддержки.
type CRMTicket struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Subject   string `json:"subject"`
	Message   string `json:"message"`
	Status    string `json:"status"`   // open | in_progress | resolved
	Priority  string `json:"priority"` // low | medium | high
	Reply     string `json:"reply,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// CRMData — состояние CRM, сериализуемое в JSON-файл.
type CRMData struct {
	Users   []CRMUser   `json:"users"`
	Tickets []CRMTicket `json:"tickets"`
	Seq     int         `json:"seq"`
}

// CRMStore — потокобезопасное JSON-хранилище CRM (пользователи + тикеты).
// Атомарная запись: tmp-файл + rename. Пустой path — чисто in-memory режим.
// При первом запуске (файла нет) — создаётся с демо-данными (seed).
type CRMStore struct {
	path string
	mu   sync.Mutex
	data *CRMData
}

// OpenCRM открывает (или создаёт с seed) CRM по пути. При path == ""
// работает только в памяти (без сохранения на диск).
func OpenCRM(path string) (*CRMStore, error) {
	s := &CRMStore{path: path, data: &CRMData{Users: []CRMUser{}, Tickets: []CRMTicket{}}}
	if path == "" {
		return s, nil
	}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, s.data); err != nil {
			return nil, fmt.Errorf("чтение %s: %w", path, err)
		}
	case os.IsNotExist(err):
		if dir := filepath.Dir(path); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
		}
		seedCRM(s.data)
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	if s.data.Users == nil {
		s.data.Users = []CRMUser{}
	}
	if s.data.Tickets == nil {
		s.data.Tickets = []CRMTicket{}
	}
	return s, nil
}

// seedCRM заполняет демо-данные: пользователи + тикеты (включая пример про
// авторизацию из задания). Файл создаётся один раз, дальше живёт как источник.
func seedCRM(d *CRMData) {
	now := time.Now().Format(time.RFC3339)
	d.Users = []CRMUser{
		{ID: "U-001", Name: "Анна Смирнова", Email: "anna@example.com", Plan: "pro", CreatedAt: now},
		{ID: "U-002", Name: "Иван Петров", Email: "ivan@example.com", Plan: "free", CreatedAt: now},
		{ID: "U-003", Name: "Ольга Козлова", Email: "olga@example.com", Plan: "team", CreatedAt: now},
	}
	d.Tickets = []CRMTicket{
		{ID: "T-001", UserID: "U-001", Subject: "Не работает авторизация",
			Message: "При входе в web-чат получаю ошибку 401, ключ из .env не подходит.",
			Status:  "open", Priority: "high", CreatedAt: now, UpdatedAt: now},
		{ID: "T-002", UserID: "U-002", Subject: "Как подключить пакет llm",
			Message: "Не понимаю, как добавить aichallenge/llm в go.mod нового дня.",
			Status:  "open", Priority: "medium", CreatedAt: now, UpdatedAt: now},
		{ID: "T-003", UserID: "U-003", Subject: "RAG не находит документы",
			Message: "После make index поиск не возвращает чанки из README.",
			Status:  "in_progress", Priority: "medium", CreatedAt: now, UpdatedAt: now},
	}
	d.Seq = 3
}

// Path возвращает путь к файлу CRM ("" — in-memory).
func (s *CRMStore) Path() string { return s.path }

func (s *CRMStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// User возвращает пользователя по id (ошибка, если нет).
func (s *CRMStore) User(id string) (CRMUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.findUserLocked(id)
}

// Ticket возвращает тикет по id (ошибка, если нет).
func (s *CRMStore) Ticket(id string) (CRMTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.data.Tickets {
		if t.ID == id {
			return t, nil
		}
	}
	return CRMTicket{}, fmt.Errorf("тикет %q не найден", id)
}

// Tickets возвращает тикеты (опционально по статусу; пусто — все).
func (s *CRMStore) Tickets(status string) []CRMTicket {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []CRMTicket{}
	for _, t := range s.data.Tickets {
		if status == "" || t.Status == status {
			out = append(out, t)
		}
	}
	return out
}

// CreateTicket создаёт тикет (пользователь должен существовать) и сохраняет
// в JSON-файл — «база» переживает рестарты.
func (s *CRMStore) CreateTicket(userID, subject, message, priority string) (CRMTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.findUserLocked(userID); err != nil {
		return CRMTicket{}, err
	}
	if strings.TrimSpace(subject) == "" || strings.TrimSpace(message) == "" {
		return CRMTicket{}, fmt.Errorf("subject и message обязательны")
	}
	if priority == "" {
		priority = "medium"
	}
	s.data.Seq++
	now := time.Now().Format(time.RFC3339)
	t := CRMTicket{
		ID:        fmt.Sprintf("T-%03d", s.data.Seq),
		UserID:    userID,
		Subject:   subject,
		Message:   message,
		Status:    "open",
		Priority:  priority,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.data.Tickets = append(s.data.Tickets, t)
	return t, s.saveLocked()
}

// UpdateTicket обновляет статус и/или ответ тикета и сохраняет.
func (s *CRMStore) UpdateTicket(id, status, reply string) (CRMTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Tickets {
		if s.data.Tickets[i].ID != id {
			continue
		}
		if status != "" {
			s.data.Tickets[i].Status = status
		}
		if reply != "" {
			s.data.Tickets[i].Reply = reply
		}
		s.data.Tickets[i].UpdatedAt = time.Now().Format(time.RFC3339)
		return s.data.Tickets[i], s.saveLocked()
	}
	return CRMTicket{}, fmt.Errorf("тикет %q не найден", id)
}

func (s *CRMStore) findUserLocked(id string) (CRMUser, error) {
	for _, u := range s.data.Users {
		if u.ID == id {
			return u, nil
		}
	}
	return CRMUser{}, fmt.Errorf("пользователь %q не найден", id)
}

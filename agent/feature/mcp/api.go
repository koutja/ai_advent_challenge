package mcpx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Task — запись задачи в mock API. Структура отдаётся как JSON.
type Task struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
	Assignee string `json:"assignee,omitempty"`
}

// mockAPI — встроенный локальный HTTP-API с in-memory хранилищем задач.
// Живёт внутри процесса MCP-сервера (cmd/mcp-server): инструменты обращаются
// к нему обычным HTTP-клиентом — ровно как к внешнему API, но без сети и ключей.
//
// Эндпоинты:
//
//	GET  /tasks/{id}   — получить задачу по id (404, если нет)
//	POST /tasks        — создать задачу {title, priority?, assignee?} -> 201 + task
type mockAPI struct {
	baseURL string
	ln      net.Listener

	mu     sync.Mutex
	nextID int
	tasks  map[string]Task
}

// newMockAPI стартует локальный HTTP-сервер на 127.0.0.1:0 и кладёт
// несколько демо-задач. Сервер крутится в goroutine, поэтому живёт до конца
// процесса независимо от сборщика мусора.
func newMockAPI() *mockAPI {
	a := &mockAPI{
		nextID: 1,
		tasks:  map[string]Task{},
	}
	a.seed("Разобраться с MCP", "open", "high", "agent")
	a.seed("Написать тест CallTool", "in_progress", "medium", "")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /tasks/{id}", a.handleGet)
	mux.HandleFunc("POST /tasks", a.handleCreate)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(fmt.Sprintf("mock API: listen: %v", err))
	}
	a.ln = ln
	a.baseURL = "http://" + ln.Addr().String()

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }() // ошибки слушателя для demo не критичны
	return a
}

// Close останавливает локальный API (для тестов, вызывающих его напрямую).
func (a *mockAPI) Close() error { return a.ln.Close() }

// seed добавляет задачу с автогенерацией id и возвращает его.
func (a *mockAPI) seed(title, status, priority, assignee string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := fmt.Sprintf("T-%03d", a.nextID)
	a.nextID++
	a.tasks[id] = Task{ID: id, Title: title, Status: status, Priority: priority, Assignee: assignee}
	return id
}

func (a *mockAPI) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.mu.Lock()
	t, ok := a.tasks[id]
	a.mu.Unlock()
	if !ok {
		http.Error(w, fmt.Sprintf("task %q not found", id), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (a *mockAPI) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title    string `json:"title"`
		Priority string `json:"priority"`
		Assignee string `json:"assignee"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	prio := req.Priority
	if prio == "" {
		prio = "medium"
	}

	a.mu.Lock()
	id := fmt.Sprintf("T-%03d", a.nextID)
	a.nextID++
	t := Task{ID: id, Title: req.Title, Status: "open", Priority: prio, Assignee: req.Assignee}
	a.tasks[id] = t
	a.mu.Unlock()

	writeJSON(w, http.StatusCreated, t)
}

// --- HTTP-клиент, которым пользуются MCP-инструменты ---

// getTask вызывает GET /tasks/{id} и возвращает задачу или ошибку.
func (a *mockAPI) getTask(id string) (Task, error) {
	u := a.baseURL + "/tasks/" + url.PathEscape(id)
	resp, err := http.Get(u)
	if err != nil {
		return Task{}, fmt.Errorf("mock API get_task: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Task{}, fmt.Errorf("mock API get_task: статус %d (задача %q не найдена?)", resp.StatusCode, id)
	}
	var t Task
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return Task{}, fmt.Errorf("mock API get_task: декодирование ответа: %w", err)
	}
	return t, nil
}

// createTask вызывает POST /tasks и возвращает созданную задачу или ошибку.
func (a *mockAPI) createTask(title, priority, assignee string) (Task, error) {
	body, _ := json.Marshal(map[string]string{
		"title":    title,
		"priority": priority,
		"assignee": assignee,
	})
	resp, err := http.Post(a.baseURL+"/tasks", "application/json", bytes.NewReader(body))
	if err != nil {
		return Task{}, fmt.Errorf("mock API create_task: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return Task{}, fmt.Errorf("mock API create_task: статус %d", resp.StatusCode)
	}
	var t Task
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return Task{}, fmt.Errorf("mock API create_task: декодирование ответа: %w", err)
	}
	return t, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

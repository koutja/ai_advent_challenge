package mcpx

import (
	"context"
	"fmt"
	"sync"
)

// ServerSpec описывает один MCP-сервер: имя, команда запуска и аргументы.
type ServerSpec struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// DefaultServerSpecs возвращает три домена по умолчанию: один бинарник
// mcp-server, запущенный с разными --server (tasks | scheduler | knowledge).
func DefaultServerSpecs(command string) []ServerSpec {
	if command == "" {
		command = "bin/mcp-server"
	}
	return []ServerSpec{
		{Name: ServerTasks, Command: command, Args: []string{"--server", ServerTasks}},
		{Name: ServerScheduler, Command: command, Args: []string{"--server", ServerScheduler}},
		{Name: ServerKnowledge, Command: command, Args: []string{"--server", ServerKnowledge}},
	}
}

// CallResult — результат маршрутизированного вызова инструмента.
type CallResult struct {
	Server string // имя сервера, выполнившего вызов
	Output string // текстовый результат инструмента
}

// Registry — оркестратор нескольких MCP-серверов.
//
// Подключается к каждому серверу (ConnectAll), через tools/list строит
// глобальную таблицу «инструмент → сервер» (дубли имён — ошибка маршрутизации)
// и направляет CallTool на нужный сервер.
type Registry struct {
	specs   []ServerSpec
	clients map[string]*Client
	tools   map[string]ToolInfo
	owner   map[string]string

	mu sync.Mutex
}

// NewRegistry создаёт оркестратор (соединения ленивые, ConnectAll).
func NewRegistry(specs []ServerSpec) *Registry {
	return &Registry{
		specs:   specs,
		clients: map[string]*Client{},
		tools:   map[string]ToolInfo{},
		owner:   map[string]string{},
	}
}

// ConnectAll подключается ко всем серверам и строит таблицу маршрутизации.
// Ошибка, если сервер недоступен или инструмент зарегистрирован в двух серверах.
func (r *Registry) ConnectAll(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, spec := range r.specs {
		if _, ok := r.clients[spec.Name]; ok {
			continue
		}
		c, err := Connect(ctx, spec.Command, spec.Args...)
		if err != nil {
			return fmt.Errorf("подключение к серверу %q: %w", spec.Name, err)
		}
		tools, err := c.ListTools(ctx)
		if err != nil {
			_ = c.Close()
			return fmt.Errorf("tools/list от %q: %w", spec.Name, err)
		}
		for _, t := range tools {
			if prev, exists := r.owner[t.Name]; exists {
				_ = c.Close()
				return fmt.Errorf("инструмент %q зарегистрирован в двух серверах: %q и %q", t.Name, prev, spec.Name)
			}
			r.owner[t.Name] = spec.Name
			r.tools[t.Name] = t
		}
		r.clients[spec.Name] = c
	}
	return nil
}

// ListTools возвращает объединённый список инструментов всех серверов.
func (r *Registry) ListTools() []ToolInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ToolInfo, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// Owner возвращает имя сервера, обслуживающего инструмент.
func (r *Registry) Owner(tool string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.owner[tool]
	return s, ok
}

// CallTool маршрутизирует вызов к серверу, владеющему инструментом.
func (r *Registry) CallTool(ctx context.Context, name string, arguments map[string]any) (CallResult, error) {
	r.mu.Lock()
	server, ok := r.owner[name]
	client := r.clients[server]
	r.mu.Unlock()
	if !ok || client == nil {
		return CallResult{}, fmt.Errorf("инструмент %q не найден ни на одном сервере", name)
	}
	out, err := client.CallTool(ctx, name, arguments)
	if err != nil {
		return CallResult{}, fmt.Errorf("%s.%s: %w", server, name, err)
	}
	return CallResult{Server: server, Output: out}, nil
}

// Close закрывает соединения со всеми серверами.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, c := range r.clients {
		_ = c.Close()
		delete(r.clients, name)
	}
}

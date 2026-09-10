package agent

import (
	"aichallenge/llm"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // регистрирует драйвер "sqlite" (чистый Go, без cgo)
)

// Memory — абстракция над хранилищем истории диалога. Ядро Agent работает только
// с этим интерфейсом, поэтому среда исполнения не зависит от конкретной БД.
//
// На Этапе 1 используется простая in-memory реализация (InMemory). На Этапе 2
// подключается SQLiteMemory — интерфейс при этом не меняется.
type Memory interface {
	// Load возвращает всю сохранённую историю в порядке диалога.
	Load() ([]llm.Message, error)
	// Append добавляет одно сообщение в конец истории.
	Append(msg llm.Message) error
	// Reset очищает историю.
	Reset() error
	// Close закрывает хранилище (освобождает ресурсы).
	Close() error
}

// InMemory — хранилище истории в оперативной памяти (Этап 1). Ничего не пишет на диск.
type InMemory struct {
	msgs []llm.Message
}

// NewInMemory создаёт пустое in-memory хранилище.
func NewInMemory() *InMemory { return &InMemory{} }

// Load возвращает историю.
func (m *InMemory) Load() ([]llm.Message, error) { return m.msgs, nil }

// Append добавляет сообщение.
func (m *InMemory) Append(msg llm.Message) error {
	m.msgs = append(m.msgs, msg)
	return nil
}

// Reset очищает историю.
func (m *InMemory) Reset() error {
	m.msgs = nil
	return nil
}

// Close — заглушка, ресурсы не использует.
func (m *InMemory) Close() error { return nil }

// SQLiteMemory — хранилище истории в SQLite (Этап 2). Диалог переживает
// перезапуск: сообщения лежат в таблице messages (role, content, порядок по id).
type SQLiteMemory struct {
	db *sql.DB
}

// NewSQLiteMemory открывает (при необходимости создаёт) БД по пути path и
// гарантирует наличие таблицы messages.
func NewSQLiteMemory(path string) (*SQLiteMemory, error) {
	if path == "" {
		path = "agent_history.db"
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("открыть SQLite %s: %w", path, err)
	}
	// Приём соединение-процесс, обычный для встроенного SQLite.
	db.SetMaxOpenConns(1)

	const ddl = `CREATE TABLE IF NOT EXISTS messages (
		id      INTEGER PRIMARY KEY AUTOINCREMENT,
		role    TEXT NOT NULL,
		content TEXT NOT NULL
	);`
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("создать таблицу messages: %w", err)
	}
	return &SQLiteMemory{db: db}, nil
}

// Load возвращает всю историю в порядке диалога (по возрастанию id).
func (m *SQLiteMemory) Load() ([]llm.Message, error) {
	rows, err := m.db.Query(`SELECT role, content FROM messages ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("загрузить историю: %w", err)
	}
	defer rows.Close()

	var out []llm.Message
	for rows.Next() {
		var msg llm.Message
		if err := rows.Scan(&msg.Role, &msg.Content); err != nil {
			return nil, fmt.Errorf("прочитать сообщение: %w", err)
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}

// Append добавляет одно сообщение в конец истории.
func (m *SQLiteMemory) Append(msg llm.Message) error {
	_, err := m.db.Exec(`INSERT INTO messages (role, content) VALUES (?, ?)`, msg.Role, msg.Content)
	if err != nil {
		return fmt.Errorf("добавить сообщение: %w", err)
	}
	return nil
}

// Reset очищает историю.
func (m *SQLiteMemory) Reset() error {
	_, err := m.db.Exec(`DELETE FROM messages`)
	if err != nil {
		return fmt.Errorf("очистить историю: %w", err)
	}
	return nil
}

// Close закрывает соединение с БД.
func (m *SQLiteMemory) Close() error { return m.db.Close() }

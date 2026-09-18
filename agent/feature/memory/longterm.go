package memory

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // регистрирует драйвер "sqlite" (чистый Go, без cgo)
)

// Типы долговременной памяти: профиль пользователя, принятые решения,
// накопленные знания и устойчивые предпочтения.
const (
	KindProfile    = "profile"
	KindDecision   = "decision"
	KindKnowledge  = "knowledge"
	KindPreference = "preference"
)

// LongEntry — одна запись долговременной памяти.
type LongEntry struct {
	Kind  string `json:"kind"`          // profile | decision | knowledge | preference
	Key   string `json:"key"`           // короткий идентификатор записи
	Value string `json:"value"`         // содержание
	Seq   int64  `json:"seq,omitempty"` // порядковый номер (порядок добавления)
}

// LongStore — долговременный слой памяти: профиль, решения, знания.
//
// Переживает перезапуск приложения и НЕ очищается при ResetSession — в отличие
// от short-term (диалог) и working (текущая задача). Пополняется явно через
// Put/Remember (например, командой /remember) или политикой классификации.
type LongStore interface {
	// Put сохраняет запись.
	Put(e LongEntry) error
	// All возвращает все записи в порядке добавления.
	All() ([]LongEntry, error)
	// ByKind возвращает записи заданного типа.
	ByKind(kind string) ([]LongEntry, error)
	// Reset очищает все записи.
	Reset() error
	// Close закрывает хранилище.
	Close() error
}

// InMemoryLong — долговременная память в RAM (для тестов и лёгких окружений).
type InMemoryLong struct {
	entries []LongEntry
}

// NewInMemoryLong создаёт пустое in-memory хранилище.
func NewInMemoryLong() *InMemoryLong { return &InMemoryLong{} }

// Put добавляет запись в конец.
func (m *InMemoryLong) Put(e LongEntry) error {
	m.entries = append(m.entries, e)
	return nil
}

// All возвращает все записи.
func (m *InMemoryLong) All() ([]LongEntry, error) { return append([]LongEntry{}, m.entries...), nil }

// ByKind фильтрует записи по типу.
func (m *InMemoryLong) ByKind(kind string) ([]LongEntry, error) {
	var out []LongEntry
	for _, e := range m.entries {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out, nil
}

// Reset очищает записи.
func (m *InMemoryLong) Reset() error {
	m.entries = nil
	return nil
}

// Close — заглушка.
func (m *InMemoryLong) Close() error { return nil }

// SQLiteLong — долговременная память в SQLite: переживает перезапуск.
type SQLiteLong struct {
	db *sql.DB
}

// NewSQLiteLong открывает (при необходимости создаёт) БД по пути path и
// гарантирует наличие таблицы longterm (kind, key, value, seq).
func NewSQLiteLong(path string) (*SQLiteLong, error) {
	if path == "" {
		path = "agent_longterm.db"
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("открыть SQLite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	const ddl = `CREATE TABLE IF NOT EXISTS longterm (
		id    INTEGER PRIMARY KEY AUTOINCREMENT,
		kind  TEXT NOT NULL,
		key   TEXT NOT NULL,
		value TEXT NOT NULL,
		seq   INTEGER NOT NULL DEFAULT 0
	);`
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("создать таблицу longterm: %w", err)
	}
	return &SQLiteLong{db: db}, nil
}

// Put добавляет запись с автоинкрементным порядком.
func (m *SQLiteLong) Put(e LongEntry) error {
	if e.Kind == "" {
		e.Kind = KindKnowledge
	}
	_, err := m.db.Exec(`INSERT INTO longterm (kind, key, value, seq) VALUES (?, ?, ?, ?)`,
		e.Kind, e.Key, e.Value, nextSeq(m))
	if err != nil {
		return fmt.Errorf("сохранить запись longterm: %w", err)
	}
	return nil
}

// All возвращает все записи в порядке добавления.
func (m *SQLiteLong) All() ([]LongEntry, error) {
	rows, err := m.db.Query(`SELECT kind, key, value, seq FROM longterm ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("загрузить longterm: %w", err)
	}
	defer rows.Close()

	var out []LongEntry
	for rows.Next() {
		var e LongEntry
		if err := rows.Scan(&e.Kind, &e.Key, &e.Value, &e.Seq); err != nil {
			return nil, fmt.Errorf("прочитать longterm: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ByKind возвращает записи заданного типа в порядке добавления.
func (m *SQLiteLong) ByKind(kind string) ([]LongEntry, error) {
	rows, err := m.db.Query(`SELECT kind, key, value, seq FROM longterm WHERE kind = ? ORDER BY id`, kind)
	if err != nil {
		return nil, fmt.Errorf("загрузить longterm по типу: %w", err)
	}
	defer rows.Close()

	var out []LongEntry
	for rows.Next() {
		var e LongEntry
		if err := rows.Scan(&e.Kind, &e.Key, &e.Value, &e.Seq); err != nil {
			return nil, fmt.Errorf("прочитать longterm: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Reset очищает таблицу.
func (m *SQLiteLong) Reset() error {
	_, err := m.db.Exec(`DELETE FROM longterm`)
	if err != nil {
		return fmt.Errorf("очистить longterm: %w", err)
	}
	return nil
}

// Close закрывает соединение.
func (m *SQLiteLong) Close() error { return m.db.Close() }

// nextSeq вычисляет следующий порядковый номер (максимум seq + 1).
func nextSeq(m *SQLiteLong) int64 {
	var max int64
	_ = m.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM longterm`).Scan(&max)
	return max + 1
}

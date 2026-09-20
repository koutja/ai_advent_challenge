package invariants

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // регистрирует драйвер "sqlite" (чистый Go, без cgo)
)

// Store — хранилище инвариантов. Хранится ОТДЕЛЬНО от диалога: инварианты
// переживают /reset и /newtask (не попадают в short/working слои). SQLite-реализация
// переживает перезапуск.
type Store interface {
	// Get возвращает инвариант по ID (nil, если нет).
	Get(id string) (*Invariant, error)
	// Set сохраняет (создаёт или обновляет) инвариант.
	Set(i Invariant) error
	// List возвращает активные (Enabled) инварианты, отсортированные по ID.
	List() ([]Invariant, error)
	// All возвращает ВСЕ инварианты, включая выключенные, отсортированные по ID.
	All() ([]Invariant, error)
	// Delete удаляет инвариант по ID.
	Delete(id string) error
	// Reset очищает все инварианты.
	Reset() error
	// Close закрывает хранилище.
	Close() error
}

// InMemoryStore — инварианты в оперативной памяти (для тестов и CLI без диска).
type InMemoryStore struct {
	items map[string]Invariant
}

// NewInMemoryStore создаёт пустое in-memory хранилище.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{items: map[string]Invariant{}}
}

// Get возвращает инвариант по ID.
func (s *InMemoryStore) Get(id string) (*Invariant, error) {
	i, ok := s.items[id]
	if !ok {
		return nil, nil
	}
	c := i
	return &c, nil
}

// Set сохраняет инвариант.
func (s *InMemoryStore) Set(i Invariant) error {
	s.items[i.ID] = i
	return nil
}

// List возвращает активные инварианты.
func (s *InMemoryStore) List() ([]Invariant, error) {
	all, _ := s.All()
	out := make([]Invariant, 0, len(all))
	for _, i := range all {
		if i.Enabled {
			out = append(out, i)
		}
	}
	return out, nil
}

// All возвращает все инварианты.
func (s *InMemoryStore) All() ([]Invariant, error) {
	out := make([]Invariant, 0, len(s.items))
	for _, i := range s.items {
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out, nil
}

// Delete удаляет инвариант.
func (s *InMemoryStore) Delete(id string) error {
	delete(s.items, id)
	return nil
}

// Reset очищает хранилище.
func (s *InMemoryStore) Reset() error {
	s.items = map[string]Invariant{}
	return nil
}

// Close — заглушка.
func (s *InMemoryStore) Close() error { return nil }

// SQLiteStore — инварианты в SQLite: переживают перезапуск.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore открывает (при необходимости создаёт) БД и гарантирует таблицу invariants.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		path = "agent_invariants.db"
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("открыть SQLite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	const ddl = `CREATE TABLE IF NOT EXISTS invariants (
		id       TEXT PRIMARY KEY,
		title    TEXT NOT NULL DEFAULT '',
		category TEXT NOT NULL DEFAULT '',
		text     TEXT NOT NULL DEFAULT '',
		keywords TEXT NOT NULL DEFAULT '',
		severity TEXT NOT NULL DEFAULT 'hard',
		enabled  INTEGER NOT NULL DEFAULT 1
	);`
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("создать таблицу invariants: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// Get возвращает инвариант по ID.
func (s *SQLiteStore) Get(id string) (*Invariant, error) {
	row := s.db.QueryRow(`SELECT id,title,category,text,keywords,severity,enabled FROM invariants WHERE id = ?`, id)
	var i Invariant
	var keywords string
	var enabled int
	err := row.Scan(&i.ID, &i.Title, &i.Category, &i.Text, &keywords, &i.Severity, &enabled)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("прочитать инвариант: %w", err)
	}
	i.Keywords = splitKeywords(keywords)
	i.Enabled = enabled != 0
	return &i, nil
}

// Set сохраняет (создаёт или обновляет) инвариант.
func (s *SQLiteStore) Set(i Invariant) error {
	if i.ID == "" {
		return fmt.Errorf("инвариант без ID")
	}
	enabled := 0
	if i.Enabled {
		enabled = 1
	}
	_, err := s.db.Exec(`INSERT INTO invariants (id,title,category,text,keywords,severity,enabled)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			title=excluded.title, category=excluded.category, text=excluded.text,
			keywords=excluded.keywords, severity=excluded.severity, enabled=excluded.enabled`,
		i.ID, i.Title, i.Category, i.Text, joinKeywords(i.Keywords), i.Severity, enabled)
	if err != nil {
		return fmt.Errorf("сохранить инвариант: %w", err)
	}
	return nil
}

// scanInvariant сканирует одну строку результата запроса в Invariant.
func scanInvariant(rows interface{ Scan(...any) error }) (*Invariant, error) {
	var i Invariant
	var keywords string
	var enabled int
	if err := rows.Scan(&i.ID, &i.Title, &i.Category, &i.Text, &keywords, &i.Severity, &enabled); err != nil {
		return nil, fmt.Errorf("прочитать инвариант: %w", err)
	}
	i.Keywords = splitKeywords(keywords)
	i.Enabled = enabled != 0
	return &i, nil
}

// List возвращает активные инварианты, отсортированные по ID.
func (s *SQLiteStore) List() ([]Invariant, error) {
	rows, err := s.db.Query(`SELECT id,title,category,text,keywords,severity,enabled FROM invariants WHERE enabled=1 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("загрузить инварианты: %w", err)
	}
	defer rows.Close()

	var out []Invariant
	for rows.Next() {
		i, err := scanInvariant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

// All возвращает все инварианты, отсортированные по ID.
func (s *SQLiteStore) All() ([]Invariant, error) {
	rows, err := s.db.Query(`SELECT id,title,category,text,keywords,severity,enabled FROM invariants ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("загрузить инварианты: %w", err)
	}
	defer rows.Close()

	var out []Invariant
	for rows.Next() {
		i, err := scanInvariant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

// Delete удаляет инвариант по ID.
func (s *SQLiteStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM invariants WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("удалить инвариант: %w", err)
	}
	return nil
}

// Reset очищает все инварианты.
func (s *SQLiteStore) Reset() error {
	_, err := s.db.Exec(`DELETE FROM invariants`)
	if err != nil {
		return fmt.Errorf("очистить инварианты: %w", err)
	}
	return nil
}

// Close закрывает соединение.
func (s *SQLiteStore) Close() error { return s.db.Close() }

// joinKeywords объединяет ключевые слова строкой с разделителем.
func joinKeywords(k []string) string { return strings.Join(k, "\n") }

// splitKeywords разбивает строку ключевых слов обратно в список.
func splitKeywords(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

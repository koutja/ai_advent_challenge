package profile

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // регистрирует драйвер "sqlite" (чистый Go, без cgo)
)

// Store — хранилище профилей пользователя. Переживает перезапуск в SQLite-реализации.
type Store interface {
	// Get возвращает профиль по ID (nil, если нет).
	Get(id string) (*Profile, error)
	// Set сохраняет (создаёт или обновляет) профиль.
	Set(p Profile) error
	// List возвращает все профили, отсортированные по ID.
	List() ([]Profile, error)
	// Delete удаляет профиль по ID.
	Delete(id string) error
	// Reset очищает все профили.
	Reset() error
	// Close закрывает хранилище.
	Close() error
}

// InMemoryStore — профили в оперативной памяти (для тестов и CLI без диска).
type InMemoryStore struct {
	profiles map[string]Profile
}

// NewInMemoryStore создаёт пустое in-memory хранилище.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{profiles: map[string]Profile{}}
}

// Get возвращает профиль по ID.
func (s *InMemoryStore) Get(id string) (*Profile, error) {
	p, ok := s.profiles[id]
	if !ok {
		return nil, nil
	}
	c := p
	return &c, nil
}

// Set сохраняет профиль.
func (s *InMemoryStore) Set(p Profile) error {
	s.profiles[p.ID] = p
	return nil
}

// List возвращает все профили, отсортированные по ID.
func (s *InMemoryStore) List() ([]Profile, error) {
	ids := make([]string, 0, len(s.profiles))
	for k := range s.profiles {
		ids = append(ids, k)
	}
	sortStrings(ids)
	out := make([]Profile, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.profiles[id])
	}
	return out, nil
}

// Delete удаляет профиль.
func (s *InMemoryStore) Delete(id string) error {
	delete(s.profiles, id)
	return nil
}

// Reset очищает хранилище.
func (s *InMemoryStore) Reset() error {
	s.profiles = map[string]Profile{}
	return nil
}

// Close — заглушка.
func (s *InMemoryStore) Close() error { return nil }

// SQLiteStore — профили в SQLite: переживают перезапуск.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore открывает (при необходимости создаёт) БД и гарантирует таблицу profiles.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		path = "agent_profiles.db"
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("открыть SQLite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	const ddl = `CREATE TABLE IF NOT EXISTS profiles (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL DEFAULT '',
		role        TEXT NOT NULL DEFAULT '',
		language    TEXT NOT NULL DEFAULT '',
		style       TEXT NOT NULL DEFAULT '',
		format      TEXT NOT NULL DEFAULT '',
		constraints TEXT NOT NULL DEFAULT '',
		expertise   TEXT NOT NULL DEFAULT ''
	);`
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("создать таблицу profiles: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// Get возвращает профиль по ID.
func (s *SQLiteStore) Get(id string) (*Profile, error) {
	row := s.db.QueryRow(`SELECT id,name,role,language,style,format,constraints,expertise FROM profiles WHERE id = ?`, id)
	var p Profile
	var constraints, expertise string
	err := row.Scan(&p.ID, &p.Name, &p.Role, &p.Language, &p.Style, &p.Format, &constraints, &expertise)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("прочитать профиль: %w", err)
	}
	p.Constraints = splitList(constraints)
	p.Expertise = splitList(expertise)
	return &p, nil
}

// Set сохраняет (создаёт или обновляет) профиль.
func (s *SQLiteStore) Set(p Profile) error {
	if p.ID == "" {
		return fmt.Errorf("профиль без ID")
	}
	_, err := s.db.Exec(`INSERT INTO profiles (id,name,role,language,style,format,constraints,expertise)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, role=excluded.role, language=excluded.language,
			style=excluded.style, format=excluded.format,
			constraints=excluded.constraints, expertise=excluded.expertise`,
		p.ID, p.Name, p.Role, p.Language, p.Style, p.Format,
		joinList(p.Constraints), joinList(p.Expertise))
	if err != nil {
		return fmt.Errorf("сохранить профиль: %w", err)
	}
	return nil
}

// List возвращает все профили, отсортированные по ID.
func (s *SQLiteStore) List() ([]Profile, error) {
	rows, err := s.db.Query(`SELECT id,name,role,language,style,format,constraints,expertise FROM profiles ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("загрузить профили: %w", err)
	}
	defer rows.Close()

	var out []Profile
	for rows.Next() {
		var p Profile
		var constraints, expertise string
		if err := rows.Scan(&p.ID, &p.Name, &p.Role, &p.Language, &p.Style, &p.Format, &constraints, &expertise); err != nil {
			return nil, fmt.Errorf("прочитать профиль: %w", err)
		}
		p.Constraints = splitList(constraints)
		p.Expertise = splitList(expertise)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Delete удаляет профиль по ID.
func (s *SQLiteStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM profiles WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("удалить профиль: %w", err)
	}
	return nil
}

// Reset очищает все профили.
func (s *SQLiteStore) Reset() error {
	_, err := s.db.Exec(`DELETE FROM profiles`)
	if err != nil {
		return fmt.Errorf("очистить профили: %w", err)
	}
	return nil
}

// Close закрывает соединение.
func (s *SQLiteStore) Close() error { return s.db.Close() }

func joinList(items []string) string { return strings.Join(items, "\n") }
func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
func sortStrings(s []string) { sort.Strings(s) }

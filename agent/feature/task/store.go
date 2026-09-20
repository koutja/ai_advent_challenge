package task

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite" // регистрирует драйвер "sqlite" (чистый Go, без cgo)
)

// Store — хранилище состояния одной активной задачи.
//
// Для тестов и лёгких окружений есть InMemoryStore; для переживающего перезапуск
// хранения (пауза/шаг) — SQLiteStore. Интерфейс у обоих одинаковый.
type Store interface {
	// Save сохраняет текущее состояние задачи.
	Save(st State) error
	// Load возвращает сохранённое состояние (пустое, если задачи нет).
	Load() (State, error)
	// Clear очищает состояние задачи.
	Clear() error
	// Close закрывает хранилище (освобождает ресурсы).
	Close() error
}

// InMemoryStore — хранилище состояния в оперативной памяти. Ничего не пишет на диск.
type InMemoryStore struct {
	st State
}

// NewInMemoryStore создаёт пустое in-memory хранилище.
func NewInMemoryStore() *InMemoryStore { return &InMemoryStore{} }

// Save сохраняет состояние.
func (s *InMemoryStore) Save(st State) error {
	// Копируем слайс, чтобы внешние мутации не влияли на сохранённое.
	st.Log = append([]string(nil), st.Log...)
	s.st = st
	return nil
}

// Load возвращает сохранённое состояние.
func (s *InMemoryStore) Load() (State, error) {
	st := s.st
	st.Log = append([]string(nil), st.Log...)
	return st, nil
}

// Clear очищает состояние.
func (s *InMemoryStore) Clear() error {
	s.st = State{}
	return nil
}

// Close — заглушка, ресурсы не использует.
func (s *InMemoryStore) Close() error { return nil }

// SQLiteStore — хранилище состояния задачи в SQLite. Состояние (включая паузу,
// шаг и ожидаемое действие) переживает перезапуск процесса: хранится в таблице
// task_state одной строкой; поле log сериализуется в JSON.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore открывает (при необходимости создаёт) БД по пути path и
// гарантирует наличие таблицы task_state.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		path = "agent_task.db"
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("открыть SQLite %s: %w", path, err)
	}
	// Приём соединение-процесс, обычный для встроенного SQLite.
	db.SetMaxOpenConns(1)

	const ddl = `CREATE TABLE IF NOT EXISTS task_state (
		id            INTEGER PRIMARY KEY CHECK (id = 1),
		goal          TEXT NOT NULL DEFAULT '',
		stage         TEXT NOT NULL DEFAULT '',
		step          INTEGER NOT NULL DEFAULT 0,
		expected      TEXT NOT NULL DEFAULT '',
		paused        INTEGER NOT NULL DEFAULT 0,
		plan_approved INTEGER NOT NULL DEFAULT 0,
		validated     INTEGER NOT NULL DEFAULT 0,
		plan          TEXT NOT NULL DEFAULT '',
		log           TEXT NOT NULL DEFAULT '[]'
	);`
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("создать таблицу task_state: %w", err)
	}
	// Миграция существующих БД (старые таблицы без новых колонок).
	migrate := map[string]string{
		"plan_approved": "INTEGER NOT NULL DEFAULT 0",
		"validated":     "INTEGER NOT NULL DEFAULT 0",
		"plan":          "TEXT NOT NULL DEFAULT ''",
	}
	for col, typ := range migrate {
		if _, err := db.Exec("ALTER TABLE task_state ADD COLUMN " + col + " " + typ); err != nil {
			// колонка уже есть — игнорируем "duplicate column name".
		}
	}
	return &SQLiteStore{db: db}, nil
}

// Save сохраняет состояние задачи одной строкой (id = 1).
func (s *SQLiteStore) Save(st State) error {
	logJSON, err := json.Marshal(st.Log)
	if err != nil {
		return fmt.Errorf("сериализовать лог: %w", err)
	}
	paused := 0
	if st.Paused {
		paused = 1
	}
	planApproved := 0
	if st.PlanApproved {
		planApproved = 1
	}
	validated := 0
	if st.Validated {
		validated = 1
	}
	_, err = s.db.Exec(`INSERT INTO task_state (id, goal, stage, step, expected, paused, plan_approved, validated, plan, log)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			goal = excluded.goal, stage = excluded.stage, step = excluded.step,
			expected = excluded.expected, paused = excluded.paused,
			plan_approved = excluded.plan_approved, validated = excluded.validated,
			plan = excluded.plan, log = excluded.log`,
		st.Goal, st.Stage, st.Step, st.Expected, paused, planApproved, validated, st.Plan, string(logJSON))
	if err != nil {
		return fmt.Errorf("сохранить состояние задачи: %w", err)
	}
	return nil
}

// Load возвращает сохранённое состояние (пустое, если задачи ещё нет).
func (s *SQLiteStore) Load() (State, error) {
	var st State
	var paused, planApproved, validated int
	var logJSON string
	err := s.db.QueryRow(`SELECT goal, stage, step, expected, paused, plan_approved, validated, plan, log FROM task_state WHERE id = 1`).
		Scan(&st.Goal, &st.Stage, &st.Step, &st.Expected, &paused, &planApproved, &validated, &st.Plan, &logJSON)
	if err == sql.ErrNoRows {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("загрузить состояние задачи: %w", err)
	}
	st.Paused = paused != 0
	st.PlanApproved = planApproved != 0
	st.Validated = validated != 0
	if err := json.Unmarshal([]byte(logJSON), &st.Log); err != nil {
		return State{}, fmt.Errorf("разобрать лог состояния: %w", err)
	}
	return st, nil
}

// Clear удаляет строку состояния задачи.
func (s *SQLiteStore) Clear() error {
	_, err := s.db.Exec(`DELETE FROM task_state WHERE id = 1`)
	if err != nil {
		return fmt.Errorf("очистить состояние задачи: %w", err)
	}
	return nil
}

// Close закрывает соединение с БД.
func (s *SQLiteStore) Close() error { return s.db.Close() }

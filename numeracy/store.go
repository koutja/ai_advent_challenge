package main

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed seeds/tasks.json
var seedsJSON []byte

// defaultDBFile — SQLite-база задач (results/ в .gitignore).
const defaultDBFile = "results/numeracy.db"

// Store — SQLite-хранилище задач тренажёра. При первом запуске (пустая
// таблица) загружаются базовые задачи из seeds/tasks.json (ДАННЫЕ, не код).
// Сгенерированные AI задачи сохраняются в ту же базу и имеют приоритет при
// выдаче.
type Store struct {
	db   *sql.DB
	path string
}

// OpenStore открывает (или создаёт) базу и заполняет seed при необходимости.
func OpenStore(path string) (*Store, error) {
	if path == "" {
		path = defaultDBFile
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db, path: path}
	if err := s.ensureSeed(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Path возвращает путь к файлу базы.
func (s *Store) Path() string { return s.path }

// Close закрывает базу.
func (s *Store) Close() error { return s.db.Close() }

const schema = `CREATE TABLE IF NOT EXISTS tasks (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	category    TEXT NOT NULL,
	level       TEXT NOT NULL,
	kind        TEXT NOT NULL,
	unit        TEXT NOT NULL DEFAULT '',
	expected    REAL NOT NULL DEFAULT 0,
	tolerance   REAL NOT NULL DEFAULT 1,
	question    TEXT NOT NULL,
	options     TEXT NOT NULL DEFAULT '[]',
	correct_idx INTEGER NOT NULL DEFAULT 0,
	explain     TEXT NOT NULL DEFAULT '',
	source      TEXT NOT NULL DEFAULT 'generated',
	created_at  TEXT NOT NULL
);`

// ensureSeed — если таблица пуста, загружаем базовые задачи (source=seed).
func (s *Store) ensureSeed() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	// Промежуточная структура: в seed kind — строка ("numeric"/"choice").
	var seeds []struct {
		Category   string   `json:"category"`
		Level      string   `json:"level"`
		Kind       string   `json:"kind"`
		Unit       string   `json:"unit"`
		Expected   float64  `json:"expected"`
		Tolerance  float64  `json:"tolerance"`
		Question   string   `json:"question"`
		Options    []string `json:"options"`
		CorrectIdx int      `json:"correct_idx"`
		Explain    string   `json:"explanation"`
	}
	if err := json.Unmarshal(seedsJSON, &seeds); err != nil {
		return fmt.Errorf("seeds/tasks.json: %w", err)
	}
	for _, seed := range seeds {
		kind, _ := kindFromString(seed.Kind)
		t := &Task{
			Category:   Category(seed.Category),
			Level:      Level(seed.Level),
			Kind:       kind,
			Unit:       Unit(seed.Unit),
			Expected:   seed.Expected,
			Tolerance:  seed.Tolerance,
			Question:   seed.Question,
			Options:    seed.Options,
			CorrectIdx: seed.CorrectIdx,
			Explain:    seed.Explain,
			Source:     SourceSeed,
		}
		if _, err := s.Save(t); err != nil {
			return err
		}
	}
	return nil
}

// Save сохраняет задачу в базу и возвращает её id. source — происхождение
// (seed|generated).
func (s *Store) Save(t *Task) (int64, error) {
	if err := validateTask(t); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`INSERT INTO tasks
		(category, level, kind, unit, expected, tolerance, question, options,
		 correct_idx, explain, source, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.Category, t.Level, kindString(t.Kind), t.Unit, t.Expected, t.Tolerance,
		t.Question, optionsJSON(t.Options), t.CorrectIdx, t.Explain, t.Source,
		time.Now().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	t.ID = int(id)
	return id, nil
}

// Task возвращает СЛУЧАЙНУЮ задачу категории/уровня из базы: предпочитает ранее
// сгенерированные AI (source=generated), иначе базовые (source=seed), чтобы
// лист не состоял из одинаковых задач. level "all"/"" — любой уровень.
func (s *Store) Task(cat Category, lvl Level) (*Task, error) {
	row := s.db.QueryRow(`SELECT id, category, level, kind, unit, expected, tolerance,
		question, options, correct_idx, explain, source FROM tasks
		WHERE category = ? AND (level = ? OR ? = 'all' OR ? = '')
		ORDER BY CASE source WHEN 'generated' THEN 1 ELSE 0 END DESC, RANDOM() LIMIT 1`,
		cat, lvl, lvl, lvl)
	return scanTask(row)
}

// rowScanner — минимальный интерфейс Scan (row/rows).
type rowScanner interface{ Scan(dest ...any) error }

func scanTask(row rowScanner) (*Task, error) {
	var (
		t        Task
		kindStr  string
		src      string
		optsJSON string
	)
	err := row.Scan(&t.ID, &t.Category, &t.Level, &kindStr, &t.Unit, &t.Expected,
		&t.Tolerance, &t.Question, &optsJSON, &t.CorrectIdx, &t.Explain, &src)
	if err != nil {
		return nil, err
	}
	t.Kind, _ = kindFromString(kindStr)
	t.Options = jsonOptions(optsJSON)
	t.Source = Source(src)
	return &t, nil
}

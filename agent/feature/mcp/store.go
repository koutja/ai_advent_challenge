package mcpx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Максимумы, чтобы планировщик не жег ресурсы даже после долгого простоя.
const (
	maxPerTick    = 50            // максимум срабатываний за один тик
	maxSummaries  = 20            // сколько последних снимков summary хранить
	rfc3339       = time.RFC3339
	defaultDataFile = "results/mcp_scheduler_data.json" // игнорируется git (**/results/)
)

// Reminder — отложенное напоминание.
type Reminder struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	DueAt   string `json:"due_at"`
	Fired   bool   `json:"fired"`
	FiredAt string `json:"fired_at,omitempty"`
}

// Job — периодическая задача планировщика.
//
//	Kind: "collect" (сбор точек данных) | "summary" (периодический снимок сводки)
//	Iterations == 0 — бесконечный цикл (до перезапуска сервера).
type Job struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Metric      string    `json:"metric,omitempty"`
	IntervalSec int       `json:"interval_seconds"`
	Iterations  int       `json:"iterations"`
	Done        int       `json:"done"`
	NextRunAt   string    `json:"next_run_at"`
	StartedAt   string    `json:"started_at"`
	Points      []float64 `json:"points,omitempty"`
}

// ReminderStats — агрегированная сводка по напоминаниям.
type ReminderStats struct {
	Total   int `json:"total"`
	Pending int `json:"pending"`
	Fired   int `json:"fired"`
}

// MetricStats — агрегат по точкам данных одной метрики.
type MetricStats struct {
	Count  int     `json:"count"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Avg    float64 `json:"avg"`
	Latest float64 `json:"latest"`
}

// SummarySnapshot — снимок сводки в момент времени.
type SummarySnapshot struct {
	At        string                `json:"at"`
	Reminders ReminderStats         `json:"reminders"`
	Metrics   map[string]MetricStats `json:"metrics"`
}

// Data — всё состояние планировщика, сериализуемое в JSON-файл.
type Data struct {
	Reminders []Reminder         `json:"reminders"`
	Jobs      []*Job             `json:"jobs"`
	Summaries []SummarySnapshot  `json:"summaries"`
	Seq       int                `json:"seq"`
}

// Store — потокобезопасное JSON-хранилище планировщика.
// Атомарная запись: tmp-файл + rename. Пустой path — чисто in-memory режим.
type Store struct {
	path string
	mu   sync.Mutex
	data *Data
}

// OpenStore открывает (или создаёт) хранилище по пути. При path == ""
// работает только в памяти (без сохранения на диск).
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, data: &Data{Jobs: []*Job{}, Summaries: []SummarySnapshot{}}}
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
	default:
		return nil, err
	}
	if s.data.Jobs == nil {
		s.data.Jobs = []*Job{}
	}
	if s.data.Summaries == nil {
		s.data.Summaries = []SummarySnapshot{}
	}
	return s, nil
}

// Path возвращает путь к файлу хранилища ("" — in-memory).
func (s *Store) Path() string { return s.path }

func (s *Store) saveLocked() error {
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

// AddReminder сохраняет напоминание и возвращает его запись.
func (s *Store) AddReminder(text string, due time.Time) (Reminder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Seq++
	r := Reminder{
		ID:    fmt.Sprintf("R-%03d", s.data.Seq),
		Text:  text,
		DueAt: due.Format(rfc3339),
	}
	s.data.Reminders = append(s.data.Reminders, r)
	return r, s.saveLocked()
}

// FireDue помечает сработавшие напоминания (due <= now). Возвращает число
// сработавших. Это и есть catch-up: после рестарта просроченные выполняются сразу.
func (s *Store) FireDue(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for i := range s.data.Reminders {
		r := &s.data.Reminders[i]
		if r.Fired {
			continue
		}
		due, err := time.Parse(rfc3339, r.DueAt)
		if err != nil {
			continue
		}
		if !due.After(now) {
			r.Fired = true
			r.FiredAt = now.Format(rfc3339)
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, s.saveLocked()
}

// ReminderStats возвращает агрегированную сводку по напоминаниям.
func (s *Store) ReminderStats() ReminderStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st ReminderStats
	st.Total = len(s.data.Reminders)
	for _, r := range s.data.Reminders {
		if r.Fired {
			st.Fired++
		} else {
			st.Pending++
		}
	}
	return st
}

// AddJob регистрирует периодическую задачу. Первое срабатывание — сразу
// (NextRunAt = now), дальше через IntervalSec.
func (s *Store) AddJob(kind, metric string, intervalSec, iterations int, now time.Time) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Seq++
	j := &Job{
		ID:          fmt.Sprintf("J-%03d", s.data.Seq),
		Kind:        kind,
		Metric:      metric,
		IntervalSec: intervalSec,
		Iterations:  iterations,
		NextRunAt:   now.Format(rfc3339),
		StartedAt:   now.Format(rfc3339),
	}
	s.data.Jobs = append(s.data.Jobs, j)
	return j, s.saveLocked()
}

// TickJobs выполняет задачи, чей next_run_at <= now. За один вызов — не более
// maxPerTick срабатываний, чтобы планировщик не «догонял» простой бесконечно.
func (s *Store) TickJobs(now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed, processed := false, 0
	for _, j := range s.data.Jobs {
		if j.Kind != "collect" && j.Kind != "summary" {
			continue
		}
		for processed < maxPerTick {
			if j.Iterations > 0 && j.Done >= j.Iterations {
				break
			}
			next, err := time.Parse(rfc3339, j.NextRunAt)
			if err != nil || next.After(now) {
				break
			}
			switch j.Kind {
			case "collect":
				j.Points = append(j.Points, collectValue(j.Metric, j.Done+1))
			case "summary":
				s.data.Summaries = append(s.data.Summaries, buildSummaryLocked(s.data, now))
				if len(s.data.Summaries) > maxSummaries {
					s.data.Summaries = s.data.Summaries[len(s.data.Summaries)-maxSummaries:]
				}
			}
			j.Done++
			processed++
			changed = true
			j.NextRunAt = next.Add(time.Duration(j.IntervalSec) * time.Second).Format(rfc3339)
		}
	}
	if !changed {
		return false, nil
	}
	return true, s.saveLocked()
}

// Aggregate строит текущую сводку (без добавления в историю).
func (s *Store) Aggregate(now time.Time) SummarySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return buildSummaryLocked(s.data, now)
}

// Summaries возвращает последние снимки summary (историю).
func (s *Store) Summaries() []SummarySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SummarySnapshot, len(s.data.Summaries))
	copy(out, s.data.Summaries)
	return out
}

// Jobs возвращает копии всех задач (для отчётов).
func (s *Store) Jobs() []*Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Job, len(s.data.Jobs))
	for i, j := range s.data.Jobs {
		c := *j
		c.Points = append([]float64(nil), j.Points...)
		out[i] = &c
	}
	return out
}

func buildSummaryLocked(d *Data, now time.Time) SummarySnapshot {
	var rs ReminderStats
	rs.Total = len(d.Reminders)
	for _, r := range d.Reminders {
		if r.Fired {
			rs.Fired++
		} else {
			rs.Pending++
		}
	}
	metrics := make(map[string]MetricStats)
	for _, j := range d.Jobs {
		if j.Kind != "collect" || len(j.Points) == 0 {
			continue
		}
		metrics[j.Metric] = statsFromPoints(j.Points)
	}
	return SummarySnapshot{
		At:        now.Format(rfc3339),
		Reminders: rs,
		Metrics:   metrics,
	}
}

func statsFromPoints(points []float64) MetricStats {
	if len(points) == 0 {
		return MetricStats{}
	}
	ms := MetricStats{Count: len(points), Min: points[0], Max: points[0], Latest: points[len(points)-1]}
	var sum float64
	for _, v := range points {
		sum += v
		if v < ms.Min {
			ms.Min = v
		}
		if v > ms.Max {
			ms.Max = v
		}
	}
	ms.Avg = sum / float64(len(points))
	return ms
}

// collectValue генерирует детерминированную «точку данных» для метрики —
// от 20 до 99. Детерминизм нужен для тестов и повторяемости демо.
func collectValue(metric string, iter int) float64 {
	h := 0
	for _, r := range metric {
		h = (h*31 + int(r)) % 9973
	}
	return float64((h+iter*13)%80) + 20
}
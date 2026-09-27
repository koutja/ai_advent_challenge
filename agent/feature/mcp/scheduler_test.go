package mcpx

import (
	"path/filepath"
	"testing"
	"time"
)

func tmpStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStore(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return st
}

// TestStoreRoundTrip: JSON-персистентность — данные переживают перезапуск хранилища.
func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")

	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := st.AddReminder("напомнить про встречу", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddJob("collect", "cpu", 1, 3, now); err != nil {
		t.Fatal(err)
	}

	st2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if rs := st2.ReminderStats(); rs.Total != 1 {
		t.Fatalf("ожидали 1 напоминание, получили %d", rs.Total)
	}
	jobs := st2.Jobs()
	if len(jobs) != 1 || jobs[0].Metric != "cpu" || jobs[0].Iterations != 3 {
		t.Fatalf("задача сохранилась некорректно: %+v", jobs)
	}
}

// TestCatchUp: просроченное напоминание срабатывает при старте планировщика
// («24/7» переживает рестарты).
func TestCatchUp(t *testing.T) {
	st := tmpStore(t)
	now := time.Now()
	if _, err := st.AddReminder("просрочено", now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddReminder("в будущем", now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	sc := NewSchedulerTuned(st, 10*time.Millisecond, func() time.Time { return now })
	sc.Start()
	defer sc.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for {
		rs := st.ReminderStats()
		if rs.Fired == 1 && rs.Pending == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("catch-up не сработал: %+v", rs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestCollectAggregation: периодическая задача за один тик «догоняет» все
// наступившие интервалы (в пределах лимита) и агрегирует точки.
func TestCollectAggregation(t *testing.T) {
	st := tmpStore(t)
	now := time.Now()
	j, err := st.AddJob("collect", "requests", 1, 5, now)
	if err != nil {
		t.Fatal(err)
	}

	// Прошло 5 интервалов — все должны собраться за один вызов.
	if changed, err := st.TickJobs(now.Add(5 * time.Second)); err != nil || !changed {
		t.Fatalf("TickJobs: changed=%v err=%v", changed, err)
	}

	points := j.Points
	if len(points) != 5 {
		t.Fatalf("ожидали 5 точек, получили %d: %v", len(points), points)
	}
	agg := st.Aggregate(now.Add(5 * time.Second))
	m, ok := agg.Metrics["requests"]
	if !ok || m.Count != 5 {
		t.Fatalf("агрегат по requests: %+v", agg.Metrics)
	}
	if m.Min > m.Max || m.Latest != points[len(points)-1] {
		t.Fatalf("странная агрегация: %+v (точки %v)", m, points)
	}
}

// TestCollectBounded: лимит maxPerTick ограничивает «догон» после долгого простоя.
func TestCollectBounded(t *testing.T) {
	st := tmpStore(t)
	now := time.Now()
	if _, err := st.AddJob("collect", "cpu", 1, 0, now); err != nil {
		t.Fatal(err)
	}
	// Бесконечная задача, простой 1000 интервалов — за один тик соберётся не более maxPerTick.
	if _, err := st.TickJobs(now.Add(1000 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, j := range st.Jobs() {
		if j.Done > maxPerTick {
			t.Fatalf("за один тик собрано %d точек (лимит %d)", j.Done, maxPerTick)
		}
	}
}

// TestCollectDeterministic: значения детерминированы и лежат в [20, 99].
func TestCollectDeterministic(t *testing.T) {
	a, b := collectValue("cpu", 1), collectValue("cpu", 1)
	if a != b {
		t.Fatalf("значения должны быть детерминированы: %v != %v", a, b)
	}
	if v := collectValue("cpu", 1); v < 20 || v > 99 {
		t.Fatalf("значение вне диапазона [20,99]: %v", v)
	}
}

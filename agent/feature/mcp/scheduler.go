package mcpx

import (
	"sync"
	"time"
)

// Scheduler — лёгкий фоновый планировщик внутри процесса MCP-сервера.
//
// Каждый тик (по умолчанию 1 с) обрабатывает:
//   - сработавшие напоминания (FireDue, включая catch-up после рестарта);
//   - периодические задачи (TickJobs: сбор точек данных / снимки summary).
//
// Ресурсы: один тикер и одна горутина на процесс; лимит maxPerTick срабатываний
// за тик; останавливается по Stop() (или завершается вместе с процессом сервера).
type Scheduler struct {
	store *Store

	stop chan struct{}
	done chan struct{}

	tick time.Duration
	now  func() time.Time

	mu   sync.Mutex
	once bool // защита от двойного Start
}

// NewScheduler создаёт планировщик с тиком 1 с и временем time.Now.
func NewScheduler(store *Store) *Scheduler {
	return &Scheduler{
		store: store,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
		tick:  time.Second,
		now:   time.Now,
	}
}

// NewSchedulerTuned создаёт планировщик с переопределёнными тиком и часами
// (для тестов, чтобы не ждать реальные секунды).
func NewSchedulerTuned(store *Store, tick time.Duration, now func() time.Time) *Scheduler {
	sc := NewScheduler(store)
	sc.tick = tick
	if now != nil {
		sc.now = now
	}
	return sc
}

// Start запускает цикл планировщика в фоне. При старте сразу выполняется
// catch-up: просроченные напоминания и задачи «догоняются».
func (sc *Scheduler) Start() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.once {
		return
	}
	sc.once = true
	go sc.loop()
}

// Stop останавливает цикл и дожидается выхода горутины.
func (sc *Scheduler) Stop() {
	select {
	case <-sc.stop:
		return
	default:
	}
	close(sc.stop)
	<-sc.done
}

func (sc *Scheduler) loop() {
	defer close(sc.done)

	sc.runOnce(sc.now()) // catch-up при старте

	t := time.NewTicker(sc.tick)
	defer t.Stop()
	for {
		select {
		case <-sc.stop:
			return
		case <-t.C:
			sc.runOnce(sc.now())
		}
	}
}

// runOnce выполняет один «такт» планировщика. Ошибки сохранения не роняют
// процесс: данные остаются в памяти, следующая запись попробует снова.
func (sc *Scheduler) runOnce(now time.Time) {
	_, _ = sc.store.FireDue(now)
	_, _ = sc.store.TickJobs(now)
}

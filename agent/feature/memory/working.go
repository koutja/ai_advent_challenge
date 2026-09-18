package memory

// WorkingStore — рабочий слой памяти: данные ТЕКУЩЕЙ задачи.
//
// Хранит пары «ключ: значение» (цель, ограничение, дедлайн, решения-в-процессе),
// релевантные активной задаче. Очищается при старте новой задачи/сессии
// (ResetSession) и отличается от short-term (история диалога) и long-term
// (профиль/знания, которые живут дольше задачи).
type WorkingStore interface {
	// Set добавляет или обновляет ключ.
	Set(key, value string)
	// Get возвращает значение по ключу.
	Get(key string) (string, bool)
	// All возвращает копию всей рабочей памяти.
	All() map[string]string
	// Count возвращает число хранимых фактов.
	Count() int
	// Clear очищает рабочую память (новая задача).
	Clear()
}

// InMemoryWorking — рабочая память в оперативной памяти (ключ → значение).
type InMemoryWorking struct {
	m map[string]string
}

// NewInMemoryWorking создаёт пустую рабочую память.
func NewInMemoryWorking() *InMemoryWorking {
	return &InMemoryWorking{m: map[string]string{}}
}

// Set добавляет или обновляет ключ.
func (w *InMemoryWorking) Set(key, value string) { w.m[key] = value }

// Get возвращает значение по ключу.
func (w *InMemoryWorking) Get(key string) (string, bool) {
	v, ok := w.m[key]
	return v, ok
}

// All возвращает копию карты.
func (w *InMemoryWorking) All() map[string]string {
	out := make(map[string]string, len(w.m))
	for k, v := range w.m {
		out[k] = v
	}
	return out
}

// Count возвращает число фактов.
func (w *InMemoryWorking) Count() int { return len(w.m) }

// Clear очищает рабочую память.
func (w *InMemoryWorking) Clear() { w.m = map[string]string{} }

package context

import (
	"fmt"

	"aichallenge/llm"

	"agent/feature/dialog"
)

// WorkingView — минимальное представление рабочей памяти (current task data),
// которое стратегия «facts» использует для логирования и снапшота. Реализуется
// feature/memory.WorkingStore, но здесь задано маленьким интерфейсом, чтобы пакет
// context не зависел от feature/memory (избегаем циклов зависимостей).
type WorkingView interface {
	All() map[string]string
}

// FactsMemory — стратегия контекста «недавние сообщения + рабочий контекст».
//
// В исходном виде (Этап стратегий) FactsMemory сам извлекал и хранил ключевые
// факты диалога. После введения многослойной памяти (feature/memory) извлечение
// фактов и их хранение — задача рабочего слоя (WorkingStore), которым управляет
// агент. Здесь остаётся диалоговое окно из FactsKeepLast сообщений плюс прозрачный
// лог о том, что лежит в рабочей памяти текущего хода.
type FactsMemory struct {
	keepLast int           // сколько последних сообщений слать
	work     WorkingView   // рабочая память (nil — недоступна)
	sink     EventSinkFunc // приёмник событий (nil — события не шлются)
}

// NewFactsMemory создаёт стратегию facts с окном keepLast сообщений.
func NewFactsMemory(keepLast int) *FactsMemory {
	if keepLast <= 0 {
		keepLast = 10
	}
	return &FactsMemory{keepLast: keepLast}
}

// SetWorking подключает представление рабочей памяти (вызывает агент).
func (f *FactsMemory) SetWorking(v WorkingView) { f.work = v }

// Name возвращает имя стратегии.
func (f *FactsMemory) Name() string { return "facts" }

// SetEventSink подключает приёмник событий (реализация EventAwareStrategy).
func (f *FactsMemory) SetEventSink(fn EventSinkFunc) { f.sink = fn }

// emit отправляет событие стратегии, если подключён приёмник.
func (f *FactsMemory) emit(kind, text string) {
	if f.sink != nil {
		f.sink(StrategyEvent{Kind: kind, Text: text})
	}
}

// History возвращает всю историю из short-term памяти.
func (f *FactsMemory) History(mem dialog.Memory) []llm.Message {
	hist, _ := mem.Load()
	return hist
}

// Build собирает запрос: последние keepLast сообщений + ввод пользователя,
// попутно сообщая о состоянии рабочей памяти.
func (f *FactsMemory) Build(hist []llm.Message, input string) []llm.Message {
	if f.work != nil {
		if n := len(f.work.All()); n > 0 {
			f.emit(EventLog, fmt.Sprintf("facts: в рабочей памяти %d фактов текущей задачи, отправляю последние %d сообщений", n, f.keepLast))
		} else {
			f.emit(EventLog, fmt.Sprintf("facts: рабочая память пуста, отправляю последние %d сообщений", f.keepLast))
		}
	}
	if n := len(hist); n > f.keepLast {
		hist = hist[n-f.keepLast:]
	}
	return append(append([]llm.Message{}, hist...), llm.Message{Role: "user", Content: input})
}

// Observe — no-op: рабочая память пополняется агентом в шаге роутинга.
func (f *FactsMemory) Observe(hist []llm.Message) error { return nil }

// Reset — no-op.
func (f *FactsMemory) Reset() error { return nil }

// State возвращает параметры стратегии и сводку рабочей памяти.
func (f *FactsMemory) State() string {
	n := 0
	if f.work != nil {
		n = len(f.work.All())
	}
	return fmt.Sprintf("facts keep_last=%d, рабочая память: %d фактов", f.keepLast, n)
}

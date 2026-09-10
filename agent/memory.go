package agent

import "aichallenge/llm"

// Memory — абстракция над хранилищем истории диалога. Ядро Agent работает только
// с этим интерфейсом, поэтому среда исполнения не зависит от конкретной БД.
//
// На Этапе 1 используется простая in-memory реализация (InMemory). На Этапе 2
// подключается SQLiteMemory — интерфейс при этом не меняется.
type Memory interface {
	// Load возвращает всю сохранённую историю в порядке диалога.
	Load() ([]llm.Message, error)
	// Append добавляет одно сообщение в конец истории.
	Append(msg llm.Message) error
	// Reset очищает историю.
	Reset() error
	// Close закрывает хранилище (освобождает ресурсы).
	Close() error
}

// InMemory — хранилище истории в оперативной памяти (Этап 1). Ничего не пишет на диск.
type InMemory struct {
	msgs []llm.Message
}

// NewInMemory создаёт пустое in-memory хранилище.
func NewInMemory() *InMemory { return &InMemory{} }

// Load возвращает историю.
func (m *InMemory) Load() ([]llm.Message, error) { return m.msgs, nil }

// Append добавляет сообщение.
func (m *InMemory) Append(msg llm.Message) error {
	m.msgs = append(m.msgs, msg)
	return nil
}

// Reset очищает историю.
func (m *InMemory) Reset() error {
	m.msgs = nil
	return nil
}

// Close — заглушка, ресурсы не использует.
func (m *InMemory) Close() error { return nil }

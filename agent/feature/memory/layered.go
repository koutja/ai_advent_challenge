// Package memory — многослойная модель памяти агента.
//
// Разделяет информацию на три хранимых раздельно слоя:
//
//   - short — краткосрочная память: история текущего диалога (feature/dialog);
//   - working — рабочая память: данные ТЕКУЩЕЙ задачи (ключ → значение);
//   - long — долговременная память: профиль, решения, знания (переживает перезапуск).
//
// Слои хранятся отдельными хранилищами. Выбор «что и куда сохраняется» — явный:
// короткий слой пополняется каждым ходом, рабочий — через RouteUserTurn,
// долговременный — через Remember/Put (например, командой /remember).
package memory

import (
	"sort"
	"strings"

	"aichallenge/llm"

	"agent/feature/dialog"
)

// Имена слоёв памяти (для логов/отладки).
const (
	LayerShort = "short"
	LayerWork  = "work"
	LayerLong  = "long"
)

// LayeredMemory — композит трёх независимых хранилищ памяти агента.
type LayeredMemory struct {
	short dialog.Memory
	work  WorkingStore
	long  LongStore
}

// NewLayered собирает многослойную память из готовых хранилищ.
func NewLayered(short dialog.Memory, work WorkingStore, long LongStore) *LayeredMemory {
	return &LayeredMemory{short: short, work: work, long: long}
}

// NewLayeredRAM создаёт полностью in-memory модель (для тестов и CLI без диска).
func NewLayeredRAM() *LayeredMemory {
	return &LayeredMemory{
		short: dialog.NewInMemory(),
		work:  NewInMemoryWorking(),
		long:  NewInMemoryLong(),
	}
}

// NewLayeredSQLite создаёт модель: short- и long-слои в SQLite (переживают
// перезапуск), working — в RAM (жизнь одной задачи).
func NewLayeredSQLite(historyPath, longPath string) (*LayeredMemory, error) {
	short, err := dialog.NewSQLiteMemory(historyPath)
	if err != nil {
		return nil, err
	}
	long, err := NewSQLiteLong(longPath)
	if err != nil {
		_ = short.Close()
		return nil, err
	}
	return &LayeredMemory{short: short, work: NewInMemoryWorking(), long: long}, nil
}

// Short возвращает краткосрочный слой (история диалога).
func (m *LayeredMemory) Short() dialog.Memory { return m.short }

// Working возвращает рабочий слой (текущая задача).
func (m *LayeredMemory) Working() WorkingStore { return m.work }

// Long возвращает долговременный слой (профиль/решения/знания).
func (m *LayeredMemory) Long() LongStore { return m.long }

// ResetSession очищает short+working (новая сессия/задача); long сохраняется.
func (m *LayeredMemory) ResetSession() error {
	if err := m.short.Reset(); err != nil {
		return err
	}
	m.work.Clear()
	return nil
}

// ResetAll очищает все три слоя, включая долговременный.
func (m *LayeredMemory) ResetAll() error {
	if err := m.ResetSession(); err != nil {
		return err
	}
	return m.long.Reset()
}

// Close закрывает все хранилища.
func (m *LayeredMemory) Close() error {
	if m.short != nil {
		_ = m.short.Close()
	}
	if m.long != nil {
		_ = m.long.Close()
	}
	return nil
}

// Remember явно сохраняет запись в долговременный слой (например, /remember).
func (m *LayeredMemory) Remember(kind, key, value string) error {
	return m.long.Put(LongEntry{Kind: kind, Key: key, Value: value})
}

// RouteUserTurn — явный роутинг реплики в рабочий слой: извлекает факты
// «ключ → значение» из текста и кладёт их в Working(). Возвращает число новых
// ключей. (Профиль/решения/знания идут в long отдельно, через Remember.)
func (m *LayeredMemory) RouteUserTurn(text string, ex Extractor) int {
	if ex == nil {
		return 0
	}
	added := 0
	for k, v := range ex.Extract(text) {
		if _, ok := m.work.Get(k); !ok {
			added++
		}
		m.work.Set(k, v)
	}
	return added
}

// WorkingSystem возвращает system-блок рабочей памяти (пусто, если фактов нет).
func (m *LayeredMemory) WorkingSystem() string {
	all := m.work.All()
	if len(all) == 0 {
		return ""
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("Данные текущей задачи:\n")
	for _, k := range keys {
		sb.WriteString("- " + k + ": " + all[k] + "\n")
	}
	return sb.String()
}

// LongSystem возвращает system-блок долговременной памяти, сгруппированный
// по типу (профиль/решения/знания/предпочтения). Пусто, если записей нет.
func (m *LayeredMemory) LongSystem() string {
	entries, err := m.long.All()
	if err != nil || len(entries) == 0 {
		return ""
	}
	groups := map[string][]string{}
	order := []string{}
	for _, e := range entries {
		if _, ok := groups[e.Kind]; !ok {
			order = append(order, e.Kind)
		}
		groups[e.Kind] = append(groups[e.Kind], "- "+e.Key+": "+e.Value)
	}
	var sb strings.Builder
	sb.WriteString("Долговременная память о пользователе:\n")
	for _, kind := range order {
		sb.WriteString(kind + ":\n")
		sb.WriteString(strings.Join(groups[kind], "\n") + "\n")
	}
	return sb.String()
}

// Prepend добавляет system-блоки долговременной и рабочей памяти в начало
// сообщений запроса (перед историей диалога и вводом). Именно так слои памяти
// влияют на ответы агента.
func (m *LayeredMemory) Prepend(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(msgs)+2)
	if s := m.LongSystem(); s != "" {
		out = append(out, llm.Message{Role: "system", Content: s})
	}
	if s := m.WorkingSystem(); s != "" {
		out = append(out, llm.Message{Role: "system", Content: s})
	}
	return append(out, msgs...)
}

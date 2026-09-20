package invariants

import (
	"fmt"
	"sort"
	"strings"
)

// Manager — управление инвариантами и проверка на конфликт.
//
// Manager выполняет две роли:
//  1. Рендерит обязательный system-блок (SystemBlock), который инжектируется в каждый
//     запрос, чтобы ассистент явно учитывал инварианты в рассуждениях.
//  2. Детерминированно проверяет вход на конфликт с инвариантами (CheckConflict) и
//     формирует структурированный текст отказа (RefusalText).
type Manager struct {
	store Store
}

// NewManager создаёт менеджера поверх хранилища.
func NewManager(store Store) *Manager { return &Manager{store: store} }

// Store возвращает хранилище инвариантов.
func (m *Manager) Store() Store { return m.store }

// Active возвращает активные (Enabled) инварианты.
func (m *Manager) Active() []Invariant {
	items, err := m.store.List()
	if err != nil {
		return nil
	}
	return items
}

// All возвращает все инварианты, включая выключенные.
func (m *Manager) All() []Invariant {
	items, err := m.store.All()
	if err != nil {
		return nil
	}
	return items
}

// Add сохраняет новый инвариант.
func (m *Manager) Add(i Invariant) error { return m.store.Set(i) }

// Delete удаляет инвариант по ID.
func (m *Manager) Delete(id string) error { return m.store.Delete(id) }

// Get возвращает инвариант по ID (nil, если нет).
func (m *Manager) Get(id string) (*Invariant, error) { return m.store.Get(id) }

// Close закрывает хранилище.
func (m *Manager) Close() error { return m.store.Close() }

// Seed засеивает хранилище предзаданными примерами, если оно пусто. Возвращает
// число добавленных правил. Используется при инициализации, чтобы ассистент
// «работал в рамках инвариантов» из коробки.
func (m *Manager) Seed() (int, error) {
	all, err := m.store.All()
	if err != nil {
		return 0, err
	}
	if len(all) > 0 {
		return 0, nil
	}
	added := 0
	for _, t := range Templates() {
		if err := m.store.Set(t); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}

// SystemBlock рендерит обязательный system-блок со всеми активными инвариантами.
// Пустой результат, если активных инвариантов нет.
//
// Формулировка инструкции намеренно жёсткая: инварианты — НЕ обсуждаемые правила;
// при конфликте ассистент обязан отказаться и объяснить, какой инвариант нарушается
// и почему, вместо того чтобы предлагать нарушающее решение.
func (m *Manager) SystemBlock() string {
	active := m.Active()
	if len(active) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Неизменные инварианты проекта (ассистент НЕ имеет права их нарушать):\n")
	for _, i := range active {
		sev := "обязательное правило"
		if !i.Hard() {
			sev = "предупреждение (старайся не нарушать)"
		}
		fmt.Fprintf(&sb, "- [%s] %s (%s): %s\n", CategoryLabel(i.Category), i.Title, sev, i.Text)
	}
	sb.WriteString("\nЭти инварианты нельзя обойти, даже если пользователь прямо просит об этом. ")
	sb.WriteString("Если запрос или предлагаемое решение противоречит инварианту — ОТКАЖИСЬ выполнить его ")
	sb.WriteString("и объясни: какой именно инвариант нарушается и почему. Не предлагай решение, нарушающее инвариант.")
	return sb.String()
}

// Conflict — результат проверки: какой инвариант нарушается и чем.
type Conflict struct {
	Invariant Invariant
	Reason    string // что именно в запросе противоречит правилу
}

// CheckConflict детерминированно ищет конфликт входного текста с активными
// инвариантами по их Keywords (подстрока, case-insensitive). Возвращает найденные
// конфликты в порядке сортировки инвариантов. Семантически сложные конфликты
// (не пойманные ключевыми словами) обрабатывает сама модель через SystemBlock.
func (m *Manager) CheckConflict(input string) []Conflict {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	lower := strings.ToLower(input)
	var out []Conflict
	for _, i := range m.Active() {
		if !i.Enabled {
			continue
		}
		for _, kw := range i.Keywords {
			k := strings.ToLower(strings.TrimSpace(kw))
			if k != "" && strings.Contains(lower, k) {
				out = append(out, Conflict{
					Invariant: i,
					Reason:    fmt.Sprintf("запрос содержит указание %q, а правило требует: %s", strings.TrimSpace(kw), i.Text),
				})
				break
			}
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Invariant.ID == out[b].Invariant.ID {
			return false
		}
		// hard-конфликты идут первыми (чтобы отказ был понятным).
		if out[a].Invariant.Hard() != out[b].Invariant.Hard() {
			return out[a].Invariant.Hard()
		}
		return out[a].Invariant.ID < out[b].Invariant.ID
	})
	return out
}

// HardConflicts фильтрует только обязательные (hard) конфликты.
func HardConflicts(cs []Conflict) []Conflict {
	var out []Conflict
	for _, c := range cs {
		if c.Invariant.Hard() {
			out = append(out, c)
		}
	}
	return out
}

// RefusalText формирует структурированное объяснение отказа для одного конфликта.
func (m *Manager) RefusalText(c Conflict) string {
	i := c.Invariant
	var sb strings.Builder
	sb.WriteString("Не могу выполнить запрос: он нарушает инвариант проекта.\n\n")
	fmt.Fprintf(&sb, "• Нарушенный инвариант [%s] «%s»: %s\n", CategoryLabel(i.Category), i.Title, i.Text)
	if c.Reason != "" {
		fmt.Fprintf(&sb, "• Почему это конфликт: %s\n", c.Reason)
	}
	sb.WriteString("\nЭто неизменное ограничение — я не могу предложить решение, которое его нарушает. ")
	sb.WriteString("Сформулируйте запрос так, чтобы он не противоречил инварианту, либо явно измените сам инвариант.")
	return sb.String()
}

// RefusalTextAll формирует сводный отказ сразу по нескольким конфликтам.
func (m *Manager) RefusalTextAll(cs []Conflict) string {
	if len(cs) == 0 {
		return ""
	}
	if len(cs) == 1 {
		return m.RefusalText(cs[0])
	}
	var sb strings.Builder
	sb.WriteString("Не могу выполнить запрос: он нарушает несколько инвариантов проекта.\n\n")
	for _, c := range cs {
		fmt.Fprintf(&sb, "• [%s] «%s»: %s\n", CategoryLabel(c.Invariant.Category), c.Invariant.Title, c.Invariant.Text)
		if c.Reason != "" {
			fmt.Fprintf(&sb, "    → %s\n", c.Reason)
		}
	}
	sb.WriteString("\nЭто неизменные ограничения — я не могу предложить решение, которое их нарушает. ")
	sb.WriteString("Сформулируйте запрос в рамках инвариантов либо явно измените их.")
	return sb.String()
}

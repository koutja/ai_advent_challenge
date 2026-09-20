// Package invariants — жёсткие правила (инварианты), которые ассистент не имеет
// права нарушать.
//
// Инварианты описывают неизменные ограничения состояния: выбранную архитектуру,
// принятые технические решения, ограничения стека, бизнес-правила. Они хранятся
// ОТДЕЛЬНО от диалога (персистентный store, переживает /reset и /newtask) и
// инжектируются в каждый запрос к LLM обязательным system-блоком, чтобы ассистент
// явно учитывал их в рассуждениях и отказывался предлагать решения, которые их
// нарушают.
package invariants

// Severity — строгость инварианта.
const (
	// SeverityHard — нарушение приводит к обязательному отказу БЕЗ ответа LLM.
	SeverityHard = "hard"
	// SeveritySoft — нарушение фиксируется предупреждением, но ответ допустим.
	SeveritySoft = "soft"
)

// Категории инвариантов (по типам из примера задания).
const (
	CatArchitecture = "архитектура"
	CatDecision     = "техническое решение"
	CatStack        = "стек"
	CatBusiness     = "бизнес-правило"
)

// Categories — канонический список категорий (для UI/валидации).
var Categories = []string{CatArchitecture, CatDecision, CatStack, CatBusiness}

// Invariant — одно правило, которое ассистент не имеет права нарушать.
type Invariant struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`              // краткое имя (для вывода)
	Category string   `json:"category"`           // архитектура | техническое решение | стек | бизнес-правило
	Text     string   `json:"text"`               // формулировка правила
	Keywords []string `json:"keywords,omitempty"` // подсказки для детекции конфликта (case-insensitive)
	Severity string   `json:"severity"`           // hard | soft
	Enabled  bool     `json:"enabled"`            // активен ли (участвует в SystemBlock и проверках)
}

// Empty сообщает, что в правиле нет ничего полезного (используется для валидации).
func (i Invariant) Empty() bool {
	return i.ID == "" && i.Title == "" && i.Text == ""
}

// Hard сообщает, что инвариант является обязательным (включён и severity=hard).
func (i Invariant) Hard() bool { return i.Enabled && i.Severity == SeverityHard }

// Valid проверяет, достаточно ли заполнено правило для сохранения.
func (i Invariant) Valid() bool {
	return i.Title != "" && i.Text != "" && i.Severity != "" && i.ID != ""
}

// CategoryLabel возвращает человекочитаемую подпись категории (саму категорию,
// если пусто — «без категории»).
func CategoryLabel(c string) string {
	if c == "" {
		return "без категории"
	}
	return c
}

// Templates — предзаданные инварианты-примеры (по типам из задания). Используются
// как seed, чтобы ассистент «работал в рамках заданных инвариантов» из коробки.
func Templates() []Invariant {
	return []Invariant{
		{
			ID:       "arch_clean",
			Title:    "Чистая архитектура",
			Category: CatArchitecture,
			Text:     "Слои зависимостей направлены только внутрь: домен не зависит от инфраструктуры.",
			Keywords: []string{"монолит", "spaghetti", "хаос", "смешать слои", "слои"},
			Severity: SeverityHard,
			Enabled:  true,
		},
		{
			ID:       "dec_pg_sqlite",
			Title:    "SQLite для хранения",
			Category: CatDecision,
			Text:     "Персистентность реализуется на SQLite (modernc.org/sqlite); другие СУБД не вводим без смены решения.",
			Keywords: []string{"postgres", "mysql", "mongodb", "redis", "oracle", "база данных"},
			Severity: SeverityHard,
			Enabled:  true,
		},
		{
			ID:       "stack_go",
			Title:    "Стек: Go",
			Category: CatStack,
			Text:     "Реализация выполняется на Go; другие языки (Python, Node, Java, Rust) не используются.",
			Keywords: []string{"python", "node", "java", "rust", "go", "переписать на"},
			Severity: SeverityHard,
			Enabled:  true,
		},
		{
			ID:       "biz_pii",
			Title:    "Не собирать PII",
			Category: CatBusiness,
			Text:     "Личные данные пользователей (PII) не сохраняются и не логируются без явного согласия.",
			Keywords: []string{"паспорт", "email клиента", "телефон клиента", "pii", "персональные данные"},
			Severity: SeverityHard,
			Enabled:  true,
		},
		{
			ID:       "soft_perf",
			Title:    "Осторожно с производительностью",
			Category: CatDecision,
			Text:     "Избегать алгоритмов с явно избыточной сложностью (O(n^2) и выше) там, где возможен линейный обход.",
			Keywords: []string{"вложенный цикл", "o(n2)", "nested loop", "медленно"},
			Severity: SeveritySoft,
			Enabled:  true,
		},
	}
}

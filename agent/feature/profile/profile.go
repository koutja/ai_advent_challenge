// Package profile — персонализация ассистента поверх модели памяти.
//
// Структурированный профиль пользователя (роль, стиль, формат, ограничения,
// экспертиза), который подключается ПЕРВЫМ system-блоком к каждому запросу,
// чтобы ассистент автоматически адаптировал ответы под конкретного пользователя.
// Профили хранятся в отдельном хранилище (feature/profile) и переживают перезапуск.
package profile

import (
	"sort"
	"strings"
)

// Profile — структурированный профиль пользователя.
type Profile struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	Language    string   `json:"language"`
	Style       string   `json:"style"`
	Format      string   `json:"format"`
	Constraints []string `json:"constraints,omitempty"`
	Expertise   []string `json:"expertise,omitempty"`
}

// Empty возвращает true, если в профиле нет ничего, что можно подставить.
func (p Profile) Empty() bool {
	return p.ID == "" && p.Name == "" && p.Role == "" && p.Language == "" &&
		p.Style == "" && p.Format == "" && len(p.Constraints) == 0 && len(p.Expertise) == 0
}

// SystemBlock рендерит профиль в system-сообщение, открывающее каждый запрос.
// Пустой профиль — пустая строка (блок не добавляется).
func (p Profile) SystemBlock() string {
	if p.Empty() {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Профиль пользователя:\n")
	addLine(&sb, "имя", p.Name)
	addLine(&sb, "роль", p.Role)
	addLine(&sb, "язык ответов", p.Language)
	addLine(&sb, "стиль", p.Style)
	addLine(&sb, "формат", p.Format)
	addList(&sb, "ограничения", p.Constraints)
	addList(&sb, "экспертиза", p.Expertise)
	sb.WriteString("\nАдаптируй ответы под этого пользователя: соблюдай стиль, формат и ограничения.")
	return sb.String()
}

func addLine(sb *strings.Builder, label, val string) {
	if strings.TrimSpace(val) != "" {
		sb.WriteString("- " + label + ": " + strings.TrimSpace(val) + "\n")
	}
}

func addList(sb *strings.Builder, label string, items []string) {
	if len(items) == 0 {
		return
	}
	sorted := append([]string{}, items...)
	sort.Strings(sorted)
	sb.WriteString("- " + label + ": " + strings.Join(sorted, "; ") + "\n")
}

// FieldNames — список редактируемых полей (для CLI/web).
var FieldNames = []string{"id", "name", "role", "language", "style", "format", "constraint", "expertise"}

// SetField обновляет одно скалярное поле профиля. constraint/expertise добавляют
// элемент в список. Возвращает false, если поле неизвестно.
func (p *Profile) SetField(field, value string) bool {
	value = strings.TrimSpace(value)
	switch strings.ToLower(field) {
	case "name":
		p.Name = value
	case "role":
		p.Role = value
	case "language":
		p.Language = value
	case "style":
		p.Style = value
	case "format":
		p.Format = value
	case "constraint", "constraints":
		if value != "" {
			p.Constraints = append(p.Constraints, value)
		}
	case "expertise":
		if value != "" {
			p.Expertise = append(p.Expertise, value)
		}
	default:
		return false
	}
	return true
}

// Templates — заготовки профилей для быстрой демонстрации/сравнения.
func Templates() map[string]Profile {
	return map[string]Profile{
		// «Кутякин» — пример по анкете: Flutter/Dart разработчик.
		"kutyakin": {
			ID:       "kutyakin",
			Name:     "Михаил Кутякин",
			Role:     "Flutter/Dart разработчик",
			Language: "русский",
			Style:    "технично, лаконично, придерживаться компромиссов без усложнения решения",
			Format:   "коротко, по делу, списками и фрагментами кода",
			Constraints: []string{
				"без воды и пространных вступлений",
				"не генерировать лишний код",
				"кратко объяснять паттерны и принципы",
			},
			Expertise: []string{
				"Dart/Flutter",
				"bloc/cubit",
				"dio, openapi",
				"нативные iOS/Android (Kotlin/Swift)",
				"адаптивные интерфейсы",
			},
		},
		// «terse» — максимально краткий профиль для сравнения.
		"terse": {
			ID:          "terse",
			Name:        "Краткий пользователь",
			Language:    "русский",
			Style:       "максимально кратко, без деталей",
			Format:      "списком, 2–3 пункта",
			Constraints: []string{"односложные ответы", "никакого разжёвывания"},
		},
		// «detailed» — развёрнутый профиль для сравнения.
		"detailed": {
			ID:          "detailed",
			Name:        "Внимательный пользователь",
			Language:    "русский",
			Style:       "развёрнуто, объясняя детали и подводные камни",
			Format:      "структурированный текст с примерами и аргументами",
			Constraints: []string{"всегда пояснять «почему»", "давать альтернативы"},
		},
	}
}

// TemplateNames возвращает отсортированные имена заготовок.
func TemplateNames() []string {
	names := make([]string, 0, len(Templates()))
	for k := range Templates() {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

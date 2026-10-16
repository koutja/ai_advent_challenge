package agent

import (
	"strings"
	"testing"

	"agent/feature/rag"
)

// TestParseReviewSections — корректный отчёт с тремя секциями.
func TestParseReviewSections(t *testing.T) {
	text := `Общий комментарий.

### Потенциальные баги
- nil pointer в agent.go:42
- нет обработки ошибки

### Архитектурные проблемы
- дублирование логики ретрива

### Рекомендации
- вынести роутинг в отдельный метод
- добавить юнит-тесты`

	bugs, arch, recs, ok := parseReviewSections(text)
	if !ok {
		t.Fatal("ожидали FormatOK=true")
	}
	if len(bugs) != 2 || bugs[0] == "" || !strings.Contains(bugs[0], "nil pointer") {
		t.Errorf("баги: %v", bugs)
	}
	if len(arch) != 1 || !strings.Contains(arch[0], "дублирование") {
		t.Errorf("архитектура: %v", arch)
	}
	if len(recs) != 2 {
		t.Errorf("рекомендации: %v", recs)
	}
}

// TestParseReviewSectionsColon — заголовки с двоеточием (реальный вывод LLM).
func TestParseReviewSectionsColon(t *testing.T) {
	text := `### Потенциальные баги:
- нет проверки ошибки

### Архитектурные проблемы:
- дублирование

### Рекомендации:
- рефакторинг`
	bugs, arch, recs, ok := parseReviewSections(text)
	if !ok {
		t.Fatal("ожидали FormatOK=true при заголовках с двоеточием")
	}
	if len(bugs) != 1 || len(arch) != 1 || len(recs) != 1 {
		t.Errorf("секции с двоеточием: bugs=%v arch=%v recs=%v", bugs, arch, recs)
	}
	if !strings.Contains(arch[0], "дублирование") {
		t.Errorf("архитектура: %v", arch)
	}
}

// TestParseReviewSectionsMissing — неполный отчёт → FormatOK=false.
func TestParseReviewSectionsMissing(t *testing.T) {
	_, _, _, ok := parseReviewSections("### Потенциальные баги\n- один пункт\nбез остальных секций")
	if ok {
		t.Error("ожидали FormatOK=false при неполном отчёте")
	}
}

// TestParseReviewSectionsEmpty — пустой текст → FormatOK=false.
func TestParseReviewSectionsEmpty(t *testing.T) {
	_, _, _, ok := parseReviewSections("")
	if ok {
		t.Error("ожидали FormatOK=false для пустого текста")
	}
}

// TestReviewEmptyDiff — пустой diff отклоняется до обращения к RAG/LLM.
func TestReviewEmptyDiff(t *testing.T) {
	ag := &Agent{rag: rag.NewRetriever(rag.Config{})}
	if _, err := ag.Review("   ", "agent/agent.go"); err == nil {
		t.Error("ожидали ошибку «пустой diff»")
	}
}

// TestReviewNoRAG — без RAG ревью невозможно.
func TestReviewNoRAG(t *testing.T) {
	ag := &Agent{}
	if _, err := ag.Review("+func main() {}", ""); err == nil {
		t.Error("ожидали ошибку «RAG не настроен»")
	}
}

// TestReviewNoClient — RAG есть, но LLM-клиент не инициализирован.
func TestReviewNoClient(t *testing.T) {
	ag := &Agent{rag: rag.NewRetriever(rag.Config{}), cfg: &Config{}}
	if _, err := ag.Review("+func main() {}", "agent/main.go"); err == nil {
		t.Error("ожидали ошибку «LLM-клиент не инициализирован»")
	}
}

// TestReviewQuery — запрос в RAG строится по изменённым файлам.
func TestReviewQuery(t *testing.T) {
	q := reviewQuery("M\tagent/agent.go\nM\tagent/review.go")
	if !strings.Contains(q, "agent/agent.go") || !strings.Contains(q, "agent/review.go") {
		t.Errorf("неожиданный RAG-запрос: %q", q)
	}
	q = reviewQuery("")
	if !strings.Contains(q, "конвенции") {
		t.Errorf("пустой файловый список должен дать запрос про конвенции: %q", q)
	}
}

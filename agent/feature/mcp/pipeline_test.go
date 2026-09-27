package mcpx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateCorpus изолирует тесты от персистентного корпуса (results/pipeline),
// чтобы сгенерированные пользователем документы не влияли на детерминизм.
func isolateCorpus(t *testing.T) {
	t.Helper()
	t.Setenv("MCP_CORPUS_FILE", filepath.Join(t.TempDir(), "corpus.json"))
}

// TestSearchDeterministic: один и тот же запрос даёт один и тот же результат.
func TestSearchDeterministic(t *testing.T) {
	isolateCorpus(t)
	a, b := Search("mcp", 5), Search("mcp", 5)
	if a.Total != b.Total || len(a.Docs) != len(b.Docs) {
		t.Fatalf("search не детерминирован: %+v vs %+v", a, b)
	}
	for i := range a.Docs {
		if a.Docs[i] != b.Docs[i] {
			t.Fatalf("разные документы: %+v vs %+v", a.Docs[i], b.Docs[i])
		}
	}
}

// TestSearchFound: по "go" находятся документы про Go SDK.
func TestSearchFound(t *testing.T) {
	isolateCorpus(t)
	res := Search("Go", 5)
	if res.Total == 0 {
		t.Fatalf("по запросу Go ничего не найдено")
	}
	found := false
	for _, d := range res.Docs {
		if strings.Contains(strings.ToLower(d.Title+" "+d.Snippet), "go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("документы не содержат запрос: %+v", res.Docs)
	}
}

// TestSearchLimit: limit соблюдается.
func TestSearchLimit(t *testing.T) {
	isolateCorpus(t)
	res := Search("", 2) // пустой запрос — все документы
	if res.Total != 2 || len(res.Docs) != 2 {
		t.Fatalf("limit=2 не соблюдён: total=%d docs=%d", res.Total, len(res.Docs))
	}
}

// TestSearchMultiToken: многословный запрос работает — ищется по словам,
// а не целой строкой (регрессия: «golang зачем нужен» должно находить doc-007).
func TestSearchMultiToken(t *testing.T) {
	isolateCorpus(t)
	res := Search("golang зачем нужен", 5)
	if res.Total == 0 {
		t.Fatalf("многословный запрос ничего не нашёл: %+v", res)
	}
	if !strings.Contains(strings.ToLower(res.Docs[0].Title), "golang") {
		t.Fatalf("лучший результат не про golang: %+v", res.Docs[0])
	}
}

// TestSearchNoFalsePrefix: короткие слова (предлог «с») не должны ложно
// совпадать по префиксу с длинными токенами («сериал»).
func TestSearchNoFalsePrefix(t *testing.T) {
	isolateCorpus(t)
	res := Search("сериал друзья", 5)
	if res.Total != 0 {
		t.Fatalf("ожидали 0 результатов, получили %d: %+v", res.Total, res.Docs)
	}
}

// TestSearchWordForm: префиксный проход ловит словоформы («инструмент» → «инструментов»).
func TestSearchWordForm(t *testing.T) {
	isolateCorpus(t)
	res := Search("инструмент", 5)
	if res.Total == 0 {
		t.Fatalf("словоформа не найдена")
	}
	found := false
	for _, d := range res.Docs {
		if strings.Contains(strings.ToLower(d.Title+" "+d.Snippet), "инструмент") {
			found = true
		}
	}
	if !found {
		t.Fatalf("ни один документ не содержит словоформу: %+v", res.Docs)
	}
}

// TestSearchRanking: документ с большим числом совпавших токенов выше.
func TestSearchRanking(t *testing.T) {
	isolateCorpus(t)
	res := Search("mcp пайплайн", 5)
	if res.Total < 2 {
		t.Fatalf("ожидали несколько результатов: %d", res.Total)
	}
	// doc-004 содержит оба слова («пайплайн» и «mcp»), остальные — одно.
	if !strings.Contains(strings.ToLower(res.Docs[0].Title), "композиция") {
		t.Fatalf("первый результат не doc-004: %+v", res.Docs[0])
	}
}

// TestSummarizeWordLimit: сводка не превышает лимит слов и детерминирована.
func TestSummarizeWordLimit(t *testing.T) {
	text := "MCP открывает агентам доступ к инструментам. " +
		"Сервер регистрирует функции и возвращает результаты. " +
		"Клиент вызывает их по stdio и передаёт данные дальше."
	a := Summarize(text, 10)
	b := Summarize(text, 10)
	if a != b {
		t.Fatalf("summarize не детерминирован: %+v vs %+v", a, b)
	}
	if a.Words > 10 {
		t.Fatalf("слов больше лимита: %d", a.Words)
	}
	if a.Sources != 1 {
		t.Fatalf("ожидали 1 источник, получили %d", a.Sources)
	}
}

// TestSummarizeSources: число источников = непустых абзацев.
func TestSummarizeSources(t *testing.T) {
	res := Summarize("Первый абзац.\n\nВторой абзац.", 100)
	if res.Sources != 2 {
		t.Fatalf("ожидали 2 источника, получили %d", res.Sources)
	}
}

// TestSanitizeFilename: убираются пути и опасные символы.
func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"summary.md":   "summary.md",
		"../evil.md":   "evil.md",
		"a/b/c.txt":    "c.txt",
		"..":           "",
		"отчёт v2.md":  "отчёт_v2.md", // кириллица сохраняется, пробел → _
		"my report.md": "my_report.md",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, ожидали %q", in, got, want)
		}
	}
}

// TestCorpusStoreRoundTrip: сгенерированные документы сохраняются в корпус
// и становятся доступны поиску.
func TestCorpusStoreRoundTrip(t *testing.T) {
	t.Setenv("MCP_CORPUS_FILE", filepath.Join(t.TempDir(), "corpus.json"))

	cs, err := openCorpusStore()
	if err != nil {
		t.Fatalf("openCorpusStore: %v", err)
	}
	// Уникальная тема, которой нет в статическом корпусе.
	added, err := cs.add(GeneratedDoc{Topic: "ml", Title: "Про машинное обучение", Snippet: "Машинное обучение помогает агентам принимать решения.", Source: "llm"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if added.ID == "" {
		t.Fatalf("документу не присвоен id")
	}

	res := Search("обучение", 5)
	if res.Total == 0 {
		t.Fatalf("search не видит сгенерированный документ")
	}
	if res.Docs[0].ID != added.ID {
		t.Fatalf("search вернул не тот документ: %+v", res.Docs[0])
	}
}

// TestGenerateDocumentFallback: при недоступном LLM возвращается
// детерминированный fallback, и документ попадает в корпус.
func TestGenerateDocumentFallback(t *testing.T) {
	t.Setenv("MCP_CORPUS_FILE", filepath.Join(t.TempDir(), "corpus.json"))
	// Ключ задан, но эндпоинт гарантированно недоступен -> быстрый отказ без сети.
	t.Setenv("LLM_API_KEY", "test-key")
	t.Setenv("LLM_BASE_URL", "http://127.0.0.1:9")

	// Тема, которой нет в статическом корпусе (чтобы не мешал doc-009 JavaScript).
	doc, err := GenerateDocument("ruby")
	if err != nil {
		t.Fatalf("GenerateDocument: %v", err)
	}
	if doc.Source != "fallback" {
		t.Fatalf("ожидали fallback, получили %q", doc.Source)
	}
	if doc.Title != "ruby" {
		t.Fatalf("title fallback должен быть темой: %q", doc.Title)
	}

	res := Search("ruby", 5)
	if res.Total != 1 || res.Docs[0].ID != doc.ID {
		t.Fatalf("search не нашёл сгенерированный документ: %+v (doc=%+v)", res, doc)
	}
}

// TestSaveFileRoundTrip: файл записывается и читается обратно.
func TestSaveFileRoundTrip(t *testing.T) {
	t.Setenv("MCP_OUTPUT_DIR", t.TempDir())
	res, err := SaveFile("../notes.md", "hello world")
	if err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	if filepath.Base(res.Path) != "notes.md" {
		t.Fatalf("путь вне выходной папки? %s", res.Path)
	}
	data, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("чтение сохранённого файла: %v", err)
	}
	if string(data) != "hello world" || res.Bytes != len("hello world") {
		t.Fatalf("контент не совпадает: %q (%d)", data, res.Bytes)
	}
}

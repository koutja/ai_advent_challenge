package mcpx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSearchDeterministic: один и тот же запрос даёт один и тот же результат.
func TestSearchDeterministic(t *testing.T) {
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
	res := Search("", 2) // пустой запрос — все документы
	if res.Total != 2 || len(res.Docs) != 2 {
		t.Fatalf("limit=2 не соблюдён: total=%d docs=%d", res.Total, len(res.Docs))
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

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agent/feature/rag"
)

// TestSayHelpNoRAG — без настроенного RAG SayHelp возвращает ошибку.
func TestSayHelpNoRAG(t *testing.T) {
	ag := &Agent{}
	if _, err := ag.SayHelp("вопрос", ""); err == nil {
		t.Error("ожидали ошибку «RAG не настроен»")
	}
}

// TestSayHelpEmptyQuery — пустой вопрос отклоняется до обращения к ретриверу.
func TestSayHelpEmptyQuery(t *testing.T) {
	ag := &Agent{rag: rag.NewRetriever(rag.Config{})}
	if _, err := ag.SayHelp("   ", "Ветка: main"); err == nil {
		t.Error("ожидали ошибку «пустой вопрос»")
	}
}

// TestSayHelpUnknownOnEmptyIndex — жёсткий гейт «не знаю» без вызова LLM:
// индекс с нерелевантными чанками → Unknown-ответ (LLM не нужен, ключевой
// fallback по chunks.jsonl). Проверяет весь путь SayHelp: git-контекст →
// extraSystem → ретрив → гейт.
func TestSayHelpUnknownOnEmptyIndex(t *testing.T) {
	dir := t.TempDir()
	writeHelpChunks(t, dir, "structure")

	cfg := rag.Config{
		Enabled:        true,
		IndexDir:       dir,
		Strategy:       "structure",
		SidecarURL:     "http://127.0.0.1:1", // недоступен → ключевой fallback
		TopKFetch:      10,
		TopKKeep:       4,
		MinScore:       0.30,
		FilterEnabled:  true,
		RerankMode:     rag.RerankHeuristic,
		UnknownBelow:   0.40,
		TimeoutSeconds: 2,
	}
	ag := &Agent{rag: rag.NewRetriever(cfg), cfg: &Config{}}

	reply, err := ag.SayHelp("как устроен агент и какие MCP-инструменты есть", "Ветка: main")
	if err != nil {
		t.Fatalf("SayHelp: %v", err)
	}
	if !reply.Unknown {
		t.Errorf("ожидали режим «не знаю» (Unknown), получили: %q", reply.Text)
	}
}

// writeHelpChunks создаёт chunks.jsonl с нерелевантными чанками (для гейта «не знаю»).
func writeHelpChunks(t *testing.T, dir, strategy string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, strategy), 0o755); err != nil {
		t.Fatal(err)
	}
	recs := []map[string]any{
		{"metadata": map[string]string{"chunk_id": "x#0000", "source": "docs/x.md",
			"title": "x.md", "section": "Раздел", "format": "md"},
			"text": "Рецепт борща: свёкла, капуста, картофель, морковь, лук."},
		{"metadata": map[string]string{"chunk_id": "y#0000", "source": "docs/y.md",
			"title": "y.md", "section": "Раздел", "format": "md"},
			"text": "Погода в Москве: солнечно, +15 градусов, ветер северный."},
	}
	f, err := os.Create(filepath.Join(dir, strategy, "chunks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

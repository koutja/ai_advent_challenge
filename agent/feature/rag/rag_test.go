package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Помощники ---------------------------------------------------------------

func rec(meta map[string]string, text string) map[string]any {
	return map[string]any{"metadata": meta, "text": text}
}

func writeChunks(t *testing.T, dir, strategy string, recs []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, strategy), 0o755); err != nil {
		t.Fatal(err)
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

func sampleRecs() []map[string]any {
	return []map[string]any{
		rec(map[string]string{"chunk_id": "a#0000", "source": "docs/a.md",
			"title": "a.md", "section": "Установка", "format": "md"},
			"Чтобы подключить пакет llm, добавьте replace aichallenge/llm в go.mod."),
		rec(map[string]string{"chunk_id": "b#0000", "source": "docs/b.go",
			"title": "b.go", "section": "func Run", "format": "code"},
			"func Run() { println(\"hello\") }"),
		rec(map[string]string{"chunk_id": "c#0000", "source": "docs/c.md",
			"title": "c.md", "section": "FAQ", "format": "md"},
			"Вопрос про переменные окружения LLM_API_KEY и LLM_BASE_URL."),
	}
}

// --- LoadChunks / KeywordSearch ---------------------------------------------

func TestLoadChunks(t *testing.T) {
	dir := t.TempDir()
	writeChunks(t, dir, "structure", sampleRecs())
	chunks, err := LoadChunks(dir, "structure")
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 3 {
		t.Fatalf("ожидалось 3 чанка, получили %d", len(chunks))
	}
}

func TestKeywordSearchFindsRightChunk(t *testing.T) {
	dir := t.TempDir()
	writeChunks(t, dir, "structure", sampleRecs())
	chunks, _ := LoadChunks(dir, "structure")

	res := KeywordSearch(chunks, "как подключить пакет llm", 3)
	if len(res) == 0 {
		t.Fatal("ни одного чанка не найдено")
	}
	if res[0].Source != "docs/a.md" {
		t.Fatalf("первый результат должен быть docs/a.md, получили %s", res[0].Source)
	}
	if res[0].Score <= 0 {
		t.Fatalf("score должен быть > 0, получили %v", res[0].Score)
	}
}

func TestKeywordSearchEmptyQueryReturnsFirst(t *testing.T) {
	dir := t.TempDir()
	writeChunks(t, dir, "structure", sampleRecs())
	chunks, _ := LoadChunks(dir, "structure")
	res := KeywordSearch(chunks, "", 2)
	if len(res) != 2 {
		t.Fatalf("пустой запрос должен вернуть topK первых, получили %d", len(res))
	}
}

// --- BuildContext --------------------------------------------------------------

func TestBuildContextNumbered(t *testing.T) {
	chunks := []Chunk{
		{Source: "README.md", Section: "Запуск", Text: "первый чанк"},
		{Source: "llm/llm.go", Section: "—", Text: "второй чанк"},
	}
	ctx := BuildContext(chunks, 1000)
	if !strings.Contains(ctx, "[1] README.md (секция: Запуск)") || !strings.Contains(ctx, "[2] llm/llm.go") {
		t.Fatalf("ожидались маркеры [1]/[2], получили:\n%s", ctx)
	}
}

func TestBuildContextTruncates(t *testing.T) {
	chunks := []Chunk{{Source: "README.md", Section: "—", Text: strings.Repeat("контекст ", 200)}}
	ctx := BuildContext(chunks, 200)
	if len(ctx) > 260 {
		t.Fatalf("контекст должен обрезаться по maxChars, длина %d", len(ctx))
	}
	if !strings.Contains(ctx, "обрезан") {
		t.Fatalf("ожидалась пометка об обрезке:\n%s", ctx)
	}
}

// --- Eval-хелперы --------------------------------------------------------------

func TestFactCoverage(t *testing.T) {
	answer := "Используйте LLM_API_KEY, LLM_BASE_URL и LLM_MODEL из .env."
	hits, total := FactCoverage(answer, []string{"LLM_API_KEY", "LLM_BASE_URL", "несуществующее"})
	if hits != 2 || total != 3 {
		t.Fatalf("ожидалось 2/3, получили %d/%d", hits, total)
	}
}

func TestSourceFound(t *testing.T) {
	chunks := []Chunk{{Source: "agent/config.go", Section: "Config"}, {Source: "README.md"}}
	if !SourceFound(chunks, []string{"config.go"}) {
		t.Fatal("config.go должен быть найден")
	}
	if SourceFound(chunks, []string{"missing/plan.md"}) {
		t.Fatal("нет такого источника — должно быть false")
	}
}

// --- Retrieve: сайдкар и fallback --------------------------------------------

func TestRetrieveUsesSidecar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"engine":"faiss","strategy":"structure","model":"e5",
			"results":[{"rank":1,"score":0.9,"chunk_id":"x#0000","source":"README.md",
			"title":"README.md","section":"Запуск","format":"md","text":"текст чанка"}]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := Config{Enabled: true, SidecarURL: srv.URL, IndexDir: dir,
		Strategy: "structure", TopK: 5, MaxContextChars: 1000, TimeoutSeconds: 5}
	r := NewRetriever(cfg)
	res, err := r.Retrieve(context.Background(), "как запустить")
	if err != nil {
		t.Fatal(err)
	}
	if res.Engine != "sidecar" {
		t.Fatalf("ожидался движок sidecar, получили %s", res.Engine)
	}
	if len(res.Chunks) != 1 || res.Chunks[0].Source != "README.md" {
		t.Fatalf("не тот результат: %+v", res.Chunks)
	}
}

func TestRetrieveFallsBackToKeyword(t *testing.T) {
	// Сайдкар отвечает 500 — Retrieve обязан переключиться на ключевой поиск.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeChunks(t, dir, "structure", sampleRecs())
	cfg := Config{Enabled: true, SidecarURL: srv.URL, IndexDir: dir,
		Strategy: "structure", TopK: 3, MaxContextChars: 1000, TimeoutSeconds: 5}
	r := NewRetriever(cfg)
	res, err := r.Retrieve(context.Background(), "переменные окружения LLM_API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if res.Engine != "keyword" {
		t.Fatalf("ожидался движок keyword, получили %s", res.Engine)
	}
	if len(res.Chunks) == 0 || res.Chunks[0].Source != "docs/c.md" {
		t.Fatalf("fallback должен найти docs/c.md, получили: %+v", res.Chunks)
	}
}

func TestRetrieveDisabled(t *testing.T) {
	cfg := Config{Enabled: false}
	r := NewRetriever(cfg)
	if _, err := r.Retrieve(context.Background(), "x"); err == nil {
		t.Fatal("выключенный RAG обязан вернуть ошибку")
	}
}

// --- SearchSidecar -------------------------------------------------------------

func TestSearchSidecarParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/search" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"engine":"faiss","strategy":"structure",
			"results":[{"rank":1,"score":0.9,"chunk_id":"x","source":"README.md",
			"title":"README.md","section":"S","format":"md","text":"t"}]}`))
	}))
	defer srv.Close()

	cfg := Config{SidecarURL: srv.URL, Strategy: "structure", TopK: 5, TimeoutSeconds: 5}
	res, err := SearchSidecar(context.Background(), cfg, "q", 5)
	if err != nil {
		t.Fatal(err)
	}
	if res.Engine != "faiss" || res.Strategy != "structure" {
		t.Fatalf("неожиданный ответ: %+v", res)
	}
}

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv(EnvSidecarURL, "http://localhost:9999")
	t.Setenv(EnvIndexDir, "/tmp/id")
	cfg := FromEnv()
	if cfg.SidecarURL != "http://localhost:9999" || cfg.IndexDir != "/tmp/id" {
		t.Fatalf("FromEnv не применил переменные: %+v", cfg)
	}
	if cfg.TopK == 0 || cfg.MaxContextChars == 0 {
		t.Fatalf("дефолты не заполнены: %+v", cfg)
	}
}

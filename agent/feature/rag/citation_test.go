package rag

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// --- ParseCitation ----------------------------------------------------------

func TestParseCitation_HappyPath(t *testing.T) {
	raw := "Ответ:\nПакет llm подключается через replace [1].\n\n" +
		"Источники:\n[1] docs/a.md (секция: Установка, chunk: a#0000)\n\n" +
		"Цитаты:\n[1] «Чтобы подключить пакет llm, добавьте replace aichallenge/llm в go.mod.»\n"
	cit := ParseCitation(raw)
	if !cit.FormatOK {
		t.Fatalf("ожидался FormatOK=true, формат не распознан: %+v", cit)
	}
	if cit.Response == "" {
		t.Fatal("пустая секция Ответ")
	}
	if len(cit.Sources) != 1 || cit.Sources[0].Source != "docs/a.md" {
		t.Fatalf("источник не распознан: %+v", cit.Sources)
	}
	if cit.Sources[0].Section != "Установка" || cit.Sources[0].ChunkID != "a#0000" {
		t.Fatalf("метаданные источника не распознаны: %+v", cit.Sources[0])
	}
	if len(cit.Quotes) != 1 {
		t.Fatalf("ожидалась 1 цитата, получили %d", len(cit.Quotes))
	}
	if cit.Quotes[0].Ref != 1 {
		t.Fatalf("номер цитаты должен быть 1: %+v", cit.Quotes[0])
	}
}

func TestParseCitation_MissingSections(t *testing.T) {
	cit := ParseCitation("Просто ответ без секций и без источников.")
	if cit.FormatOK {
		t.Fatal("без секций FormatOK должен быть false")
	}
	if cit.Response == "" {
		t.Fatal("весь текст должен попасть в Response при отсутствии секций")
	}
	if len(cit.Sources) != 0 || len(cit.Quotes) != 0 {
		t.Fatalf("не должно быть источников/цитат: %+v", cit)
	}
}

// --- Валидация анти-галлюцинаций --------------------------------------------

func TestValidateSources_RealAndHallucinated(t *testing.T) {
	chunks := []Chunk{{Rank: 1, Source: "docs/a.md"}}
	cit := Citation{
		Sources: []CitationItem{
			{Ref: 1, Source: "docs/a.md"},     // реальный
			{Ref: 2, Source: "выдуманный.md"}, // галлюцинация
		},
	}
	real, total := ValidateSources(cit, chunks)
	if real != 1 || total != 2 {
		t.Fatalf("ожидалось 1/2, получили %d/%d", real, total)
	}
}

func TestValidateQuotes_VerbatimAndHallucinated(t *testing.T) {
	chunkText := "Чтобы подключить пакет llm, добавьте replace aichallenge/llm в go.mod."
	chunks := []Chunk{{Rank: 1, Text: chunkText}}
	cit := Citation{
		Quotes: []CitationItem{
			{Ref: 1, Source: "добавьте replace aichallenge/llm в go.md"},  // опечатка — не дословно
			{Ref: 1, Source: "добавьте replace aichallenge/llm в go.mod"}, // дословно
			{Ref: 1, Source: "выдуманная цитата, которой нет в чанке"},    // галлюцинация
		},
	}
	verbatim, total := ValidateQuotes(cit, chunks)
	if verbatim != 1 || total != 3 {
		t.Fatalf("ожидалось 1/3, получили %d/%d", verbatim, total)
	}
}

func TestValidateQuotes_NormalizesWhitespace(t *testing.T) {
	// Цитата с лишними пробелами/переносами должна совпадать дословно.
	chunks := []Chunk{{Rank: 1, Text: "первая строка\nвторая строка"}}
	cit := Citation{Quotes: []CitationItem{{Ref: 1, Source: "первая   строка  вторая строка"}}}
	verbatim, total := ValidateQuotes(cit, chunks)
	if verbatim != 1 || total != 1 {
		t.Fatalf("нормализация пробелов: ожидалось 1/1, получили %d/%d", verbatim, total)
	}
}

func TestValidateQuotes_NotInAnyChunk(t *testing.T) {
	// Цитата, которой нет ни в одном чанке, — галлюцинация (проверка по всем чанкам).
	chunks := []Chunk{{Rank: 1, Text: "текст первого чанка"}}
	cit := Citation{Quotes: []CitationItem{{Ref: 1, Source: "выдуманная цитата, которой нет"}}}
	verbatim, total := ValidateQuotes(cit, chunks)
	if verbatim != 0 || total != 1 {
		t.Fatalf("галлюцинация цитаты: ожидалось 0/1, получили %d/%d", verbatim, total)
	}
}

// --- Жёсткий гейт «не знаю» -------------------------------------------------

// sidecarReturning отдаёт один чанк с заданным score.
func sidecarReturning(score float64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"engine":"faiss","strategy":"structure","model":"e5",
			"results":[{"rank":1,"score":` + ftoa(score) + `,"chunk_id":"x#0000","source":"README.md",
			"title":"README.md","section":"Запуск","format":"md","text":"текст чанка"}]}`))
	}))
}

func TestUnknownGate_KeptZero(t *testing.T) {
	// MinScore выше score → фильтр убирает все чанки → Kept==0 → гейт «не знаю».
	srv := sidecarReturning(0.5)
	defer srv.Close()
	cfg := Config{Enabled: true, SidecarURL: srv.URL, IndexDir: t.TempDir(),
		Strategy: "structure", TopK: 5, TopKFetch: 5, TopKKeep: 4,
		MinScore: 0.9, FilterEnabled: true, UnknownBelow: 0.4,
		MaxContextChars: 1000, TimeoutSeconds: 5, CitationsRequired: true}
	r := NewRetriever(cfg)
	// nil-клиент безопасен: гейт срабатывает до обращения к LLM.
	ans, err := AnswerRAGWithMode(context.Background(), nil, r, "вопрос", 100, ModeFilter)
	if err != nil {
		t.Fatal(err)
	}
	if !ans.Unknown {
		t.Fatal("ожидался режим «не знаю» (Kept==0)")
	}
	if ans.Text != UnknownText {
		t.Fatalf("ожидался детерминированный UnknownText, получили: %q", ans.Text)
	}
}

func TestUnknownGate_BelowThreshold(t *testing.T) {
	// Чанк проходит фильтр (score > MinScore), но top-1 ниже UnknownBelow → гейт.
	srv := sidecarReturning(0.5)
	defer srv.Close()
	cfg := Config{Enabled: true, SidecarURL: srv.URL, IndexDir: t.TempDir(),
		Strategy: "structure", TopK: 5, TopKFetch: 5, TopKKeep: 4,
		MinScore: 0.1, FilterEnabled: true, UnknownBelow: 0.9,
		MaxContextChars: 1000, TimeoutSeconds: 5, CitationsRequired: true}
	r := NewRetriever(cfg)
	ans, err := AnswerRAGWithMode(context.Background(), nil, r, "вопрос", 100, ModeFilter)
	if err != nil {
		t.Fatal(err)
	}
	if !ans.Unknown {
		t.Fatal("ожидался режим «не знаю» (top-1 < UnknownBelow)")
	}
}

func TestLooksUnknown(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"Я не знаю. Уточните вопрос.", true},
		{"В контексте нет информации по этому вопросу.", true},
		{"Пакет llm подключается через replace.", false},
	}
	for _, c := range cases {
		if got := looksUnknown(c.text); got != c.want {
			t.Errorf("looksUnknown(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// ftoa — форматирование float для JSON-мока.
func ftoa(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

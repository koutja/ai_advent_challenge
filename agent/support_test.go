package agent

import (
	"testing"

	"agent/feature/rag"
)

// TestSupportAnswerNoRAG — без RAG поддержка невозможна.
func TestSupportAnswerNoRAG(t *testing.T) {
	ag := &Agent{}
	if _, err := ag.SupportAnswer("Почему не работает авторизация?", ""); err == nil {
		t.Error("ожидали ошибку «RAG не настроен»")
	}
}

// TestSupportAnswerEmptyQuestion — пустой вопрос отклоняется.
func TestSupportAnswerEmptyQuestion(t *testing.T) {
	ag := &Agent{rag: rag.NewRetriever(rag.Config{})}
	if _, err := ag.SupportAnswer("   ", "Тикет T-001"); err == nil {
		t.Error("ожидали ошибку «пустой вопрос»")
	}
}

// TestSupportAnswerUnknownOnEmptyIndex — жёсткий гейт «не знаю» без LLM:
// индекс с нерелевантными чанками → Unknown-ответ (ключевой fallback).
func TestSupportAnswerUnknownOnEmptyIndex(t *testing.T) {
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

	reply, err := ag.SupportAnswer("Почему не работает авторизация?",
		"Тикет T-001: Не работает авторизация. Пользователь: Анна (план pro)")
	if err != nil {
		t.Fatalf("SupportAnswer: %v", err)
	}
	if !reply.Unknown {
		t.Errorf("ожидали режим «не знаю» (Unknown), получили: %q", reply.Text)
	}
}

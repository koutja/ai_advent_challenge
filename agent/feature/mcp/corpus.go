package mcpx

import (
	"aichallenge/llm"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// defaultCorpusFile — файл корпуса со сгенерированными документами
// (git игнорирует **/results/).
const defaultCorpusFile = "results/pipeline/corpus.json"

// GeneratedDoc — документ, добавленный в корпус через generate_document.
type GeneratedDoc struct {
	ID      string `json:"id"`
	Topic   string `json:"topic"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
	Source  string `json:"source"` // "llm" | "fallback"
}

// corpusStore — персистентный JSON-корпус сгенерированных документов.
type corpusStore struct {
	path string
	mu   sync.Mutex
	docs []GeneratedDoc
	seq  int
}

func corpusFilePath() string {
	if p := os.Getenv("MCP_CORPUS_FILE"); p != "" {
		return p
	}
	return defaultCorpusFile
}

// openCorpusStore открывает (или создаёт) файл корпуса.
func openCorpusStore() (*corpusStore, error) {
	cs := &corpusStore{path: corpusFilePath()}
	raw, err := os.ReadFile(cs.path)
	switch {
	case err == nil:
		var f struct {
			Docs []GeneratedDoc `json:"docs"`
			Seq  int            `json:"seq"`
		}
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("чтение корпуса %s: %w", cs.path, err)
		}
		cs.docs, cs.seq = f.Docs, f.Seq
	case os.IsNotExist(err):
		if dir := filepath.Dir(cs.path); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
		}
	default:
		return nil, err
	}
	return cs, nil
}

// add сохраняет документ и возвращает его с присвоенным id.
func (cs *corpusStore) add(doc GeneratedDoc) (GeneratedDoc, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.seq++
	doc.ID = fmt.Sprintf("gen-%03d", cs.seq)
	cs.docs = append(cs.docs, doc)

	b, err := json.MarshalIndent(map[string]any{
		"docs": cs.docs,
		"seq":  cs.seq,
	}, "", "  ")
	if err != nil {
		return GeneratedDoc{}, err
	}
	tmp := cs.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return GeneratedDoc{}, err
	}
	if err := os.Rename(tmp, cs.path); err != nil {
		return GeneratedDoc{}, err
	}
	return doc, nil
}

// GenerateDocument формирует документ по теме через LLM (общий пакет llm,
// читает LLM_API_KEY / LLM_BASE_URL / LLM_MODEL каскадом из .env). При любой
// ошибке (нет ключа, сеть, не JSON) возвращается детерминированный fallback.
// Документ добавляется в персистентный корпус.
func GenerateDocument(topic string) (GeneratedDoc, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return GeneratedDoc{}, fmt.Errorf("generate_document: topic обязателен")
	}

	doc := GeneratedDoc{Topic: topic, Source: "fallback"}
	if title, snippet, err := generateDocLLM(topic); err == nil {
		doc.Title, doc.Snippet, doc.Source = title, snippet, "llm"
	} else {
		fmt.Fprintf(os.Stderr, "mcp: LLM недоступен, fallback-документ (%v)\n", err)
		doc.Title = topic
		doc.Snippet = fmt.Sprintf("Документ по теме «%s» сформирован локально, так как LLM недоступен.", topic)
	}

	cs, err := openCorpusStore()
	if err != nil {
		return GeneratedDoc{}, fmt.Errorf("generate_document: корпус: %w", err)
	}
	added, err := cs.add(doc)
	if err != nil {
		return GeneratedDoc{}, fmt.Errorf("generate_document: сохранение: %w", err)
	}
	return added, nil
}

// generateDocLLM просит LLM составить title + snippet по теме.
// Ответ ожидается одним JSON-объектом (фрагмент извлекается устойчиво).
func generateDocLLM(topic string) (title, snippet string, err error) {
	client, err := llm.New()
	if err != nil {
		return "", "", err
	}
	prompt := "Тема: " + topic + "\n\n" +
		"Составь краткий справочный документ об этой теме. Ответь ОДНИМ JSON-объектом без пояснений: " +
		`{"title": "Заголовок до 60 символов", "snippet": "Одно-два предложения, до 200 символов"}`
	out, err := client.Chat([]llm.Message{{Role: "user", Content: prompt}}, nil)
	if err != nil {
		return "", "", err
	}
	m, err := parseJSONObject(out)
	if err != nil {
		return "", "", err
	}
	title, _ = m["title"].(string)
	snippet, _ = m["snippet"].(string)
	title, snippet = strings.TrimSpace(title), strings.TrimSpace(snippet)
	if title == "" || snippet == "" {
		return "", "", fmt.Errorf("LLM вернул пустые поля")
	}
	return title, snippet, nil
}

// parseJSONObject извлекает первый JSON-объект из текста ответа.
func parseJSONObject(s string) (map[string]any, error) {
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return nil, fmt.Errorf("в ответе нет JSON-объекта")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s[i:j+1]), &m); err != nil {
		return nil, err
	}
	return m, nil
}

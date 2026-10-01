package rag

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// chunkRecord — одна строка chunks.jsonl из индекса index_service.
type chunkRecord struct {
	Metadata struct {
		ChunkID string `json:"chunk_id"`
		Source  string `json:"source"`
		Title   string `json:"title"`
		Section string `json:"section"`
		Format  string `json:"format"`
	} `json:"metadata"`
	Text string `json:"text"`
}

// LoadChunks читает chunks.jsonl индекса выбранной стратегии.
func LoadChunks(indexDir, strategy string) ([]chunkRecord, error) {
	path := filepath.Join(indexDir, strategy, "chunks.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("chunks.jsonl (%s): %w", path, err)
	}
	defer f.Close()

	var out []chunkRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20) // строки до 4 МБ
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec chunkRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue // пропускаем битые строки
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// KeywordSearch — детерминированный ключевой поиск по чанкам (fallback без сети).
//
// Запрос токенизируется (в нижнем регистре, пунктуация срезается); каждый токен
// ищется в «заголовке поиска» = source + title + section + текст чанка.
// Чанк ранжируется по числу совпавших токенов; пустой запрос — первые topK как есть.
func KeywordSearch(chunks []chunkRecord, query string, topK int) []Chunk {
	if topK <= 0 {
		topK = 5
	}
	tokens := tokenize(query)

	type hit struct {
		chunk Chunk
		score int
	}
	var hits []hit
	for i, c := range chunks {
		hay := strings.ToLower(c.Metadata.Source + " " + c.Metadata.Title + " " +
			c.Metadata.Section + " " + c.Text)
		score := 0
		for _, tok := range tokens {
			if strings.Contains(hay, tok) {
				score++
			}
		}
		if score == 0 && len(tokens) > 0 {
			continue
		}
		hits = append(hits, hit{
			chunk: Chunk{
				Rank:    0, // проставим после сортировки
				ChunkID: c.Metadata.ChunkID,
				Source:  c.Metadata.Source,
				Title:   c.Metadata.Title,
				Section: c.Metadata.Section,
				Format:  c.Metadata.Format,
				Text:    c.Text,
			},
			score: score,
		})
		_ = i
	}

	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	if len(hits) > topK {
		hits = hits[:topK]
	}

	out := make([]Chunk, 0, len(hits))
	for rank, h := range hits {
		h.chunk.Rank = rank + 1
		h.chunk.Score = float64(h.score) // «сырые» очки совпадений
		out = append(out, h.chunk)
	}
	return out
}

// tokenize режет строку на токены: слова >= 2 символов без пунктуации.
func tokenize(s string) []string {
	fields := strings.Fields(strings.ToLower(s))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, ".,!?()«»\"'`:;-—/\\|")
		if len(f) >= 2 {
			out = append(out, f)
		}
	}
	return out
}

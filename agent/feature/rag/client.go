package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// searchRequest — тело POST /search микросервиса index_service.
type searchRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

// sidecarResponse — ответ микросервиса index_service.
type sidecarResponse struct {
	Engine   string  `json:"engine"`
	Strategy string  `json:"strategy"`
	Model    string  `json:"model"`
	Results  []Chunk `json:"results"`
}

// SearchSidecar делает семантический запрос к index_service/serve.py.
// Ошибка означает, что микросервис недоступен/ответил плохо — вызывающий код
// переключается на ключевой fallback (см. Retrieve).
func SearchSidecar(ctx context.Context, cfg Config, query string, topK int) (*Result, error) {
	body, err := json.Marshal(searchRequest{Query: query, TopK: topK})
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(cfg.SidecarURL, "/") + "/search"
	reqCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sidecar %s: %w", cfg.SidecarURL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // лимит 8 МБ
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sidecar %s вернул %d: %s", cfg.SidecarURL, resp.StatusCode, truncate(string(raw), 300))
	}

	var sr sidecarResponse
	if err := json.Unmarshal(raw, &sr); err != nil {
		return nil, fmt.Errorf("sidecar: разбор ответа: %w", err)
	}
	if len(sr.Results) == 0 {
		return &Result{Engine: orString(sr.Engine, "sidecar"), Strategy: orString(sr.Strategy, cfg.Strategy),
			Model: sr.Model, Chunks: nil}, nil
	}
	return &Result{Engine: orString(sr.Engine, "sidecar"), Strategy: orString(sr.Strategy, cfg.Strategy),
		Model: sr.Model, Chunks: sr.Results}, nil
}

func orString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- Этап 2: cross-encoder реранкер (POST /rerank в index_service) ------------

type rerankItem struct {
	ChunkID string `json:"chunk_id"`
	Text    string `json:"text"`
}

type rerankRequest struct {
	Query      string       `json:"query"`
	Candidates []rerankItem `json:"candidates"`
	Model      string       `json:"model,omitempty"`
}

type rerankResponse struct {
	Scores []float64 `json:"scores"`
}

// RerankSidecar пересортировывает чанки по скору cross-encoder модели из
// index_service (эндпоинт /rerank). Ошибка означает, что модель недоступна —
// вызывающий код переходит на heuristic-fallback.
func RerankSidecar(ctx context.Context, cfg Config, query string, chunks []Chunk) ([]Chunk, error) {
	if len(chunks) == 0 {
		return nil, nil
	}
	cands := make([]rerankItem, len(chunks))
	for i, c := range chunks {
		cands[i] = rerankItem{ChunkID: c.ChunkID, Text: c.Text}
	}
	body, err := json.Marshal(rerankRequest{Query: query, Candidates: cands, Model: cfg.RerankModel})
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(cfg.SidecarURL, "/") + "/rerank"
	reqCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("sidecar /rerank: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sidecar /rerank вернул %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var rr rerankResponse
	if err := json.Unmarshal(raw, &rr); err != nil {
		return nil, err
	}
	if len(rr.Scores) != len(chunks) {
		return nil, fmt.Errorf("sidecar /rerank: число скоров %d != число кандидатов %d", len(rr.Scores), len(chunks))
	}

	type scored struct {
		chunk Chunk
		score float64
	}
	sorted := make([]scored, len(chunks))
	for i, c := range chunks {
		sorted[i] = scored{chunk: c, score: rr.Scores[i]}
	}
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].score > sorted[b].score })

	out := make([]Chunk, len(sorted))
	for i, s := range sorted {
		s.chunk.Rank = i + 1
		s.chunk.Score = s.score
		out[i] = s.chunk
	}
	return out, nil
}

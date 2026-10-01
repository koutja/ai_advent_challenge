package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

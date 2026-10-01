package rag

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// loadChunksOnce — ленивая потокобезопасная загрузка chunks.jsonl.
func (r *Retriever) loadChunksOnce() ([]chunkRecord, error) {
	r.once.Do(func() {
		r.chunks, r.loadErr = LoadChunks(r.cfg.IndexDir, r.cfg.Strategy)
	})
	return r.chunks, r.loadErr
}

// Retrieve возвращает top-k чанков по запросу.
//
// Приоритет: семантический поиск через микросервис index_service (движок
// "sidecar"); при любой ошибке сети/сервиса — детерминированный ключевой поиск
// по chunks.jsonl (движок "keyword"). Выбранный движок фиксируется в Result.Engine,
// чтобы потребитель знал, насколько «глубоким» был поиск.
func (r *Retriever) Retrieve(ctx context.Context, query string) (*Result, error) {
	if !r.cfg.Enabled {
		return nil, fmt.Errorf("RAG выключен (config.rag.enabled=false)")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("пустой запрос")
	}

	// 1) Семантический ретрив через микросервис index_service.
	res, err := SearchSidecar(ctx, r.cfg, query, r.cfg.TopK)
	if err == nil {
		res.Engine = "sidecar"
		res.Strategy = r.cfg.Strategy
		return res, nil
	}
	fmt.Fprintf(os.Stderr, "rag: сайдкар недоступен (%v), ключевой fallback\n", err)

	// 2) Ключевой fallback по chunks.jsonl индекса.
	chunks, loadErr := r.loadChunksOnce()
	if loadErr != nil {
		return nil, fmt.Errorf("retrieve: сайдкар: %v; fallback: %w", err, loadErr)
	}
	return &Result{
		Engine:   "keyword",
		Strategy: r.cfg.Strategy,
		Chunks:   KeywordSearch(chunks, query, r.cfg.TopK),
	}, nil
}

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

// fetch выполняет поиск по индексу: сайдкар (семантика) или ключевой fallback.
// Возвращает top_k_fetch чанков БЕЗ этапа 2.
func (r *Retriever) fetch(ctx context.Context, query string) (*Result, error) {
	res, err := SearchSidecar(ctx, r.cfg, query, r.cfg.TopKFetch)
	if err == nil {
		res.Engine = "sidecar"
		res.Strategy = r.cfg.Strategy
		return res, nil
	}
	fmt.Fprintf(os.Stderr, "rag: сайдкар недоступен (%v), ключевой fallback\n", err)
	chunks, loadErr := r.loadChunksOnce()
	if loadErr != nil {
		return nil, fmt.Errorf("retrieve: сайдкар: %v; fallback: %w", err, loadErr)
	}
	return &Result{
		Engine:   "keyword",
		Strategy: r.cfg.Strategy,
		Chunks:   KeywordSearch(chunks, query, r.cfg.TopKFetch),
	}, nil
}

// rerankCross — обёртка над /rerank сайдкара для applyPipeline.
func (r *Retriever) rerankCross(ctx context.Context) func([]Chunk, string) ([]Chunk, error) {
	return func(chunks []Chunk, query string) ([]Chunk, error) {
		return RerankSidecar(ctx, r.cfg, query, chunks)
	}
}

// Retrieve — полный RAG-ретрив: поиск top_k_fetch → этап 2 (фильтр по MinScore →
// реранк → обрезка до top_k_keep). Статистика этапов — в Result.Stages.
func (r *Retriever) Retrieve(ctx context.Context, query string) (*Result, error) {
	if !r.cfg.Enabled {
		return nil, fmt.Errorf("RAG выключен (config.rag.enabled=false)")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("пустой запрос")
	}

	res, err := r.fetch(ctx, query)
	if err != nil {
		return nil, err
	}
	stage, err := applyPipeline(query, res.Chunks, r.cfg, r.rerankCross(ctx))
	if err != nil {
		return nil, err
	}
	res.Chunks = stage.Chunks
	res.Stages = stage.Stages
	return res, nil
}

// RetrieveRaw — «сырой» ретрив без этапа 2: первые top_k_keep чанков как есть
// (baseline для сравнения режимов в eval).
func (r *Retriever) RetrieveRaw(ctx context.Context, query string) (*Result, error) {
	if !r.cfg.Enabled {
		return nil, fmt.Errorf("RAG выключен (config.rag.enabled=false)")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("пустой запрос")
	}
	res, err := r.fetch(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(res.Chunks) > r.cfg.TopKKeep {
		res.Chunks = res.Chunks[:r.cfg.TopKKeep]
	}
	res.Stages = PipelineStages{
		Fetched:     r.cfg.TopKFetch,
		AfterFilter: len(res.Chunks),
		Kept:        len(res.Chunks),
		MinScore:    0,
		RerankMode:  RerankOff,
	}
	return res, nil
}

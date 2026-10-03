package rag

import (
	"sort"
)

// PipelineStages — статистика этапа 2 (фильтрация/реранкинг), для отчётов.
type PipelineStages struct {
	Fetched        int     // сколько чанков пришло из поиска (top_k_fetch)
	AfterFilter    int     // осталось после фильтра по MinScore
	Kept           int     // сколько попало в контекст (top_k_keep)
	MinScore       float64 // использованный порог
	RerankMode     string  // off | heuristic | cross_encoder
	RewriteApplied bool    // запрос был переписан перед поиском
	RewrittenQuery string  // переписанный запрос (если применялся)
}

// StageResult — результат прохода этапа 2.
type StageResult struct {
	Chunks []Chunk
	Stages PipelineStages
}

// heuristicScore — гибридный скор для реранкинга без дополнительных моделей:
//
//	0.7 * cosine-similarity (Score из индекса) + 0.3 * доля токенов запроса,
//	присутствующих в чанке (source+title+section+text).
//
// Позволяет «подтянуть» семантически близкие чанки с хорошим лексическим
// совпадением выше, чем просто близкие по вектору.
func heuristicScore(query string, c Chunk) float64 {
	sim := c.Score
	if sim < 0 {
		sim = 0
	}
	if sim > 1 {
		sim = 1
	}
	qSet := tokenSet(query)
	if len(qSet) == 0 {
		return sim
	}
	hay := tokenSet(c.Source + " " + c.Title + " " + c.Section + " " + c.Text)
	matched := 0
	for t := range qSet {
		if _, ok := hay[t]; ok {
			matched++
		}
	}
	lexical := float64(matched) / float64(len(qSet))
	return 0.7*sim + 0.3*lexical
}

func tokenSet(s string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, t := range tokenize(s) {
		out[t] = struct{}{}
	}
	return out
}

// heuristicSort сортирует чанки по гибридному скору (убывание).
func heuristicSort(chunks []Chunk, query string) {
	sort.SliceStable(chunks, func(i, j int) bool {
		return heuristicScore(query, chunks[i]) > heuristicScore(query, chunks[j])
	})
}

// applyPipeline выполняет этап 2 пайплайна:
//
//	поиск (top_k_fetch) → [фильтр по MinScore] → [реранк] → top_k_keep.
//
// rerankCross — опциональный внешний реранкер (cross-encoder через /rerank
// сайдкара); если он вернул ошибку или пустой результат — используется
// heuristic-сортировка (graceful fallback без падения пайплайна).
func applyPipeline(query string, chunks []Chunk, cfg Config, rerankCross func([]Chunk, string) ([]Chunk, error)) (StageResult, error) {
	st := PipelineStages{
		Fetched:    len(chunks),
		MinScore:   cfg.MinScore,
		RerankMode: cfg.RerankMode,
	}

	// 1) Фильтр по порогу similarity.
	kept := chunks
	if cfg.FilterEnabled {
		filtered := make([]Chunk, 0, len(chunks))
		for _, c := range chunks {
			if c.Score >= cfg.MinScore {
				filtered = append(filtered, c)
			}
		}
		kept = filtered
	}
	st.AfterFilter = len(kept)
	if len(kept) == 0 {
		return StageResult{Chunks: nil, Stages: st}, nil
	}

	// 2) Реранкинг.
	switch cfg.RerankMode {
	case RerankCrossEncoder:
		if rerankCross != nil {
			rk, err := rerankCross(kept, query)
			if err == nil && len(rk) > 0 {
				kept = rk
				break
			}
			// модель недоступна/ошибка → heuristic fallback
		}
		heuristicSort(kept, query)
	case RerankHeuristic:
		heuristicSort(kept, query)
	}

	// 3) Обрезка до top_k_keep.
	if len(kept) > cfg.TopKKeep {
		kept = kept[:cfg.TopKKeep]
	}
	for i := range kept {
		kept[i].Rank = i + 1
	}
	st.Kept = len(kept)
	return StageResult{Chunks: kept, Stages: st}, nil
}

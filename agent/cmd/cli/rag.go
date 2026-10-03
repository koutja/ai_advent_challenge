package main

import (
	stdctx "context"
	"errors"
	"fmt"
	"os"

	"agent"
	"agent/feature/memory"
	"agent/feature/rag"
)

// runRAGAnswer — одноразовый RAG-ответ: вопрос → поиск чанков в индексе
// index_service → контекст с источниками → LLM. Источники печатаются под ответом.
func runRAGAnswer(cfg *agent.Config, query string) {
	mem, err := memory.NewLayeredSQLite(cfg.HistoryFile, cfg.LongMemoryFile)
	if err != nil {
		die(err)
	}
	defer mem.Close()

	ag, err := agent.New(cfg, mem)
	if err != nil {
		die(err)
	}
	reply, err := ag.SayRAG(query)
	if err != nil {
		die(err)
	}

	fmt.Println(reply.Text)
	if reply.Engine != "" {
		fmt.Printf("\n[RAG: движок %s, источников: %d, токенов: %d]\n",
			reply.Engine, len(reply.Sources), tokensOrDash(reply))
		for i, s := range reply.Sources {
			fmt.Printf("  %d. %s | секция: %s | score=%.3f\n", i+1, s.Source, orText(s.Section, "—"), s.Score)
		}
	}
}

// runRAGEval — прогон контрольных вопросов (с RAG и без) и отчёт в results/.
func runRAGEval(cfg *agent.Config, judge bool) {
	mem, err := memory.NewLayeredSQLite(cfg.HistoryFile, cfg.LongMemoryFile)
	if err != nil {
		die(err)
	}
	defer mem.Close()

	ag, err := agent.New(cfg, mem)
	if err != nil {
		die(err)
	}
	if ag.Retriever() == nil {
		die(errors.New("RAG не настроен: добавьте секцию rag в config.json"))
	}

	qs, err := rag.LoadQuestions("rag_questions.json")
	if err != nil {
		die(err)
	}
	fmt.Printf("Контрольных вопросов: %d (LLM-as-judge: %v)\n", len(qs), judge)

	comps := rag.RunComparison(stdctx.Background(), ag.Client(), ag.Retriever(), qs, cfg.MaxTokens, judge)
	report := rag.RenderReport(comps, ag.Retriever().Config(), judge)

	if err := os.MkdirAll("results", 0o755); err != nil {
		die(err)
	}
	path := "results/rag_comparison.md"
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		die(err)
	}

	// Короткая сводка в консоль.
	fmt.Printf("\n%-5s %-14s %-14s %-8s\n", "id", "факты без RAG", "факты с RAG", "движок")
	var f0, f1 int
	for _, c := range comps {
		fmt.Printf("%-5s %d/%-12d %d/%-12d %-8s\n",
			c.Question.ID, c.NoRAG.FactHits, c.NoRAG.FactTotal,
			c.RAG.FactHits, c.RAG.FactTotal, orText(c.RAG.Engine, "—"))
		f0 += c.NoRAG.FactHits
		f1 += c.RAG.FactHits
	}
	fmt.Printf("\nПокрытие ожиданий: без RAG %d/%d, с RAG %d/%d\n", f0, totalFactsCli(comps), f1, totalFactsCli(comps))
	fmt.Printf("Полный отчёт: %s\n", path)
}

// runRAGModes — сравнение трёх режимов пайплайна (base / filter / full):
// фильтрация по MinScore, реранкинг и query rewrite. Отчёт — results/.
func runRAGModes(cfg *agent.Config) {
	mem, err := memory.NewLayeredSQLite(cfg.HistoryFile, cfg.LongMemoryFile)
	if err != nil {
		die(err)
	}
	defer mem.Close()

	ag, err := agent.New(cfg, mem)
	if err != nil {
		die(err)
	}
	if ag.Retriever() == nil {
		die(errors.New("RAG не настроен: добавьте секцию rag в config.json"))
	}

	qs, err := rag.LoadQuestions("rag_questions.json")
	if err != nil {
		die(err)
	}
	fmt.Printf("Контрольных вопросов: %d; режимы: base / filter / full\n", len(qs))

	comps := rag.RunModeComparison(stdctx.Background(), ag.Client(), ag.Retriever(), qs, cfg.MaxTokens)
	report := rag.RenderModesReport(comps, ag.Retriever().Config())

	if err := os.MkdirAll("results", 0o755); err != nil {
		die(err)
	}
	path := "results/rag_modes_comparison.md"
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		die(err)
	}

	// Консольная сводка.
	fmt.Printf("\n%-8s %-12s %-12s\n", "режим", "факты", "источники")
	for _, m := range rag.Modes {
		hits, total, src := 0, 0, 0
		for _, c := range comps {
			hits += c.Cases[m].FactHits
			total += c.Cases[m].FactTotal
			if c.Cases[m].SourceFound {
				src++
			}
		}
		fmt.Printf("%-8s %-3d/%-8d %-3d/%-8d\n", m, hits, total, src, len(qs))
	}
	fmt.Printf("\nПолный отчёт: %s\n", path)
}

// runRAGCitations — проверка обязательных источников и цитат (День 24):
// строгий формат Ответ/Источники/Цитаты + режим «не знаю» на внекорпусных вопросах.
// Отчёт: results/rag_citations.md. Опционально LLM-as-judge (grounded):
//
//	make run-rag-citations JUDGE=1
func runRAGCitations(cfg *agent.Config, judge bool) {
	mem, err := memory.NewLayeredSQLite(cfg.HistoryFile, cfg.LongMemoryFile)
	if err != nil {
		die(err)
	}
	defer mem.Close()

	ag, err := agent.New(cfg, mem)
	if err != nil {
		die(err)
	}
	if ag.Retriever() == nil {
		die(errors.New("RAG не настроен: добавьте секцию rag в config.json"))
	}

	qs, err := rag.LoadQuestions("rag_questions.json")
	if err != nil {
		die(err)
	}
	uqs, err := rag.LoadQuestions("rag_unknown_questions.json")
	if err != nil {
		die(err)
	}
	fmt.Printf("Контрольных вопросов: %d (+ %d внекорпусных на «не знаю»); judge: %v\n",
		len(qs), len(uqs), judge)

	ctx := stdctx.Background()
	cases := rag.RunCitationEval(ctx, ag.Client(), ag.Retriever(), qs, cfg.MaxTokens, judge)
	unknownCases := rag.RunCitationEval(ctx, ag.Client(), ag.Retriever(), uqs, cfg.MaxTokens, false)
	report := rag.RenderCitationReport(cases, unknownCases, ag.Retriever().Config(), judge)

	if err := os.MkdirAll("results", 0o755); err != nil {
		die(err)
	}
	path := "results/rag_citations.md"
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		die(err)
	}

	// Консольная сводка.
	fmt.Printf("\n%-5s %-7s %-14s %-16s %-9s %-8s\n",
		"id", "формат", "источники", "цитаты", "grounded", "top")
	var fmtOK, hasSrc, hasQt int
	for _, c := range cases {
		if c.FormatOK {
			fmtOK++
		}
		if c.HasSources {
			hasSrc++
		}
		if c.HasQuotes {
			hasQt++
		}
		fmt.Printf("%-5s %-7s %-3d/%-10d %-3d/%-12d %-9s %.3f\n",
			c.QuestionID, markStr(c.FormatOK), c.SourcesReal, c.SourcesTotal,
			c.QuotesVerbatim, c.QuotesTotal, orText(c.Grounded, "—"), c.TopScore)
	}
	fmt.Printf("\nФормат: %d/%d, источники: %d/%d, цитаты: %d/%d\n",
		fmtOK, len(cases), hasSrc, len(cases), hasQt, len(cases))
	fired := 0
	for _, c := range unknownCases {
		if c.Unknown {
			fired++
		}
	}
	fmt.Printf("«Не знаю» на внекорпусных: %d/%d\n", fired, len(unknownCases))
	fmt.Printf("Полный отчёт: %s\n", path)
}

func markStr(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

func tokensOrDash(r *agent.Reply) int {
	if r.Usage == nil || r.Usage.TotalTokens < 0 {
		return 0
	}
	return r.Usage.TotalTokens
}

func orText(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func totalFactsCli(comps []rag.Comparison) int {
	n := 0
	for _, c := range comps {
		n += c.NoRAG.FactTotal
	}
	return n
}

package main

// День 28 — локальный RAG: сравнение локальной и облачной моделей на одном
// retrieval-пайплайне. Retrieval ВСЕГДА локальный (сайдкар index_service с
// эмбеддером intfloat/multilingual-e5-small, без API-ключей и интернета);
// генерация идёт через два клиента:
//   - локальный:  LLM_LOCAL_BASE_URL / LLM_LOCAL_MODEL / LLM_LOCAL_API_KEY
//     (дефолты: Ollama http://localhost:11434/v1, qwen2.5:0.5b, ollama);
//   - облачный:   LLM_CLOUD_BASE_URL / LLM_CLOUD_API_KEY / LLM_CLOUD_MODEL
//     (заполняются Makefile-целью из корневого .env; если не заданы —
//     сравнение выполняется только на локальной модели).
//
// Метрики:
//   - качество:    покрытие ожидаемых фактов (FactCoverage), источник найден,
//     гейт «не знаю» на внекорпусных вопросах;
//   - скорость:    wall-время (retrieval + генерация), время вызова LLM,
//     токены и токенов/с;
//   - стабильность: несколько прогонов локальной модели — консистентность
//     покрытия фактов и разброс времени (min/avg/max).
//
// Отчёт: results/rag_local_vs_cloud.md.

import (
	stdctx "context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"agent"
	"agent/feature/memory"
	"agent/feature/rag"

	"aichallenge/llm"
)

const (
	defLocalBaseURL = "http://localhost:11434/v1"
	defLocalModel   = "qwen2.5:0.5b"
	defLocalAPIKey  = "ollama"
)

// ragRunCase — результат одного RAG-прогона одного вопроса.
type ragRunCase struct {
	Run         int
	FactHits    int
	FactTotal   int
	SourceFound bool
	Unknown     bool  // сработал гейт «не знаю» (без вызова LLM)
	WallMS      int64 // retrieval + генерация
	GenMS       int64 // только вызов LLM
	Completion  int   // сгенерировано токенов
	Err         string
	Answer      string
}

// ragClientEval — все прогоны одного клиента (модели).
type ragClientEval struct {
	Label           string                  // «локальная» / «облачная»
	Model           string                  // имя модели
	Runs            int                     // прогонов на вопрос
	Cases           map[string][]ragRunCase // id вопроса → прогоны
	UnknownRefusals int                     // корректных отказов «не знаю»
	UnknownTotal    int                     // внекорпусных вопросов всего
}

// newLocalClient — клиент локальной модели из LLM_LOCAL_* (с дефолтами Ollama).
func newLocalClient() (*llm.Client, error) {
	llm.LoadEnvCascade()
	base := os.Getenv("LLM_LOCAL_BASE_URL")
	if base == "" {
		base = defLocalBaseURL
	}
	model := os.Getenv("LLM_LOCAL_MODEL")
	if model == "" {
		model = defLocalModel
	}
	key := os.Getenv("LLM_LOCAL_API_KEY")
	if key == "" {
		key = defLocalAPIKey
	}
	return llm.NewWithConfig(base, key, model)
}

// newCloudClient — клиент облачной модели из LLM_CLOUD_*; nil, если не заданы.
func newCloudClient() (*llm.Client, error) {
	llm.LoadEnvCascade()
	base := os.Getenv("LLM_CLOUD_BASE_URL")
	key := os.Getenv("LLM_CLOUD_API_KEY")
	model := os.Getenv("LLM_CLOUD_MODEL")
	if base == "" && key == "" && model == "" {
		return nil, nil // облачная модель не настроена — это не ошибка
	}
	if base == "" || key == "" || model == "" {
		return nil, errors.New("заданы не все LLM_CLOUD_BASE_URL / LLM_CLOUD_API_KEY / LLM_CLOUD_MODEL")
	}
	return llm.NewWithConfig(base, key, model)
}

// runRAGLocalVsCloud — точка входа режима --rag-local-vs-cloud.
func runRAGLocalVsCloud(cfg *agent.Config, runs int) {
	if runs < 1 {
		runs = 1
	}

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
		die(errors.New("RAG не настроен: добавьте секцию rag в config (config.local.json)"))
	}
	retriever := ag.Retriever()

	qs, err := rag.LoadQuestions("rag_questions.json")
	if err != nil {
		die(err)
	}
	uqs, err := rag.LoadQuestions("rag_unknown_questions.json")
	if err != nil {
		die(err)
	}

	localClient, err := newLocalClient()
	if err != nil {
		die(err)
	}
	cloudClient, err := newCloudClient()
	if err != nil {
		die(err)
	}

	ctx := stdctx.Background()

	fmt.Printf("Локальный RAG (День 28). Retrieval: %s (индекс %s, движок локальный)\n",
		retriever.Config().SidecarURL, retriever.Config().IndexDir)
	fmt.Printf("Локальная модель: %s (прогонов на вопрос: %d)\n", localClient.Model(), runs)
	if cloudClient != nil {
		fmt.Printf("Облачная модель:  %s\n", cloudClient.Model())
	} else {
		fmt.Println("Облачная модель:  недоступна (LLM_CLOUD_* не заданы) — сравнение только локальной")
	}
	fmt.Printf("Контрольных вопросов: %d (+ %d внекорпусных)\n\n", len(qs), len(uqs))

	local := evalRAGClient(ctx, "локальная", localClient, retriever, qs, uqs, runs)

	var cloud *ragClientEval
	if cloudClient != nil {
		cloud = evalRAGClient(ctx, "облачная", cloudClient, retriever, qs, uqs, 1)
	}

	report := renderLocalVsCloudReport(local, cloud, retriever.Config())
	if err := os.MkdirAll("results", 0o755); err != nil {
		die(err)
	}
	path := "results/rag_local_vs_cloud.md"
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		die(err)
	}

	printLocalVsCloudSummary(local, cloud)
	fmt.Printf("\nПолный отчёт: %s\n", path)
}

// evalRAGClient прогоняет все вопросы runs раз (плюс внекорпусные на «не знаю»).
func evalRAGClient(ctx stdctx.Context, label string, client *llm.Client, retriever *rag.Retriever, qs, uqs []rag.Question, runs int) *ragClientEval {
	ev := &ragClientEval{Label: label, Model: client.Model(), Runs: runs, Cases: map[string][]ragRunCase{}}

	for _, q := range qs {
		ev.Cases[q.ID] = nil
		for r := 1; r <= runs; r++ {
			fmt.Printf("  [%s %s] прогон %d/%d…\n", label, q.ID, r, runs)
			ev.Cases[q.ID] = append(ev.Cases[q.ID], runRAGCase(ctx, client, retriever, q, r))
		}
	}

	// Гейт «не знаю»: внекорпусные вопросы, по одному прогону.
	for _, q := range uqs {
		ev.UnknownTotal++
		c := runRAGCase(ctx, client, retriever, q, 1)
		if c.Unknown {
			ev.UnknownRefusals++
		}
	}
	return ev
}

// runRAGCase — один RAG-запрос с замерами.
func runRAGCase(ctx stdctx.Context, client *llm.Client, retriever *rag.Retriever, q rag.Question, run int) ragRunCase {
	c := ragRunCase{Run: run}
	start := time.Now()
	ans, err := rag.AnswerRAGWithMode(ctx, client, retriever, q.Question, 0, rag.ModeFilter)
	c.WallMS = time.Since(start).Milliseconds()
	if err != nil {
		c.Err = err.Error()
		return c
	}
	c.Answer = ans.Text
	c.Unknown = ans.Unknown
	c.FactHits, c.FactTotal = rag.FactCoverage(ans.Text, q.Expectation)
	c.SourceFound = rag.SourceFound(ans.Sources, q.Sources)
	if ans.Usage != nil {
		c.GenMS = ans.Usage.Duration.Milliseconds()
		c.Completion = ans.Usage.CompletionTokens
	}
	return c
}

// --- агрегаты ---

func caseAvg(cases []ragRunCase, sel func(ragRunCase) float64) float64 {
	if len(cases) == 0 {
		return 0
	}
	var sum float64
	for _, c := range cases {
		sum += sel(c)
	}
	return sum / float64(len(cases))
}

func caseMinMax(cases []ragRunCase, sel func(ragRunCase) int64) (int64, int64) {
	if len(cases) == 0 {
		return 0, 0
	}
	mn, mx := sel(cases[0]), sel(cases[0])
	for _, c := range cases[1:] {
		v := sel(c)
		if v < mn {
			mn = v
		}
		if v > mx {
			mx = v
		}
	}
	return mn, mx
}

// hitsConsistent — одинаковое ли покрытие фактов во всех прогонах вопроса.
func hitsConsistent(cases []ragRunCase) bool {
	for _, c := range cases {
		if c.FactHits != cases[0].FactHits {
			return false
		}
	}
	return true
}

// totalFactsEv — суммарные факты по первым прогонам (среднее по прогонам).
func totalFactsEv(ev *ragClientEval) (hits, total float64) {
	for _, cases := range ev.Cases {
		if len(cases) == 0 {
			continue
		}
		hits += caseAvg(cases, func(c ragRunCase) float64 { return float64(c.FactHits) })
		total += float64(cases[0].FactTotal)
	}
	return hits, total
}

// avgWall, avgGen, tokPerSec — средние по всем вопросам/прогонам клиента.
func (ev *ragClientEval) speedStats() (avgWall, avgGen, tps float64) {
	var n int
	var walls, gens, comps float64
	for _, cases := range ev.Cases {
		for _, c := range cases {
			if c.Err != "" {
				continue
			}
			n++
			walls += float64(c.WallMS)
			gens += float64(c.GenMS)
			comps += float64(c.Completion)
		}
	}
	if n == 0 {
		return 0, 0, 0
	}
	avgWall, avgGen = walls/float64(n), gens/float64(n)
	if gens > 0 {
		tps = comps / (gens / 1000.0)
	}
	return avgWall, avgGen, tps
}

func (ev *ragClientEval) errCount() int {
	n := 0
	for _, cases := range ev.Cases {
		for _, c := range cases {
			if c.Err != "" {
				n++
			}
		}
	}
	return n
}

// sortedIDs — id вопросов в детерминированном порядке.
func sortedIDs(ev *ragClientEval) []string {
	ids := make([]string, 0, len(ev.Cases))
	for id := range ev.Cases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// --- вывод ---

func printLocalVsCloudSummary(local, cloud *ragClientEval) {
	fmt.Printf("\n%-12s %-16s %-10s %-10s %-12s %-10s\n", "модель", "факты (ср.)", "«не знаю»", "wall, мс", "gen, мс", "ток/с")
	printRow := func(ev *ragClientEval) {
		h, t := totalFactsEv(ev)
		aw, ag, tps := ev.speedStats()
		fmt.Printf("%-12s %-16s %-10s %-10.0f %-12.0f %-10.1f\n",
			ev.Label, fmt.Sprintf("%.1f/%.0f", h, t),
			fmt.Sprintf("%d/%d", ev.UnknownRefusals, ev.UnknownTotal), aw, ag, tps)
	}
	printRow(local)
	if cloud != nil {
		printRow(cloud)
	}
	if n := local.errCount(); n > 0 {
		fmt.Printf("ошибок запросов (локальная): %d\n", n)
	}
}

func renderLocalVsCloudReport(local, cloud *ragClientEval, cfg rag.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Локальный RAG: локальная vs облачная модель (День 28)\n\n")
	fmt.Fprintf(&b, "_Сформировано %s_\n\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "Retrieval — локальный сайдкар `%s` (индекс `%s`, стратегия %s), эмбеддер intfloat/multilingual-e5-small — без API-ключей.\n\n", cfg.SidecarURL, cfg.IndexDir, cfg.Strategy)

	fmt.Fprintf(&b, "## Модели\n\n| роль | модель | прогонов на вопрос |\n|---|---|---|\n")
	fmt.Fprintf(&b, "| локальная | %s | %d |\n", local.Model, local.Runs)
	if cloud != nil {
		fmt.Fprintf(&b, "| облачная | %s | %d |\n", cloud.Model, cloud.Runs)
	} else {
		fmt.Fprintf(&b, "| облачная | недоступна (LLM_CLOUD_* не заданы) | — |\n")
	}

	// --- Сводка: качество + скорость ---
	fmt.Fprintf(&b, "\n## Сводка (качество и скорость)\n\n")
	fmt.Fprintf(&b, "| модель | факты (ср. покрытие) | источник найден | «не знаю» | wall, мс (ср.) | gen, мс (ср.) | ток/с | ошибок |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|---|---|\n")
	row := func(ev *ragClientEval) {
		h, t := totalFactsEv(ev)
		aw, ag, tps := ev.speedStats()
		src := 0
		for _, cases := range ev.Cases {
			if len(cases) > 0 && cases[0].SourceFound {
				src++
			}
		}
		fmt.Fprintf(&b, "| %s (%s) | %.1f/%.0f | %d/%d | %d/%d | %.0f | %.0f | %.1f | %d |\n",
			ev.Label, ev.Model, h, t, src, len(ev.Cases),
			ev.UnknownRefusals, ev.UnknownTotal, aw, ag, tps, ev.errCount())
	}
	row(local)
	if cloud != nil {
		row(cloud)
	}

	// --- По вопросам ---
	fmt.Fprintf(&b, "\n## По вопросам\n\n| id | факты локальная | факты облачная | wall локальная, мс | wall облачная, мс |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|\n")
	for _, id := range sortedIDs(local) {
		lc := local.Cases[id]
		lHits := fmt.Sprintf("%.1f/%d", caseAvg(lc, func(c ragRunCase) float64 { return float64(c.FactHits) }), lc[0].FactTotal)
		lWall := fmt.Sprintf("%.0f", caseAvg(lc, func(c ragRunCase) float64 { return float64(c.WallMS) }))
		cHits, cWall := "—", "—"
		if cloud != nil {
			cc := cloud.Cases[id]
			cHits = fmt.Sprintf("%.1f/%d", caseAvg(cc, func(c ragRunCase) float64 { return float64(c.FactHits) }), cc[0].FactTotal)
			cWall = fmt.Sprintf("%.0f", caseAvg(cc, func(c ragRunCase) float64 { return float64(c.WallMS) }))
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", id, lHits, cHits, lWall, cWall)
	}

	// --- Стабильность локальной модели ---
	fmt.Fprintf(&b, "\n## Стабильность локальной модели (%d прогонов на вопрос)\n\n", local.Runs)
	fmt.Fprintf(&b, "| id | факты по прогонам | консистентно | wall min/avg/max, мс |\n")
	fmt.Fprintf(&b, "|---|---|---|---|\n")
	var consistent int
	for _, id := range sortedIDs(local) {
		cases := local.Cases[id]
		hits := make([]string, 0, len(cases))
		for _, c := range cases {
			hits = append(hits, fmt.Sprintf("%d/%d", c.FactHits, c.FactTotal))
		}
		ok := hitsConsistent(cases)
		if ok {
			consistent++
		}
		mn, mx := caseMinMax(cases, func(c ragRunCase) int64 { return c.WallMS })
		avg := caseAvg(cases, func(c ragRunCase) float64 { return float64(c.WallMS) })
		fmt.Fprintf(&b, "| %s | %s | %v | %.0f / %.0f / %d |\n",
			id, strings.Join(hits, ", "), ok, float64(mn), avg, mx)
	}
	fmt.Fprintf(&b, "\n**Консистентность покрытия фактов:** %d/%d вопросов.\n\n", consistent, len(local.Cases))

	// --- Детали ответов ---
	fmt.Fprintf(&b, "## Детали ответов\n\n")
	for _, id := range sortedIDs(local) {
		lc := local.Cases[id]
		fmt.Fprintf(&b, "### %s\n\n", id)
		if len(lc) > 0 {
			if lc[0].Err != "" {
				fmt.Fprintf(&b, "**Локальная (%s):** ошибка — %s\n\n", local.Model, lc[0].Err)
			} else {
				fmt.Fprintf(&b, "**Локальная (%s):**\n\n```\n%s\n```\n\n", local.Model, strings.TrimSpace(lc[0].Answer))
			}
		}
		if cloud != nil {
			cc := cloud.Cases[id]
			if len(cc) > 0 {
				if cc[0].Err != "" {
					fmt.Fprintf(&b, "**Облачная (%s):** ошибка — %s\n\n", cloud.Model, cc[0].Err)
				} else {
					fmt.Fprintf(&b, "**Облачная (%s):**\n\n```\n%s\n```\n\n", cloud.Model, strings.TrimSpace(cc[0].Answer))
				}
			}
		}
	}
	return b.String()
}

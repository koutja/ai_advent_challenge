package main

// День 29 — оптимизация локальной LLM под конкретный кейс: RAG-ответы агента
// по корпусу репозитория. Бенчмарк прогоняет ОДИН retrieval (локальный сайдкар
// index_service) через несколько вариантов генерации и сравнивает «до/после»:
//
//	V0 baseline  qwen2.5:0.5b        — дефолтные параметры, исходный промпт (как в Дне 28)
//	V1 параметры qwen2.5:0.5b-opt    — num_ctx 8192 + temperature 0.2 + top_p 0.8
//	                                   (запечены через ollama create + Modelfile),
//	                                   лимит max_tokens 256; промпт исходный
//	V2 +промпт   qwen2.5:0.5b-opt    — тот же промпт заменён на оптимизированный
//	V3 квант     qwen2.5:0.5b-q8-opt — то же, но база q8_0 (точнее веса, +35% размера)
//
// num_ctx — ключевой фикс: runtime-контекст Ollama по умолчанию мал, RAG-контекст
// (до 6000 символов ≈ 2–3к токенов) усекался и терял чанки-источники.
//
// Метрики: покрытие фактов, источник найден, «не знаю», cold start (первый
// запрос после принудительной выгрузки модели), wall/gen время, ток/с, ссылки
// [n] в ответе, размер модели и память (через /api/tags и /api/ps Ollama).
// Отчёт: results/rag_optimization.md.

import (
	stdctx "context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"agent"
	"agent/feature/memory"
	"agent/feature/rag"

	"aichallenge/llm"
)

const ollamaAPI = "http://localhost:11434"

// ragBaselinePrompt — исходный системный промпт пайплайна (копия
// ragSystemPrompt из feature/rag/answer.go) — для честного «до/после».
const ragBaselinePrompt = `Ты — ассистент, отвечающий на вопрос строго по предоставленному контексту из документов проекта. Правила:
1. Отвечай на русском, кратко и по делу.
2. Опирайся ТОЛЬКО на контекст ниже. Если в контексте нет ответа — так прямо и скажи («в контексте этого нет»), не додумывай.
3. После каждого факта указывай источник в квадратных скобках: [1], [2] и т.д.`

// ragOptimizedPrompt — оптимизированный промпт под маленькую модель:
// короче (меньше токенов и «шума»), директивнее, с жёсткой структурой и
// явным шаблоном отказа. Проверяется бенчмарком; если выигрывает —
// принимается в пайплайне (feature/rag/answer.go).
const ragOptimizedPrompt = `Ответь на вопрос строго по контексту ниже.
Правила:
1. Только факты из контекста. Если ответа в контексте нет — напиши ровно: «В контексте этого нет».
2. На русском, 1–3 предложения, без вступлений и повторов вопроса.
3. После каждого факта ставь номер источника в квадратных скобках: [1], [2].
4. В конце добавь строку «Источники:» и перечисли использованные номера.`

// ragVariant — один вариант оптимизации.
type ragVariant struct {
	Name      string // короткое имя в отчёте
	Model     string // имя модели в Ollama
	Prompt    string // системный промпт
	MaxTokens int    // 0 — не ограничивать
	Note      string // что менялось
}

var srcRefRe = regexp.MustCompile(`\[\d+\]`)

// ragVariantResult — агрегированный результат варианта.
type ragVariantResult struct {
	Variant       ragVariant
	SizeBytes     int64   // размер модели на диске (/api/tags)
	MemBytes      int64   // размер в RAM/VRAM после загрузки (/api/ps)
	ColdStartMS   int64   // первый запрос после выгрузки модели
	FactHits      float64 // среднее покрытие фактов на прогон
	FactTotal     float64 // всего ожиданий
	SourcesFound  int     // вопросов (по первому прогону), где источник найден
	QuestionCount int
	UnknownOK     int // корректных отказов «не знаю»
	UnknownTotal  int
	srcRefCount   int // ответов со ссылками [n] (для SrcRefRate)
	AvgWallMS     float64
	AvgGenMS      float64
	TokPerSec     float64
	SrcRefRate    float64 // доля ответов со ссылками [n]
	Consistency   float64 // доля вопросов с одинаковым покрытием по прогонам
	Errors        int
	PerQuestionID []string
	PerQuestion   map[string]float64 // id → средние факты по прогонам
}

// runRAGOptimize — точка входа режима --rag-optimize.
func runRAGOptimize(cfg *agent.Config, runs int) {
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
	rCfg := retriever.Config()

	qs, err := rag.LoadQuestions("rag_questions.json")
	if err != nil {
		die(err)
	}
	uqs, err := rag.LoadQuestions("rag_unknown_questions.json")
	if err != nil {
		die(err)
	}

	variants := []ragVariant{
		{Name: "V0 baseline", Model: "qwen2.5:0.5b", Prompt: ragBaselinePrompt, MaxTokens: 0,
			Note: "как есть (День 28): дефолтные параметры Ollama, исходный промпт"},
		{Name: "V1 параметры", Model: "qwen2.5:0.5b-opt", Prompt: ragBaselinePrompt, MaxTokens: 256,
			Note: "num_ctx 8192 + temp 0.2 + top_p 0.8 (Modelfile), max_tokens 256"},
		{Name: "V2 +промпт", Model: "qwen2.5:0.5b-opt", Prompt: ragOptimizedPrompt, MaxTokens: 256,
			Note: "V1 + оптимизированный промпт (короче, директивнее, шаблон отказа)"},
		{Name: "V3 квант q8", Model: "qwen2.5:0.5b-q8-opt", Prompt: ragOptimizedPrompt, MaxTokens: 256,
			Note: "V2 на базе q8_0 (точнее веса квантования)"},
	}

	ctx := stdctx.Background()
	fmt.Printf("Оптимизация локальной LLM (День 29). Retrieval: %s (индекс %s)\n", rCfg.SidecarURL, rCfg.IndexDir)
	fmt.Printf("Вопросов: %d (+ %d внекорпусных), прогонов на вопрос: %d, вариантов: %d\n\n",
		len(qs), len(uqs), runs, len(variants))

	results := make([]*ragVariantResult, 0, len(variants))
	for _, v := range variants {
		fmt.Printf("=== %s (%s) ===\n", v.Name, v.Model)
		res := benchVariant(ctx, v, retriever, rCfg, qs, uqs, runs)
		results = append(results, res)
		fmt.Printf("  факты %.1f/%.0f | «не знаю» %d/%d | cold %d мс | wall %.0f мс | ток/с %.1f\n\n",
			res.FactHits, res.FactTotal, res.UnknownOK, res.UnknownTotal, res.ColdStartMS, res.AvgWallMS, res.TokPerSec)
	}

	report := renderOptimizeReport(results, rCfg)
	if err := os.MkdirAll("results", 0o755); err != nil {
		die(err)
	}
	path := "results/rag_optimization.md"
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		die(err)
	}

	printOptimizeSummary(results)
	fmt.Printf("\nПолный отчёт: %s\n", path)
}

// benchVariant прогоняет один вариант по всем вопросам. Перед началом все
// загруженные модели Ollama выгружаются — первый запрос даёт честный cold start.
func benchVariant(ctx stdctx.Context, v ragVariant, retriever *rag.Retriever, rCfg rag.Config, qs, uqs []rag.Question, runs int) *ragVariantResult {
	client, err := llm.NewWithConfig(ollamaAPI+"/v1", "ollama", v.Model)
	if err != nil {
		die(err)
	}

	res := &ragVariantResult{
		Variant: v, PerQuestion: map[string]float64{},
		QuestionCount: len(qs),
	}
	res.SizeBytes = ollamaModelSize(v.Model)
	unloadAllOllama()

	var (
		wallSum, genSum, compSum float64
		nReq                     int
		hitsSum                  float64
		consistent               int
		firstReq                 = true
	)

	for _, q := range qs {
		hitsPerRun := make([]int, 0, runs)
		for r := 1; r <= runs; r++ {
			c := optAsk(ctx, client, retriever, rCfg, v, q)
			if firstReq {
				res.ColdStartMS = c.wallMS // загрузка модели + retrieval + генерация
				firstReq = false
			}
			if c.err != "" {
				res.Errors++
				continue
			}
			hitsPerRun = append(hitsPerRun, c.factHits)
			hitsSum += float64(c.factHits)
			wallSum += float64(c.wallMS)
			genSum += float64(c.genMS)
			compSum += float64(c.completion)
			nReq++
			if c.sourceFound && r == 1 {
				res.SourcesFound++
			}
			if c.srcRef {
				res.srcRefCount++
			}
		}
		if len(hitsPerRun) > 0 {
			res.PerQuestion[q.ID] = avgInt(hitsPerRun)
		}
		if len(hitsPerRun) > 1 && allEqual(hitsPerRun) {
			consistent++
		}
	}

	// «Не знаю»: внекорпусные вопросы, 1 прогон.
	for _, q := range uqs {
		res.UnknownTotal++
		c := optAsk(ctx, client, retriever, rCfg, v, q)
		if c.refused {
			res.UnknownOK++
		}
	}

	for _, q := range qs {
		res.FactTotal += float64(len(q.Expectation))
	}
	if runs > 0 {
		res.FactHits = hitsSum / float64(runs) // среднее покрытие на прогон
	}
	if nReq > 0 {
		res.AvgWallMS = wallSum / float64(nReq)
		res.AvgGenMS = genSum / float64(nReq)
		if genSum > 0 {
			res.TokPerSec = compSum / (genSum / 1000.0)
		}
		res.SrcRefRate = float64(res.srcRefCount) / float64(nReq)
	}
	if len(qs) > 0 {
		res.Consistency = float64(consistent) / float64(len(qs))
	}
	res.PerQuestionID = sortedOptKeys(res.PerQuestion)
	res.MemBytes = ollamaLoadedSize(v.Model)
	return res
}

// optCase — сырый результат одного запроса бенчмарка.
type optCase struct {
	factHits    int
	sourceFound bool
	srcRef      bool
	refused     bool
	wallMS      int64
	genMS       int64
	completion  int
	answer      string
	err         string
}

// optAsk — один RAG-запрос бенчмарка: retrieval → контекст → LLM с параметрами варианта.
func optAsk(ctx stdctx.Context, client *llm.Client, retriever *rag.Retriever, rCfg rag.Config, v ragVariant, q rag.Question) optCase {
	c := optCase{}
	start := time.Now()
	result, err := retriever.Retrieve(ctx, q.Question)
	if err != nil {
		c.err = err.Error()
		c.wallMS = time.Since(start).Milliseconds()
		return c
	}
	// Гейт «не знаю» — как в пайплайне (feature/rag/answer.go).
	top := 0.0
	if len(result.Chunks) > 0 {
		top = result.Chunks[0].Score
	}
	if result.Stages.Kept == 0 || top < rCfg.UnknownBelow {
		c.refused = true
		c.answer = rag.UnknownText
		c.wallMS = time.Since(start).Milliseconds()
		c.factHits, _ = rag.FactCoverage(c.answer, q.Expectation)
		return c
	}

	contextText := rag.BuildContext(result.Chunks, rCfg.MaxContextChars)
	msgs := []llm.Message{
		{Role: "system", Content: v.Prompt},
		{Role: "user", Content: contextText + "\n\nВопрос: " + q.Question},
	}
	opts := &llm.Options{}
	if v.MaxTokens > 0 {
		opts.MaxTokens = v.MaxTokens
	}
	res, err := client.ChatResult(msgs, opts)
	c.wallMS = time.Since(start).Milliseconds()
	if err != nil {
		c.err = err.Error()
		return c
	}
	c.answer = res.Text
	c.genMS = res.Duration.Milliseconds()
	c.completion = res.CompletionTokens
	c.factHits, _ = rag.FactCoverage(res.Text, q.Expectation)
	c.sourceFound = rag.SourceFound(result.Chunks, q.Sources)
	c.srcRef = srcRefRe.MatchString(res.Text)
	return c
}

// --- Ollama API helpers ---

type ollamaTagModel struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type ollamaPSModel struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	SizeVRAM int64  `json:"size_vram"`
}

// ollamaModelSize — размер модели на диске (/api/tags).
func ollamaModelSize(model string) int64 {
	resp, err := http.Get(ollamaAPI + "/api/tags")
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var out struct {
		Models []ollamaTagModel `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0
	}
	for _, m := range out.Models {
		if m.Name == model || strings.HasPrefix(m.Name, model+":") {
			return m.Size
		}
	}
	return 0
}

// ollamaLoadedSize — сколько модель занимает в RAM/VRAM сейчас (/api/ps).
func ollamaLoadedSize(model string) int64 {
	resp, err := http.Get(ollamaAPI + "/api/ps")
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var out struct {
		Models []ollamaPSModel `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0
	}
	for _, m := range out.Models {
		if m.Name == model || strings.HasPrefix(m.Name, model+":") {
			if m.SizeVRAM > 0 {
				return m.SizeVRAM
			}
			return m.Size
		}
	}
	return 0
}

// unloadAllOllama выгружает все загруженные модели (keep_alive=0),
// чтобы cold start следующего варианта был честным.
func unloadAllOllama() {
	resp, err := http.Get(ollamaAPI + "/api/ps")
	if err != nil {
		return
	}
	var out struct {
		Models []ollamaPSModel `json:"models"`
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if err != nil {
		return
	}
	for _, m := range out.Models {
		body := strings.NewReader(fmt.Sprintf(`{"model":%q,"keep_alive":0}`, m.Name))
		req, err := http.NewRequest(http.MethodPost, ollamaAPI+"/api/generate", body)
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 10 * time.Second}
		if r, err := client.Do(req); err == nil {
			_, _ = io.Copy(io.Discard, r.Body)
			r.Body.Close()
		}
	}
}

// --- агрегаты и вывод ---

func avgInt(xs []int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0
	for _, x := range xs {
		s += x
	}
	return float64(s) / float64(len(xs))
}

func allEqual(xs []int) bool {
	for _, x := range xs {
		if x != xs[0] {
			return false
		}
	}
	return true
}

func sortedOptKeys(m map[string]float64) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func mb(b int64) string {
	if b <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f МБ", float64(b)/1024/1024)
}

func (r *ragVariantResult) factTotalStr() string {
	return fmt.Sprintf("%.1f/%.0f", r.FactHits, r.FactTotal)
}

func printOptimizeSummary(results []*ragVariantResult) {
	fmt.Printf("\n%-16s %-12s %-9s %-10s %-10s %-8s %-8s\n",
		"вариант", "факты", "«не знаю»", "cold, мс", "wall, мс", "ток/с", "размер")
	for _, r := range results {
		fmt.Printf("%-16s %-12s %-9s %-10d %-9.0f %-8.1f %-8s\n",
			r.Variant.Name, r.factTotalStr(),
			fmt.Sprintf("%d/%d", r.UnknownOK, r.UnknownTotal),
			r.ColdStartMS, r.AvgWallMS, r.TokPerSec, mb(r.SizeBytes))
	}
	if len(results) > 1 {
		base, best := results[0], results[len(results)-1]
		fmt.Printf("\nΔ к baseline: факты %.1f → %.1f из %.0f | wall %.0f → %.0f мс | ток/с %.1f → %.1f | размер %s → %s\n",
			base.FactHits, best.FactHits, base.FactTotal,
			base.AvgWallMS, best.AvgWallMS, base.TokPerSec, best.TokPerSec,
			mb(base.SizeBytes), mb(best.SizeBytes))
	}
}

func renderOptimizeReport(results []*ragVariantResult, cfg rag.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Оптимизация локальной LLM под RAG-кейс (День 29)\n\n")
	fmt.Fprintf(&b, "_Сформировано %s_\n\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "Кейс: RAG-ответы агента по корпусу репозитория. Retrieval один для всех вариантов: сайдкар `%s`, индекс `%s` (%s), эмбеддер e5-small.\n\n", cfg.SidecarURL, cfg.IndexDir, cfg.Strategy)

	fmt.Fprintf(&b, "## Варианты\n\n| вариант | модель | размер на диске | память | что менялось |\n|---|---|---|---|---|\n")
	for _, r := range results {
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s |\n",
			r.Variant.Name, r.Variant.Model, mb(r.SizeBytes), mb(r.MemBytes), r.Variant.Note)
	}

	fmt.Fprintf(&b, "\n## Результаты (до/после)\n\n")
	fmt.Fprintf(&b, "| вариант | факты (ср.) | источников | «не знаю» | ссылки [n] | консистентность | cold, мс | wall, мс | gen, мс | ток/с | ошибок |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range results {
		fmt.Fprintf(&b, "| %s | %s | %d/%d | %d/%d | %.0f%% | %.0f%% | %d | %.0f | %.0f | %.1f | %d |\n",
			r.Variant.Name, r.factTotalStr(), r.SourcesFound, r.QuestionCount,
			r.UnknownOK, r.UnknownTotal, r.SrcRefRate*100, r.Consistency*100,
			r.ColdStartMS, r.AvgWallMS, r.AvgGenMS, r.TokPerSec, r.Errors)
	}

	fmt.Fprintf(&b, "\n## Покрытие фактов по вопросам (среднее по прогонам)\n\n| id |")
	for _, r := range results {
		fmt.Fprintf(&b, " %s |", r.Variant.Name)
	}
	fmt.Fprintf(&b, "\n|---|")
	for range results {
		fmt.Fprintf(&b, "---|")
	}
	fmt.Fprintf(&b, "\n")
	for _, id := range results[0].PerQuestionID {
		fmt.Fprintf(&b, "| %s |", id)
		for _, r := range results {
			fmt.Fprintf(&b, " %.1f |", r.PerQuestion[id])
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "\n## Выводы\n\n")
	if len(results) > 1 {
		base, best := results[0], results[len(results)-1]
		fmt.Fprintf(&b, "- Качество (факты): %.1f → %.1f из %.0f (%s → %s).\n",
			base.FactHits, best.FactHits, base.FactTotal, base.Variant.Name, best.Variant.Name)
		fmt.Fprintf(&b, "- Скорость: wall %.0f → %.0f мс, генерация %.1f → %.1f ток/с.\n",
			base.AvgWallMS, best.AvgWallMS, base.TokPerSec, best.TokPerSec)
		fmt.Fprintf(&b, "- Ресурсы: размер %s → %s, память %s → %s.\n",
			mb(base.SizeBytes), mb(best.SizeBytes), mb(base.MemBytes), mb(best.MemBytes))
	}
	return b.String()
}

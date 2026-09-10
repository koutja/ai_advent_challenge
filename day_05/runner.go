package main

import (
	"fmt"
	"os"
	"time"

	"aichallenge/llm"
)

// noValue — сентинел «данные недоступны». В отчёте выводится как «–».
const noValue = -1

// TierResult — итог прогона одного тира. Все числовые поля, которые не удалось
// получить, равны noValue (-1). Прогон никогда не падает: ошибка пишется в Err.
type TierResult struct {
	Tier             string
	Label            string
	ID               string
	Title            string
	Provider         string
	Elo              int
	BaseURL          string
	Text             string
	DurationMs       int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CostUSD          float64
	CostKnown        bool
	Err              string
	Skipped          bool
}

// runAll выполняет запрос на каждом выбранном тире и пишет итоги в лог.
func runAll(cfg *Config, tiers []TierCfg, cat map[string]ModelEntry, query string, log *logger) []*TierResult {
	out := make([]*TierResult, 0, len(tiers))
	for _, t := range tiers {
		log.logf("RUN tier=%s id=%q", t.Name, t.ID)
		r := runTier(cfg, t, cat, query)
		out = append(out, r)
		if r.Err != "" || r.Skipped {
			log.logf("RESULT tier=%s status=skip/error err=%q", r.Tier, r.Err)
		} else {
			log.logf("RESULT tier=%s dur_ms=%d prompt=%d completion=%d total=%d cost_known=%v cost_usd=%f",
				r.Tier, r.DurationMs, r.PromptTokens, r.CompletionTokens, r.TotalTokens, r.CostKnown, r.CostUSD)
		}
	}
	return out
}

// resolveBaseURL читает URL эндпоинта из переменной окружения, имя которой
// указано в конфиге (cfg.BaseURL, по умолчанию LLM_BASE_URL). Тировый
// t.BaseURL, если задан, перекрывает глобальный. Возвращает пустую строку,
// если переменная не задана — тогда llm подставит дефолтный URL.
func resolveBaseURL(cfg *Config, t TierCfg) string {
	name := cfg.BaseURL
	if name == "" {
		name = "LLM_BASE_URL"
	}
	if t.BaseURL != "" {
		name = t.BaseURL
	}
	return os.Getenv(name)
}

// runTier прогоняет один тир. Ошибки не фатальны — они фиксируются в TierResult,
// чтобы остальные тиры продолжили работу.
func runTier(cfg *Config, t TierCfg, cat map[string]ModelEntry, query string) *TierResult {
	res := &TierResult{
		Tier:             t.Name,
		Label:            t.Label,
		ID:               t.ID,
		PromptTokens:     noValue,
		CompletionTokens: noValue,
		TotalTokens:      noValue,
		DurationMs:       noValue,
	}
	if m, ok := cat[t.ID]; ok {
		res.Title = m.Title
		res.Provider = m.Provider
		res.Elo = m.Elo
	}

	baseURL := resolveBaseURL(cfg, t)
	envKey := cfg.EnvKey
	if t.EnvKey != "" {
		envKey = t.EnvKey
	}
	res.BaseURL = baseURL

	if t.ID == "" {
		res.Skipped = true
		res.Err = "модель не настроена (пустой id в config.json)"
		return res
	}

	apiKey := os.Getenv(envKey)
	if apiKey == "" && envKey != "LLM_API_KEY" {
		apiKey = os.Getenv("LLM_API_KEY")
	}
	if apiKey == "" {
		if envKey == "LLM_API_KEY" {
			res.Err = "не задан API-ключ (LLM_API_KEY)"
		} else {
			res.Err = fmt.Sprintf("не задан API-ключ (переменная %q или LLM_API_KEY)", envKey)
		}
		return res
	}

	client, err := llm.NewWithConfig(baseURL, apiKey, t.ID)
	if err != nil {
		res.Err = err.Error()
		return res
	}

	start := time.Now()
	out, cerr := client.ChatResult([]llm.Message{{Role: "user", Content: query}}, nil)
	res.DurationMs = int(time.Since(start).Milliseconds())

	if cerr != nil {
		res.Err = cerr.Error()
		return res
	}

	res.PromptTokens = out.PromptTokens
	res.CompletionTokens = out.CompletionTokens
	res.TotalTokens = out.TotalTokens
	res.Text = out.Text

	if p, ok := priceFor(cat, t.ID); ok {
		pt, ct := out.PromptTokens, out.CompletionTokens
		if pt < 0 {
			pt = 0
		}
		if ct < 0 {
			ct = 0
		}
		res.CostUSD = (float64(pt)*p.Input + float64(ct)*p.Output) / 1e6
		res.CostKnown = true
	}
	return res
}

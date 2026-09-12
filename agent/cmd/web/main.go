// Команда web — опциональный HTTP-интерфейс к ядру агента.
//
// Запуск:  go run ./cmd/web
// Открыть: http://<web_addr>   (по умолчанию 127.0.0.1:8080)
//
// Эндпоинты:
//
//	GET  /            статическая страница чата (web/index.html)
//	POST /chat        {"message":"..."} -> {"reply":"...", "usage":{...}}
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"agent"
)

var (
	ag     *agent.Agent
	appCfg *agent.Config
)

type chatReq struct {
	Message string `json:"message"`
}

type strategyReq struct {
	Strategy string `json:"strategy"`
}

type strategyResp struct {
	Current   string   `json:"current"`
	Available []string `json:"available"`
}

type compareResp struct {
	Results  []agent.StrategyResult `json:"results"`
	Analysis string                 `json:"analysis"`
}

type usageView struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type statsView struct {
	Model          string  `json:"model,omitempty"`
	HistoryTokens  int     `json:"history_tokens,omitempty"`
	RequestTokens  int     `json:"request_tokens,omitempty"`
	ResponseTokens int     `json:"response_tokens,omitempty"`
	ContextWindow  int     `json:"context_window,omitempty"`
	CostUSD        float64 `json:"cost_usd,omitempty"`
	CostKnown      bool    `json:"cost_known,omitempty"`
}

type chatResp struct {
	Reply  string                `json:"reply"`
	Usage  *usageView            `json:"usage,omitempty"`
	Stats  *statsView            `json:"stats,omitempty"`
	Events []agent.StrategyEvent `json:"events,omitempty"`
}

func main() {
	cfgPath := flag.String("config", "config.json", "путь к config.json")
	addr := flag.String("addr", "", "адрес веб-сервера (по умолчанию — из config.json)")
	webDir := flag.String("web-dir", "", "папка со статикой (по умолчанию — из config.json)")
	strategy := flag.String("strategy", "", "стратегия контекста по умолчанию: window|facts|branch")
	flag.Parse()

	cfg, err := agent.LoadConfig(*cfgPath)
	if err != nil {
		die(err)
	}
	appCfg = cfg
	if *addr == "" {
		*addr = cfg.WebAddr
	}
	if *webDir == "" {
		*webDir = cfg.WebDir
	}

	// Этап 2: история сохраняется в SQLite (cfg.HistoryFile) и переживает перезапуск.
	mem, err := agent.NewSQLiteMemory(cfg.HistoryFile)
	if err != nil {
		die(err)
	}
	defer mem.Close()

	ag, err = agent.New(cfg, mem)
	if err != nil {
		die(err)
	}

	// Стратегия по умолчанию из флага --strategy (перекрывает config.json).
	if *strategy != "" {
		if err := ag.SetStrategy(*strategy); err != nil {
			die(err)
		}
	}

	http.HandleFunc("/chat", handleChat)
	http.HandleFunc("/chat/stream", handleChatStream)
	http.HandleFunc("/history", handleHistory)
	http.HandleFunc("/reset", handleReset)
	http.HandleFunc("/strategy", handleStrategy)
	http.HandleFunc("/compare", handleCompare)
	http.HandleFunc("/compare/stream", handleCompareStream)
	http.Handle("/", http.FileServer(http.Dir(*webDir)))

	fmt.Printf("Web-интерфейс агента: http://%s (стратегия: %s)\n", *addr, strategyLabel(ag))
	log.Fatal(http.ListenAndServe(*addr, nil))
}

// strategyLabel возвращает имя активной стратегии или legacy/off.
func strategyLabel(ag *agent.Agent) string {
	if n := ag.StrategyName(); n != "" {
		return n
	}
	if ag.CompressionEnabled() {
		return "legacy-сжатие"
	}
	return "off"
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "ожидается POST", http.StatusMethodNotAllowed)
		return
	}
	var req chatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "неверный JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	reply, err := ag.Say(req.Message)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	resp := chatResp{Reply: reply.Text}
	if len(reply.Events) > 0 {
		resp.Events = reply.Events
	}
	if reply.Usage != nil && reply.Usage.TotalTokens >= 0 {
		resp.Usage = &usageView{
			PromptTokens:     reply.Usage.PromptTokens,
			CompletionTokens: reply.Usage.CompletionTokens,
			TotalTokens:      reply.Usage.TotalTokens,
		}
	}
	if reply.Stats != nil {
		resp.Stats = &statsView{
			Model:          reply.Stats.Model,
			HistoryTokens:  reply.Stats.HistoryTokens,
			RequestTokens:  reply.Stats.RequestTokens,
			ResponseTokens: reply.Stats.ResponseTokens,
			ContextWindow:  reply.Stats.ContextWindow,
			CostUSD:        reply.Stats.CostUSD,
			CostKnown:      reply.Stats.CostKnown,
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleChatStream: GET /chat/stream?message=... — стримит ход чата через SSE.
// События стратегии (Build/Observe) шлются сразу по мере работы Say(), затем —
// событие reply с текстом ответа. Даёт живой лог стратегий на клиенте.
func handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "ожидается GET", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "стриминг не поддерживается", http.StatusInternalServerError)
		return
	}
	msg := strings.TrimSpace(r.URL.Query().Get("message"))
	if msg == "" {
		http.Error(w, "пустое сообщение", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	send := func(ev, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, data)
		flusher.Flush()
	}

	// Передаём события стратегии в стрим сразу, как только они возникают.
	ag.SetOnEvent(func(se agent.StrategyEvent) {
		b, _ := json.Marshal(se)
		send("strategy", string(b))
	})
	defer ag.SetOnEvent(nil)

	reply, err := ag.Say(msg)
	if err != nil {
		eb, _ := json.Marshal(map[string]string{"error": err.Error()})
		send("chat_error", string(eb))
		return
	}

	resp := chatResp{Reply: reply.Text}
	if len(reply.Events) > 0 {
		resp.Events = reply.Events
	}
	if reply.Usage != nil && reply.Usage.TotalTokens >= 0 {
		resp.Usage = &usageView{
			PromptTokens:     reply.Usage.PromptTokens,
			CompletionTokens: reply.Usage.CompletionTokens,
			TotalTokens:      reply.Usage.TotalTokens,
		}
	}
	if reply.Stats != nil {
		resp.Stats = &statsView{
			Model:          reply.Stats.Model,
			HistoryTokens:  reply.Stats.HistoryTokens,
			RequestTokens:  reply.Stats.RequestTokens,
			ResponseTokens: reply.Stats.ResponseTokens,
			ContextWindow:  reply.Stats.ContextWindow,
			CostUSD:        reply.Stats.CostUSD,
			CostKnown:      reply.Stats.CostKnown,
		}
	}
	b, _ := json.Marshal(resp)
	send("reply", string(b))
}

// handleReset: POST — очистить историю и состояние стратегии (новый диалог).
func handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "ожидается POST", http.StatusMethodNotAllowed)
		return
	}
	if err := ag.ResetContext(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleHistory возвращает всю сохранённую историю диалога в JSON-виде,
// чтобы страница могла показать её при загрузке (после перезапуска сервера).
func handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "ожидается GET", http.StatusMethodNotAllowed)
		return
	}
	hist, err := ag.Memory().Load()
	if err != nil {
		http.Error(w, "ошибка чтения истории: "+err.Error(), http.StatusInternalServerError)
		return
	}
	type histMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	msgs := make([]histMsg, 0, len(hist))
	for _, m := range hist {
		msgs = append(msgs, histMsg{Role: m.Role, Content: m.Content})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(msgs)
}

// handleStrategy: GET — вернуть текущую стратегию и список доступных;
// POST {"strategy":"window|facts|branch"} — переключить активную стратегию.
func handleStrategy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	resp := strategyResp{
		Current:   ag.StrategyName(),
		Available: agent.StrategyNames,
	}

	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "ожидается GET или POST", http.StatusMethodNotAllowed)
		return
	}

	var req strategyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "неверный JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := ag.SetStrategy(req.Strategy); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp.Current = ag.StrategyName()
	_ = json.NewEncoder(w).Encode(resp)
}

// handleCompare: POST — прогнать сценарий на всех стратегиях (свежие агенты),
// собрать метрики и вернуть результаты + текстовый анализ.
func handleCompare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "ожидается POST", http.StatusMethodNotAllowed)
		return
	}
	results, err := agent.CompareStrategies(appCfg, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp := compareResp{Results: results, Analysis: agent.AnalyzeStrategies(results)}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleCompareStream: GET — стримит прогресс сравнения стратегий через
// Server-Sent Events (SSE): событие progress на каждый ход, в конце — done
// с результатами и анализом. Нужно для живой прозрачности UI.
func handleCompareStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "ожидается GET", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "стриминг не поддерживается", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	send := func(ev, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, data)
		flusher.Flush()
	}

	send("start", "{}")

	results, err := agent.CompareStrategies(appCfg, func(p agent.CompareProgress) {
		b, _ := json.Marshal(p)
		send("progress", string(b))
	})
	if err != nil {
		eb, _ := json.Marshal(map[string]string{"error": err.Error()})
		send("compare_error", string(eb))
		return
	}

	resp := compareResp{Results: results, Analysis: agent.AnalyzeStrategies(results)}
	b, _ := json.Marshal(resp)
	send("done", string(b))
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	os.Exit(1)
}

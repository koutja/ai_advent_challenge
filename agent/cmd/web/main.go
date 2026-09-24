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
	"agent/feature/context"
	"agent/feature/invariants"
	"agent/feature/memory"
	"agent/feature/profile"
	"agent/feature/task"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
	Reply  string                  `json:"reply"`
	Usage  *usageView              `json:"usage,omitempty"`
	Stats  *statsView              `json:"stats,omitempty"`
	Events []context.StrategyEvent `json:"events,omitempty"`
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

	// Многослойная память: short- и long-слои в SQLite (переживают перезапуск),
	// working — в RAM (жизнь одной задачи).
	mem, err := memory.NewLayeredSQLite(cfg.HistoryFile, cfg.LongMemoryFile)
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
	http.HandleFunc("/chat/execute", handleChatExecute)
	http.HandleFunc("/chat/validate", handleChatValidate)
	http.HandleFunc("/chat/rework", handleChatRework)
	http.HandleFunc("/chat/finalize", handleChatFinalize)
	http.HandleFunc("/history", handleHistory)
	http.HandleFunc("/reset", handleReset)
	http.HandleFunc("/strategy", handleStrategy)
	http.HandleFunc("/memory", handleMemory)
	http.HandleFunc("/remember", handleRemember)
	http.HandleFunc("/newtask", handleNewTask)
	http.HandleFunc("/task", handleTask)
	http.HandleFunc("/profile", handleProfile)
	http.HandleFunc("/profile/use", handleProfileUse)
	http.HandleFunc("/profile/template", handleProfileTemplate)
	http.HandleFunc("/invariants", handleInvariants)
	http.HandleFunc("/invariants/delete", handleInvariantDelete)
	http.HandleFunc("/compare", handleCompare)
	http.HandleFunc("/compare/stream", handleCompareStream)
	http.HandleFunc("/favicon.ico", handleFavicon)
	http.Handle("/", http.FileServer(http.Dir(*webDir)))

	fmt.Printf("Web-интерфейс агента: http://%s (стратегия: %s)\n", *addr, strategyLabel(ag))
	log.Fatal(http.ListenAndServe(*addr, nil))
}

// handleFavicon отдаёт заглушку favicon (favicon.svg), чтобы браузеры и агенты
// не получали 404 на /favicon.ico.
func handleFavicon(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(filepath.Join(appCfg.WebDir, "favicon.svg"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
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
		http.Error(w, "неверный JSON: "+safeError(err), http.StatusBadRequest)
		return
	}

	reply, err := ag.Say(req.Message)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": safeError(err)})
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
	ag.SetOnEvent(func(se context.StrategyEvent) {
		b, _ := json.Marshal(se)
		send("strategy", string(b))
	})
	defer ag.SetOnEvent(nil)

	reply, err := ag.Say(msg)
	if err != nil {
		eb, _ := json.Marshal(map[string]string{"error": safeError(err)})
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

// handleChatExecute: GET /chat/execute — стримит исполнение плана задачи через
// SSE. Пока задача на этапе execution и есть шаги, по очереди выполняет каждый
// шаг плана через LLM (ExecuteCurrentStep) и шлёт событие "step" {step,total,text};
// в конце — событие "done". Каждый шаг появляется в чате как сообщение.
func handleChatExecute(w http.ResponseWriter, r *http.Request) {
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

	ag.SetOnEvent(func(se context.StrategyEvent) {
		b, _ := json.Marshal(se)
		send("strategy", string(b))
	})
	defer ag.SetOnEvent(nil)

	st := ag.TaskState()
	if !st.IsActive() || st.Stage != task.StageExecution {
		eb, _ := json.Marshal(map[string]string{"error": "нет активной задачи на этапе исполнения"})
		send("chat_error", string(eb))
		return
	}
	total := len(task.PlanSteps(st.Plan))

	// Выполняем шаги по очереди, пока задача активна на этапе execution.
	for {
		cur := ag.TaskState()
		if !cur.IsActive() || cur.Stage != task.StageExecution || cur.Paused {
			break
		}
		if cur.Step > total {
			break // все шаги плана выполнены
		}
		stepNo := cur.Step
		text, err := ag.ExecuteCurrentStep()
		if err != nil {
			eb, _ := json.Marshal(map[string]string{"error": safeError(err)})
			send("chat_error", string(eb))
			break
		}
		b, _ := json.Marshal(map[string]interface{}{"step": stepNo, "total": total, "text": text})
		send("step", string(b))
	}
	send("done", "{}")
}

// sseSendHeaders настраивает общие SSE-заголовки.
func sseSendHeaders(w http.ResponseWriter) (http.Flusher, bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	f, ok := w.(http.Flusher)
	return f, ok
}

// handleChatValidate: GET /chat/validate — запускает валидацию результата через
// LLM и шлёт событие "verdict" {verdict, review}. Авто-вызывается при входе на
// этап валидации; также доступно вручную (/validate).
func handleChatValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "ожидается GET", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := sseSendHeaders(w)
	if !ok {
		http.Error(w, "стриминг не поддерживается", http.StatusInternalServerError)
		return
	}
	send := func(ev, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, data)
		flusher.Flush()
	}
	ag.SetOnEvent(func(se context.StrategyEvent) {
		b, _ := json.Marshal(se)
		send("strategy", string(b))
	})
	defer ag.SetOnEvent(nil)

	verdict, review, err := ag.ValidateWork()
	if err != nil {
		eb, _ := json.Marshal(map[string]string{"error": safeError(err)})
		send("chat_error", string(eb))
		return
	}
	b, _ := json.Marshal(map[string]string{"verdict": string(verdict), "review": review})
	send("verdict", string(b))
}

// handleChatRework: GET /chat/rework?reason=... — запускает доработку через LLM,
// результат (указания по исправлению) шлёт событием "rework".
func handleChatRework(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "ожидается GET", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := sseSendHeaders(w)
	if !ok {
		http.Error(w, "стриминг не поддерживается", http.StatusInternalServerError)
		return
	}
	send := func(ev, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, data)
		flusher.Flush()
	}
	reason := strings.TrimSpace(r.URL.Query().Get("reason"))
	err := ag.ReworkWithLLM(reason)
	if err != nil {
		eb, _ := json.Marshal(map[string]string{"error": safeError(err)})
		send("chat_error", string(eb))
		return
	}
	send("rework", "{\"status\":\"ok\"}")
}

// handleChatFinalize: GET /chat/finalize — собирает финальную сводку по задаче
// через LLM и шлёт её событием "summary". Авто-вызывается после accept (done).
func handleChatFinalize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "ожидается GET", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := sseSendHeaders(w)
	if !ok {
		http.Error(w, "стриминг не поддерживается", http.StatusInternalServerError)
		return
	}
	send := func(ev, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, data)
		flusher.Flush()
	}
	ag.SetOnEvent(func(se context.StrategyEvent) {
		b, _ := json.Marshal(se)
		send("strategy", string(b))
	})
	defer ag.SetOnEvent(nil)

	summary, err := ag.SummarizeDone()
	if err != nil {
		eb, _ := json.Marshal(map[string]string{"error": safeError(err)})
		send("chat_error", string(eb))
		return
	}
	b, _ := json.Marshal(map[string]string{"summary": summary})
	send("summary", string(b))
}

// handleReset: POST — очистить историю и состояние стратегии (новый диалог).
func handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "ожидается POST", http.StatusMethodNotAllowed)
		return
	}
	if err := ag.ResetContext(); err != nil {
		http.Error(w, safeError(err), http.StatusInternalServerError)
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
		http.Error(w, "ошибка чтения истории: "+safeError(err), http.StatusInternalServerError)
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
		Available: context.StrategyNames,
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
		http.Error(w, "неверный JSON: "+safeError(err), http.StatusBadRequest)
		return
	}
	if err := ag.SetStrategy(req.Strategy); err != nil {
		http.Error(w, safeError(err), http.StatusBadRequest)
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
		http.Error(w, safeError(err), http.StatusInternalServerError)
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
		eb, _ := json.Marshal(map[string]string{"error": safeError(err)})
		send("compare_error", string(eb))
		return
	}

	resp := compareResp{Results: results, Analysis: agent.AnalyzeStrategies(results)}
	b, _ := json.Marshal(resp)
	send("done", string(b))
}

// memoryView — JSON-представление трёх слоёв памяти.
type memoryView struct {
	Short   int                `json:"short_messages"`
	Working map[string]string  `json:"working"`
	Long    []memory.LongEntry `json:"long"`
}

// handleMemory: GET — вернуть снапшот трёх слоёв памяти (для UI).
func handleMemory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "ожидается GET", http.StatusMethodNotAllowed)
		return
	}
	m := ag.Memories()
	hist, _ := m.Short().Load()
	long, _ := m.Long().All()
	resp := memoryView{Short: len(hist), Working: m.Working().All(), Long: long}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

// invariantsView — представление инвариантов для UI.
type invariantsView struct {
	Enabled    bool                   `json:"enabled"`
	Invariants []invariants.Invariant `json:"invariants"`
}

// handleInvariants: GET — список активных инвариантов; POST — добавить правило.
func handleInvariants(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		view := invariantsView{Enabled: ag.Invariants() != nil}
		if ag.Invariants() != nil {
			view.Invariants = ag.Invariants().Active()
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(view)
	case http.MethodPost:
		var req struct {
			Category string `json:"category"`
			Title    string `json:"title"`
			Text     string `json:"text"`
			Severity string `json:"severity"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "неверный JSON: "+safeError(err), http.StatusBadRequest)
			return
		}
		if req.Title == "" || req.Text == "" {
			http.Error(w, "нужны title и text", http.StatusBadRequest)
			return
		}
		if ag.Invariants() == nil {
			http.Error(w, "инварианты не настроены (invariants_file пуст)", http.StatusBadRequest)
			return
		}
		sev := req.Severity
		if sev != invariants.SeverityHard && sev != invariants.SeveritySoft {
			sev = invariants.SeverityHard
		}
		inv := invariants.Invariant{
			ID:       fmt.Sprintf("inv_%d", time.Now().UnixNano()),
			Category: req.Category,
			Title:    req.Title,
			Text:     req.Text,
			Severity: sev,
			Enabled:  true,
		}
		if err := ag.Invariants().Add(inv); err != nil {
			http.Error(w, safeError(err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "id": inv.ID})
	default:
		http.Error(w, "ожидается GET или POST", http.StatusMethodNotAllowed)
	}
}

// handleInvariantDelete: POST {"id":"..."} — удалить инвариант по ID.
func handleInvariantDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "ожидается POST", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "неверный JSON: "+safeError(err), http.StatusBadRequest)
		return
	}
	if ag.Invariants() == nil {
		http.Error(w, "инварианты не настроены (invariants_file пуст)", http.StatusBadRequest)
		return
	}
	if err := ag.Invariants().Delete(req.ID); err != nil {
		http.Error(w, safeError(err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

type rememberReq struct {
	Kind  string `json:"kind"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// handleRemember: POST — явно сохранить запись в долговременный слой памяти.
func handleRemember(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "ожидается POST", http.StatusMethodNotAllowed)
		return
	}
	var req rememberReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "неверный JSON: "+safeError(err), http.StatusBadRequest)
		return
	}
	if req.Kind == "" || req.Key == "" {
		http.Error(w, "нужны kind и key", http.StatusBadRequest)
		return
	}
	if err := ag.Remember(req.Kind, req.Key, req.Value); err != nil {
		http.Error(w, safeError(err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleNewTask: POST — начать новую задачу: очистить короткий и рабочий слои,
// долговременную память сохранить.
func handleNewTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "ожидается POST", http.StatusMethodNotAllowed)
		return
	}
	if err := ag.ResetContext(); err != nil {
		http.Error(w, safeError(err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// taskView — JSON-представление состояния задачи для UI.
type taskView struct {
	Enabled      bool     `json:"enabled"`
	Goal         string   `json:"goal,omitempty"`
	Stage        string   `json:"stage,omitempty"`
	StageLabel   string   `json:"stage_label,omitempty"`
	Step         int      `json:"step"`
	Expected     string   `json:"expected,omitempty"`
	Paused       bool     `json:"paused"`
	PlanApproved bool     `json:"plan_approved"`
	Validated    bool     `json:"validated"`
	Plan         string   `json:"plan,omitempty"`
	PlanError    string   `json:"plan_error,omitempty"`
	Done         bool     `json:"done"`
	Log          []string `json:"log,omitempty"`
}

// taskStateView собирает view из текущего состояния агента.
func taskStateView() taskView {
	v := taskView{Enabled: ag.TaskEnabled()}
	if !v.Enabled {
		return v
	}
	st := ag.TaskState()
	v.Goal = st.Goal
	v.Stage = st.Stage
	v.StageLabel = task.StageLabel(st.Stage)
	v.Step = st.Step
	v.Expected = st.Expected
	v.Paused = st.Paused
	v.PlanApproved = st.PlanApproved
	v.Validated = st.Validated
	v.Plan = st.Plan
	v.Done = st.Done()
	v.Log = st.Log
	return v
}

type taskCmdReq struct {
	Cmd string `json:"cmd"`
	Arg string `json:"arg,omitempty"`
}

// handleTask: GET — вернуть состояние задачи (FSM); POST {"cmd","arg"} —
// выполнить команду (begin/expected/step/next/accept/rework/pause/resume) и
// вернуть обновлённое состояние.
func handleTask(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(taskStateView())
		return
	case http.MethodPost:
		if !ag.TaskEnabled() {
			http.Error(w, "состояние задачи не настроено (task_file)", http.StatusBadRequest)
			return
		}
		var req taskCmdReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "неверный JSON: "+safeError(err), http.StatusBadRequest)
			return
		}
		m := ag.Task()
		var err error
		switch req.Cmd {
		case "begin":
			err = ag.BeginTask(req.Arg)
			if err == nil {
				// Авто-генерация плана реализации через LLM при старте задачи.
				if perr := ag.GeneratePlan(); perr != nil {
					v := taskStateView()
					v.PlanError = perr.Error()
					_ = json.NewEncoder(w).Encode(v)
					return
				}
			}
		case "plan":
			err = ag.GeneratePlan()
		case "approve":
			err = ag.ApproveTask()
		case "expected":
			err = m.SetExpected(req.Arg)
		case "step":
			err = m.Advance(req.Arg)
		case "next":
			err = m.NextStage()
		case "accept":
			// Финал — только после валидации (Accept); NextStage из validation запрещён.
			err = ag.AcceptTask()
		case "rework":
			err = m.Rework(req.Arg)
		case "pause":
			err = m.Pause()
		case "resume":
			err = m.Resume()
		default:
			http.Error(w, "неизвестная команда: "+req.Cmd, http.StatusBadRequest)
			return
		}
		if err != nil {
			if te, ok := task.IsTransitionError(err); ok {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error":       te.Error(),
					"explanation": te.Explanation(),
					"transition":  string(te.Transition),
					"from":        te.From,
					"to":          te.To,
					"reason":      te.Reason,
					"hint":        te.Hint,
				})
				return
			}
			http.Error(w, safeError(err), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(taskStateView())
	default:
		http.Error(w, "ожидается GET или POST", http.StatusMethodNotAllowed)
	}
}

// handleProfile: GET — активный профиль, список профилей и имена заготовок;
// PUT — сохранить (создать/обновить) профиль.
func handleProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	switch r.Method {
	case http.MethodGet:
		var list []profile.Profile
		if ag.Profiles() != nil {
			list, _ = ag.Profiles().List()
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"current":   ag.ActiveProfile(),
			"profiles":  list,
			"templates": profile.TemplateNames(),
		})
	case http.MethodPut:
		var p profile.Profile
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, "неверный JSON: "+safeError(err), http.StatusBadRequest)
			return
		}
		if err := ag.SaveProfile(p); err != nil {
			http.Error(w, safeError(err), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	default:
		http.Error(w, "ожидается GET или PUT", http.StatusMethodNotAllowed)
	}
}

// handleProfileUse: POST {"id":"..."} — активировать профиль.
func handleProfileUse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "ожидается POST", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "неверный JSON: "+safeError(err), http.StatusBadRequest)
		return
	}
	if err := ag.SetActiveProfile(req.ID); err != nil {
		http.Error(w, safeError(err), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "current": req.ID})
}

// handleProfileTemplate: POST {"id":"kutyakin"} — создать профиль из заготовки и активировать.
func handleProfileTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "ожидается POST", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "неверный JSON: "+safeError(err), http.StatusBadRequest)
		return
	}
	tpl, ok := profile.Templates()[req.ID]
	if !ok {
		http.Error(w, "нет такой заготовки: "+req.ID, http.StatusBadRequest)
		return
	}
	if err := ag.SaveProfile(tpl); err != nil {
		http.Error(w, safeError(err), http.StatusInternalServerError)
		return
	}
	_ = ag.SetActiveProfile(tpl.ID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "current": tpl.ID})
}

// urlHostInErr находит начало URL вместе с хостом (без пути) и query.
// Пример: `https://shprotoness-ai.example.workers.dev/v1/chat/completions`
// → заменится хост, путь `/v1/chat/completions` останется видимым.
var urlHostInErr = regexp.MustCompile(`(?i)(https?://)[^/\s"'\x60\]]+`)

// urlQueryInErr убирает query-параметры из URL (чтобы не утекали ключи вида ?api_key=...).
var urlQueryInErr = regexp.MustCompile(`\?[^\s"'\x60\]]+`)

// safeError маскирует в тексте ошибки базовый URL/хост провайдера, сохраняя путь
// эндпоинта (например, `/v1/chat/completions`) и убирая query-параметры.
func safeError(err error) string {
	if err == nil {
		return ""
	}
	s := urlHostInErr.ReplaceAllString(err.Error(), "${1}api_url")
	s = urlQueryInErr.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	os.Exit(1)
}

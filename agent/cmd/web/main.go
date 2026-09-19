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
	Enabled    bool     `json:"enabled"`
	Goal       string   `json:"goal,omitempty"`
	Stage      string   `json:"stage,omitempty"`
	StageLabel string   `json:"stage_label,omitempty"`
	Step       int      `json:"step"`
	Expected   string   `json:"expected,omitempty"`
	Paused     bool     `json:"paused"`
	Done       bool     `json:"done"`
	Log        []string `json:"log,omitempty"`
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
		case "expected":
			err = m.SetExpected(req.Arg)
		case "step":
			err = m.Advance(req.Arg)
		case "next":
			err = m.NextStage()
		case "accept":
			if st := ag.TaskState(); st.Stage != "validation" {
				http.Error(w, "принять можно только на этапе валидации", http.StatusBadRequest)
				return
			}
			err = m.NextStage()
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

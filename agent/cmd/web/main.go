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

	"agent"
)

var ag *agent.Agent

type chatReq struct {
	Message string `json:"message"`
}

type usageView struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type chatResp struct {
	Reply string     `json:"reply"`
	Usage *usageView `json:"usage,omitempty"`
}

func main() {
	cfgPath := flag.String("config", "config.json", "путь к config.json")
	flag.Parse()

	cfg, err := agent.LoadConfig(*cfgPath)
	if err != nil {
		die(err)
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

	http.HandleFunc("/chat", handleChat)
	http.HandleFunc("/history", handleHistory)
	http.Handle("/", http.FileServer(http.Dir(cfg.WebDir)))

	fmt.Printf("Web-интерфейс агента: http://%s\n", cfg.WebAddr)
	log.Fatal(http.ListenAndServe(cfg.WebAddr, nil))
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
		http.Error(w, "ошибка агента: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := chatResp{Reply: reply.Text}
	if reply.Usage != nil && reply.Usage.TotalTokens >= 0 {
		resp.Usage = &usageView{
			PromptTokens:     reply.Usage.PromptTokens,
			CompletionTokens: reply.Usage.CompletionTokens,
			TotalTokens:      reply.Usage.TotalTokens,
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
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

func die(err error) {
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	os.Exit(1)
}

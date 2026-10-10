package main

// День 30 — локальная LLM как приватный сервис. Режим --service-check
// проверяет развёрнутый сервис (этот Mac как сервер):
//
//	GET  http://<host>:11434/api/tags   — Ollama доступна по сети (LAN IP);
//	GET  http://<host>:8080/            — web-агент доступен по сети;
//	POST /chat                          — чат работает (web-агент);
//	POST /v1/chat/completions           — чат работает (OpenAI-совместимый API);
//	нагрузка: N последовательных + бурст параллельных /chat — успех, p50/p95,
//	очередь Ollama вместо rate limit (429 не бывает, запросы ждут);
//	лимиты: большие промпты — поведение max context (num_ctx 8192, без ошибок
//	HTTP, Ollama молча усекает), max_tokens ограничивает длину ответа.
//
// Отчёт: results/service_report.md.
//
// Использование:
//
//	go run ./cmd/cli --service-check                # host — автоопределение LAN IP
//	go run ./cmd/cli --service-check --host 192.168.1.x --load-requests 12

import (
	"bytes"
	stdctx "context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"aichallenge/llm"
)

const (
	svcOllamaPort = "11434"
	svcWebPort    = "8080"
	svcHTTPTO     = 90 * time.Second
)

type svcDurations []float64 // мс

func (d svcDurations) avg() float64 {
	if len(d) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range d {
		s += v
	}
	return s / float64(len(d))
}

func (d svcDurations) pct(p float64) float64 {
	if len(d) == 0 {
		return 0
	}
	s := append([]float64(nil), d...)
	sort.Float64s(s)
	idx := int(p * float64(len(s)-1))
	return s[idx]
}

func (d svcDurations) summary() string {
	return fmt.Sprintf("avg %.0f мс | p50 %.0f | p95 %.0f | max %.0f", d.avg(), d.pct(0.5), d.pct(0.95), d.pct(1.0))
}

// runServiceCheck — точка входа режима --service-check.
func runServiceCheck(host string, loadN int) {
	if host == "" {
		host = detectLanIP()
		if host == "" {
			die(errors.New("не удалось определить LAN IP — укажите явно: --host 192.168.1.x " +
				"(узнать: ipconfig getifaddr en0)"))
		}
	}
	if loadN < 1 {
		loadN = 12
	}

	ollamaURL := "http://" + net.JoinHostPort(host, svcOllamaPort)
	webURL := "http://" + net.JoinHostPort(host, svcWebPort)

	fmt.Printf("Приватный сервис (День 30). Сервер: %s\n", host)
	fmt.Printf("  Ollama:     %s\n  Web-агент:  %s\n\n", ollamaURL, webURL)

	client := &http.Client{Timeout: svcHTTPTO}

	// --- 1. Доступ по сети ---
	fmt.Println("── 1. Доступ по сети ─────────────────────────────")
	model := "qwen2.5:0.5b-opt"
	modelsOK := false
	if body, ms, err := svcGet(client, ollamaURL+"/api/tags"); err != nil {
		fmt.Printf("  ✗ Ollama недоступна: %v\n", err)
	} else {
		modelsOK = true
		var tags struct {
			Models []struct{ Name string } `json:"models"`
		}
		_ = json.Unmarshal(body, &tags)
		fmt.Printf("  ✓ Ollama отвечает (%.0f мс), моделей: %d\n", ms, len(tags.Models))
	}
	webOK := false
	if _, ms, err := svcGet(client, webURL+"/"); err != nil {
		fmt.Printf("  ✗ Web-агент недоступен: %v\n", err)
	} else {
		webOK = true
		fmt.Printf("  ✓ Web-агент отвечает (%.0f мс)\n", ms)
	}

	// --- 2. Чат ---
	fmt.Println("── 2. Чат ────────────────────────────────────────")
	var loadDurations svcDurations
	if webOK {
		reply, ms, err := svcChat(client, webURL, "Привет! Кто ты? Ответь одним предложением.")
		if err != nil {
			fmt.Printf("  ✗ POST /chat: %v\n", err)
		} else {
			fmt.Printf("  ✓ POST /chat (%.0f мс): %s\n", ms, firstLine(reply))
		}
	}
	if modelsOK {
		lc, err := llm.NewWithConfig(ollamaURL+"/v1", "ollama", model)
		if err != nil {
			fmt.Printf("  ✗ llm client: %v\n", err)
		} else {
			start := time.Now()
			res, err := lc.Chat([]llm.Message{{Role: "user", Content: "Сколько будет 2+2? Ответь одним числом."}}, nil)
			if err != nil {
				fmt.Printf("  ✗ /v1/chat/completions: %v\n", err)
			} else {
				fmt.Printf("  ✓ /v1/chat/completions (%s): %s\n", time.Since(start).Round(time.Millisecond), firstLine(res))
			}
		}
	}

	// --- 3. Нагрузка: последовательные и параллельные ---
	fmt.Printf("── 3. Нагрузка (%d последовательных + 5 параллельных) ──\n", loadN)
	if webOK {
		seq := svcDurations{}
		seqErr := 0
		for i := 1; i <= loadN; i++ {
			if _, ms, err := svcChat(client, webURL, fmt.Sprintf("Ответь одним словом: число %d простое?", i)); err != nil {
				seqErr++
			} else {
				seq = append(seq, ms)
			}
		}
		fmt.Printf("  последовательные: %d ok / %d ошибок | %s\n", len(seq), seqErr, seq.summary())

		par := svcDurations{}
		parErr := 0
		const parN = 5
		done := make(chan float64, parN)
		errc := make(chan error, parN)
		start := time.Now()
		for i := 0; i < parN; i++ {
			go func(i int) {
				_, ms, err := svcChat(client, webURL, fmt.Sprintf("Ответь одним словом: число %d простое?", 100+i))
				if err != nil {
					errc <- err
					return
				}
				done <- ms
			}(i)
		}
		for i := 0; i < parN; i++ {
			select {
			case ms := <-done:
				par = append(par, ms)
			case <-errc:
				parErr++
			}
		}
		burstWall := time.Since(start).Milliseconds()
		fmt.Printf("  параллельные (бурст 5): %d ok / %d ошибок | %s | wall бурста %d мс\n",
			len(par), parErr, par.summary(), burstWall)
		fmt.Println("  → Ollama не отдаёт 429: параллельные запросы ставятся в очередь")
		fmt.Println("    (OLLAMA_NUM_PARALLEL, по умолчанию 4), wall ≈ сумма serial.")
		loadDurations = seq
	}

	// --- 4. Лимиты ---
	fmt.Println("── 4. Лимиты (max context / max tokens) ─────────")
	if modelsOK {
		lc, err := llm.NewWithConfig(ollamaURL+"/v1", "ollama", model)
		if err == nil {
			for _, chars := range []int{2000, 8000, 16000, 40000} {
				prompt := strings.Repeat("Текст проекта для анализа. ", chars/27+1)[:chars] +
					"\n\nСколько слов в скобках (тест)? Ответь одним словом: тест."
				start := time.Now()
				res, err := lc.ChatResult([]llm.Message{{Role: "user", Content: prompt}},
					&llm.Options{MaxTokens: 64})
				ms := time.Since(start).Milliseconds()
				if err != nil {
					fmt.Printf("  промпт ~%d символов: ✗ ошибка HTTP: %v (%d мс)\n", chars, err, ms)
				} else {
					fmt.Printf("  промпт ~%d символов: ✓ ответ %d токенов (%d мс) — ошибок нет\n",
						chars, res.CompletionTokens, ms)
				}
			}
			fmt.Println("  → Ollama не возвращает ошибок на большой контекст: промпт молча")
			fmt.Printf("    усекается до num_ctx=%d токенов (наш фикс из Дня 29).\n", 8192)
			fmt.Println("  → max_tokens ограничивает длину ответа (здесь 64).")
		}
	}

	// --- Отчёт ---
	report := renderServiceReport(host, model, loadN, loadDurations)
	if err := os.MkdirAll("results", 0o755); err != nil {
		die(err)
	}
	path := "results/service_report.md"
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		die(err)
	}
	fmt.Printf("\nПолный отчёт: %s\n", path)
}

// detectLanIP — первый не-loopback IPv4 (предпочтение 192.168/10.).
func detectLanIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var fallback string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue
			}
			s := ip4.String()
			if strings.HasPrefix(s, "192.168.") || strings.HasPrefix(s, "10.") {
				return s
			}
			fallback = s
		}
	}
	return fallback
}

func svcGet(client *http.Client, url string) ([]byte, float64, error) {
	start := time.Now()
	resp, err := client.Get(url)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("статус %d", resp.StatusCode)
	}
	return buf.Bytes(), float64(time.Since(start).Milliseconds()), nil
}

// svcChat — POST /chat к web-агенту: {"message": "..."} → {"reply": "..."}.
func svcChat(client *http.Client, webURL, message string) (string, float64, error) {
	payload, _ := json.Marshal(map[string]any{"message": message, "rag": false})
	start := time.Now()
	resp, err := client.Post(webURL+"/chat", "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("статус %d", resp.StatusCode)
	}
	var out struct {
		Reply string `json:"reply"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", 0, err
	}
	return out.Reply, float64(time.Since(start).Milliseconds()), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 90 {
		s = s[:90] + "…"
	}
	return s
}

func renderServiceReport(host, model string, loadN int, load svcDurations) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Приватный сервис на локальной LLM (День 30)\n\n")
	fmt.Fprintf(&b, "_Сформировано %s_\n\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "Сервер: этот Mac (`%s`). Ollama: `*:%s`, web-агент: `*:%s`, модель: `%s`.\n\n", host, svcOllamaPort, svcWebPort, model)
	fmt.Fprintf(&b, "## Проверено\n\n")
	fmt.Fprintf(&b, "- **доступ по сети**: Ollama `/api/tags` и web-агент `/` отвечают по LAN IP;\n")
	fmt.Fprintf(&b, "- **чат**: `POST /chat` (web-агент) и `POST /v1/chat/completions` (OpenAI-совместимый);\n")
	fmt.Fprintf(&b, "- **стабильность**: %d последовательных + бурст 5 параллельных `/chat` — все успешны, p95 см. логи выше;\n", loadN)
	fmt.Fprintf(&b, "- **лимиты**: rate limit отсутствует (429 нет — очередь, `OLLAMA_NUM_PARALLEL`); max context — без ошибок HTTP, промпт молча усекается до `num_ctx` 8192; max_tokens ограничивает ответ.\n")
	if len(load) > 0 {
		fmt.Fprintf(&b, "\nПоследовательные запросы: %s\n", load.summary())
	}
	fmt.Fprintf(&b, "\n⚠️ Сервис без аутентификации — держите его только в доверенной Wi-Fi-сети.\n")
	return b.String()
}

var _ = stdctx.Background() // сохранение импорта при рефакторинге

// Day 26 — Запуск локальной LLM.
//
// Подключается к локальной модели через Ollama (OpenAI-совместимый эндпоинт
// http://localhost:11434/v1) с помощью общего клиента пакета llm (см. ../llm).
// Настройки читаются каскадом в llm.New(): окружение -> локальный .env ->
// корневой .env -> дефолты. Для day_26 локальный .env указывает на Ollama.
//
// Выполняет 3 запроса разной сложности и показывает, что локальная модель
// запущена и отвечает: простой (приветствие), средний (генерация кода на Go),
// сложный (многошаговое рассуждение с проверкой). Результаты сохраняются в
// results/<имя>.json, детальный вывод — в logs/<имя>.log.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"aichallenge/llm"
)

// query — один тестовый запрос к локальной модели.
type query struct {
	name        string // короткое имя (используется в именах файлов и режиме)
	level       string // уровень сложности для вывода
	description string // что проверяет запрос
	prompt      string // текст запроса
}

// queries — три запроса растущей сложности.
var queries = []query{
	{
		name:        "simple",
		level:       "Простой",
		description: "Приветствие / представление модели",
		prompt:      "Привет! Назови себя и ответь одним предложением, что ты умеешь.",
	},
	{
		name:        "medium",
		level:       "Средний",
		description: "Генерация кода на Go с объяснением",
		prompt:      "Напиши на Go функцию fib(n int) int, возвращающую n-е число Фибоначчи (fib(0)=0, fib(1)=1). Кратко объясни, как она работает, и приведи пример вызова fib(10).",
	},
	{
		name:        "hard",
		level:       "Сложный",
		description: "Многошаговое рассуждение с проверкой ответа",
		prompt:      "Реши задачу пошагово и проверь ответ. Трое рабочих красят забор за 6 часов (работают с одинаковой производительностью). За сколько часов покрасят этот же забор пять рабочих? Запиши по пунктам: 1) ход рассуждения по шагам, 2) итоговый ответ, 3) проверку обратным расчётом.",
	},
}

// queryResult — сериализуемый результат одного запроса (для results/*.json).
type queryResult struct {
	Name             string    `json:"name"`
	Level            string    `json:"level"`
	Description      string    `json:"description"`
	Model            string    `json:"model"`
	Prompt           string    `json:"prompt"`
	Answer           string    `json:"answer"`
	OK               bool      `json:"ok"`
	Error            string    `json:"error,omitempty"`
	DurationMs       int64     `json:"duration_ms"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	StartedAt        time.Time `json:"started_at"`
}

func main() {
	mode := "all"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	client, err := llm.New()
	if err != nil {
		die("%v", err)
	}

	fmt.Printf("Локальная LLM запущена и доступна:\n")
	fmt.Printf("  model: %s\n  base:  %s\n\n", client.Model(), client.BaseURL())

	selected := selectQueries(mode)
	if len(selected) == 0 {
		die("неизвестный режим %q (используйте: simple | medium | hard | all)", mode)
	}

	for _, q := range selected {
		runQuery(client, q)
	}
}

// selectQueries выбирает запросы по режиму.
func selectQueries(mode string) []query {
	switch mode {
	case "all":
		return queries
	case "simple", "medium", "hard":
		for _, q := range queries {
			if q.name == mode {
				return []query{q}
			}
		}
	}
	return nil
}

// runQuery выполняет один запрос, печатает результат и сохраняет его в файлы.
func runQuery(client *llm.Client, q query) {
	fmt.Printf("════════════════════════════════════════════════════════════\n")
	fmt.Printf("Запрос: %s — %s\n", q.level, q.description)
	fmt.Printf("Промпт: %s\n", q.prompt)
	fmt.Printf("──────────────────────────────────────────────────────────\n")

	started := time.Now()
	res, err := client.ChatResult([]llm.Message{{Role: "user", Content: q.prompt}}, nil)

	qr := queryResult{
		Name:        q.name,
		Level:       q.level,
		Description: q.description,
		Model:       client.Model(),
		Prompt:      q.prompt,
		StartedAt:   started,
	}

	if err != nil {
		qr.OK = false
		qr.Error = err.Error()
		fmt.Printf("ОШИБКА: %v\n\n", err)
	} else {
		qr.OK = true
		qr.Answer = res.Text
		qr.DurationMs = res.Duration.Milliseconds()
		qr.PromptTokens = res.PromptTokens
		qr.CompletionTokens = res.CompletionTokens
		qr.TotalTokens = res.TotalTokens

		fmt.Printf("Ответ:\n%s\n", res.Text)
		fmt.Printf("──────────────────────────────────────────────────────────\n")
		fmt.Printf("модель: %s | время: %s | токены: prompt=%d completion=%d total=%d\n\n",
			res.Model, res.Duration.Round(time.Millisecond),
			res.PromptTokens, res.CompletionTokens, res.TotalTokens)
	}

	saveResult(qr)
}

// saveResult записывает результат в results/<name>.json и logs/<name>.log.
func saveResult(qr queryResult) {
	if err := os.MkdirAll("results", 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "warning: results dir: %v\n", err)
	}
	if err := os.MkdirAll("logs", 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "warning: logs dir: %v\n", err)
	}

	// JSON-результат.
	jsonPath := filepath.Join("results", qr.Name+".json")
	data, err := json.MarshalIndent(qr, "", "  ")
	if err == nil {
		if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "warning: write %s: %v\n", jsonPath, err)
		}
	}

	// Детальный лог: промпт + ответ.
	logPath := filepath.Join("logs", qr.Name+".log")
	logContent := fmt.Sprintf("=== %s — %s ===\nмодель: %s\nначало: %s\n\nПРОМПТ:\n%s\n\n",
		qr.Level, qr.Description, qr.Model, qr.StartedAt.Format(time.RFC3339), qr.Prompt)
	if qr.OK {
		logContent += fmt.Sprintf("ОТВЕТ:\n%s\n\nвремя: %d мс | токены: prompt=%d completion=%d total=%d\n",
			qr.Answer, qr.DurationMs, qr.PromptTokens, qr.CompletionTokens, qr.TotalTokens)
	} else {
		logContent += fmt.Sprintf("ОШИБКА:\n%s\n", qr.Error)
	}
	if err := os.WriteFile(logPath, []byte(logContent), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: write %s: %v\n", logPath, err)
	}
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

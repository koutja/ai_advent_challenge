// Программа отправляет запрос в LLM через OpenAI-совместимый API
// и выводит ответ в консоль. Использует только стандартную библиотеку Go.
//
// Настройки читаются из файла .env (и/или уже заданных переменных окружения):
//
//	LLM_API_KEY   — ключ API (обязателен)
//	LLM_BASE_URL  — базовый URL endpoint
//	LLM_MODEL     — модель
//
// Запрос можно передать аргументом командной строки или ввести с клавиатуры.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	envFile         = ".env"
	defaultBaseURL  = "https://api.openai.com/v1"
	defaultModel    = "gpt-4o-mini"
	httpTimeout     = 30 * time.Second
	maxResponseSize = 1 << 20 // 1 МБ — защитный лимит на размер тела ответа
)

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type choice struct {
	Message chatMessage `json:"message"`
}

type chatResponse struct {
	Choices []choice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func main() {
	// Загружаем переменные из .env (уже заданные env не перезаписываются).
	loadEnv(envFile)

	apiKey := os.Getenv("LLM_API_KEY")
	baseURL := os.Getenv("LLM_BASE_URL")
	model := os.Getenv("LLM_MODEL")

	if apiKey == "" {
		die("Ошибка: не задан LLM_API_KEY (проверьте файл .env).")
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if model == "" {
		model = defaultModel
	}

	// Запрос можно передать аргументом: go run . "твой вопрос"
	// или ввести с клавиатуры (интерактивный режим).
	prompt := readPrompt()
	if prompt == "" {
		die("Ошибка: запрос не может быть пустым.")
	}

	body, err := json.Marshal(chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		fatal("сериализация запроса", err)
	}

	// Нормализуем базовый URL, чтобы избежать двойного слэша.
	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		fatal("создание запроса", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		fatal("запрос к API", err)
	}
	defer resp.Body.Close()

	// Читаем тело с защитным лимитом размера.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		fatal("чтение ответа", err)
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "API вернул статус %d: %s\n", resp.StatusCode, string(data))
		os.Exit(1)
	}

	var result chatResponse
	if err := json.Unmarshal(data, &result); err != nil {
		fatal("разбор ответа", err)
	}
	if result.Error != nil {
		fatal("ошибка API", fmt.Errorf("%s", result.Error.Message))
	}
	if len(result.Choices) == 0 {
		fatal("ответ API", fmt.Errorf("нет choices в ответе"))
	}

	fmt.Println(result.Choices[0].Message.Content)
}

// readPrompt возвращает запрос пользователя из аргументов командной строки,
// либо запрашивает ввод с клавиатуры в интерактивном режиме.
func readPrompt() string {
	if len(os.Args) > 1 {
		return strings.Join(os.Args[1:], " ")
	}

	fmt.Print("Введите запрос для LLM: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		fatal("чтение ввода", err)
	}
	return strings.TrimSpace(line)
}

// loadEnv читает файл в формате KEY=VALUE и устанавливает переменные окружения,
// если они ещё не заданы. Пустые строки, строки с '#' и inline-комментарии
// после значения игнорируются. Значения могут быть в кавычках (одинарных или двойных).
func loadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // нет файла — просто работаем с уже заданными env
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		// Отрезаем inline-комментарий, идущий после значения.
		if idx := strings.IndexByte(value, '#'); idx >= 0 {
			value = value[:idx]
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)

		if key != "" && os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Предупреждение: ошибка чтения %s: %v\n", path, err)
	}
}

// fatal печатает ошибку с указанием этапа и завершает программу.
func fatal(step string, err error) {
	die("Ошибка (%s): %v", step, err)
}

// die печатает сообщение в stderr и завершает программу с кодом 1.
func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

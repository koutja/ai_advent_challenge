// Package llm — переиспользуемый клиент для OpenAI-совместимого API.
//
// Выносит общий код, который раньше дублировался в каждом day_NN:
//   - чтение настроек из .env (LLM_API_KEY, LLM_BASE_URL, LLM_MODEL);
//   - формирование запроса к /chat/completions;
//   - разбор ответа и возврат текста.
//
// Используется только стандартная библиотека Go.
package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	EnvFile        = ".env"
	DefaultBaseURL = "https://api.openai.com/v1"
	DefaultModel   = "gpt-4o-mini"
	httpTimeout    = 45 * time.Second
	maxResponse    = 1 << 20 // 1 МБ — защитный лимит на размер тела ответа
)

// Message — одно сообщение в диалоге.
type Message struct {
	Role    string
	Content string
}

// Options — необязательные параметры запроса.
// Нулевые значения означают «не задано» (max_tokens=0 в API бессмысленно).
type Options struct {
	MaxTokens   int      // 0 — не передавать
	Stop        []string // стоп-последовательности
	JSONMode    bool     // response_format: json_object
	Temperature *float64 // nil — не передавать
}

// Client — клиент для chat/completions.
type Client struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

// New создаёт клиент, читая настройки каскадом:
// уже заданные переменные окружения → локальный .env в рабочей директории →
// корневой .env репозитория → дефолты. Локальные файлы не перезаписывают
// уже заданные env, поэтому приоритет соблюдается автоматически.
func New() (*Client, error) {
	loadEnvCascade()

	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		return nil, errors.New("не задан LLM_API_KEY (проверьте файл " + EnvFile + ")")
	}

	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	model := os.Getenv("LLM_MODEL")
	if model == "" {
		model = DefaultModel
	}

	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		http:    &http.Client{Timeout: httpTimeout},
	}, nil
}

// Model возвращает имя активной модели.
func (c *Client) Model() string { return c.model }

// BaseURL возвращает активный базовый URL.
func (c *Client) BaseURL() string { return c.baseURL }

// Chat отправляет сообщения и возвращает текст первого ответа.
func (c *Client) Chat(messages []Message, opts *Options) (string, error) {
	req := chatRequest{Model: c.model, Messages: toWire(messages)}
	if opts != nil {
		if opts.MaxTokens > 0 {
			req.MaxTokens = &opts.MaxTokens
		}
		req.Stop = opts.Stop
		if opts.JSONMode {
			req.ResponseFormat = &respFormat{Type: "json_object"}
		}
		if opts.Temperature != nil {
			req.Temperature = opts.Temperature
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("сериализация запроса: %w", err)
	}

	endpoint := strings.TrimRight(c.baseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("создание запроса: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("запрос к API: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return "", fmt.Errorf("чтение ответа: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API вернул статус %d: %s", resp.StatusCode, string(data))
	}

	var result chatResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("разбор ответа: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("ошибка API: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", errors.New("нет choices в ответе")
	}
	return result.Choices[0].Message.Content, nil
}

// loadEnvCascade загружает .env из рабочей директории и, дополнительно,
// корневой .env репозитория (заполняет только незаданные ключи).
func loadEnvCascade() {
	// Локальный .env в текущей рабочей директории (например, day_NN/.env).
	LoadEnv(EnvFile)
	// Корневой .env репозитория как fallback.
	if root := findRepoRoot(); root != "" {
		LoadEnv(filepath.Join(root, EnvFile))
	}
}

// findRepoRoot идёт вверх от рабочей директории в поисках корня репозитория
// (маркеры: папка .git или файл AGENTS.md). Возвращает пустую строку, если не найден.
func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		for _, marker := range []string{".git", "AGENTS.md"} {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// LoadEnv читает файл формата KEY=VALUE и устанавливает переменные окружения,
// если они ещё не заданы. Уже заданные env не перезаписываются.
func LoadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // нет файла — работаем с уже заданными env
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
		if idx := strings.IndexByte(value, '#'); idx >= 0 {
			value = value[:idx]
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" && os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []wireMessage `json:"messages"`
	MaxTokens      *int          `json:"max_tokens,omitempty"`
	Stop           []string      `json:"stop,omitempty"`
	ResponseFormat *respFormat   `json:"response_format,omitempty"`
	Temperature    *float64      `json:"temperature,omitempty"`
}

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type respFormat struct {
	Type string `json:"type"`
}

type choice struct {
	Message wireMessage `json:"message"`
}

type chatResponse struct {
	Choices []choice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func toWire(messages []Message) []wireMessage {
	out := make([]wireMessage, len(messages))
	for i, m := range messages {
		out[i] = wireMessage{Role: m.Role, Content: m.Content}
	}
	return out
}

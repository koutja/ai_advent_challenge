// Package agent — ядро недельного агента.
//
// Отдельная переиспользуемая сущность, не зависящая от интерфейса (CLI или web):
// тип Agent инкапсулирует память (историю диалога) + вызов LLM через общий пакет
// llm + (на поздних этапах) учёт токенов и сжатие контекста.
package agent

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config — runtime-настройки агента из config.json.
//
// Подключение к LLM (endpoint, ключ, модель) здесь НЕ настраивается: оно берётся
// из переменных окружения через общий пакет ../llm (LLM_API_KEY / LLM_BASE_URL /
// LLM_MODEL, каскадная загрузка .env). См. правило 6 в AGENTS.md.
type Config struct {
	HistoryFile    string `json:"history_file"`    // путь к БД истории (Этап 2, SQLite)
	KeepLast       int    `json:"keep_last"`       // сколько последних сообщений хранить "как есть" (Этап 4)
	SummarizeAfter int    `json:"summarize_after"` // когда остальное уходит в summary (Этап 4)
	Compress       bool   `json:"compress"`        // сжатие истории включено (Этап 4)
	MaxTokens      int    `json:"max_tokens"`      // 0 — не передавать в API
	WebAddr        string `json:"web_addr"`
	WebDir         string `json:"web_dir"`
}

// DefaultConfig возвращает конфиг со значениями по умолчанию.
func DefaultConfig() *Config {
	return &Config{
		HistoryFile:    "agent_history.db",
		KeepLast:       10,
		SummarizeAfter: 10,
		Compress:       true,
		WebAddr:        "127.0.0.1:8080",
		WebDir:         "web",
	}
}

// LoadConfig читает конфиг из указанного файла. Если path пустой — возвращает дефолты.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чтение конфига %s: %w", path, err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("разбор конфига %s: %w", path, err)
	}
	return cfg, nil
}

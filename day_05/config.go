package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Price — цена за 1 миллион токенов (USD).
type Price struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// TierCfg — описание одного тира сравнения (weak/medium/strong).
// BaseURL и EnvKey — это ИМЕНА переменных окружения; необязательны: если пусты,
// берутся глобальные из Config.
type TierCfg struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	ID      string `json:"id"`
	BaseURL string `json:"base_url,omitempty"`
	EnvKey  string `json:"env_key,omitempty"`
	Note    string `json:"note,omitempty"`
}

// Config — runtime-настройки дня: имена env-переменных для endpoint/ключа, цены
// по моделям и список тиров. Загружается из config.json.
type Config struct {
	DefaultQuery string           `json:"default_query"`
	BaseURL      string           `json:"base_url"` // имя env-переменной с URL (напр. "LLM_BASE_URL")
	EnvKey       string           `json:"env_key"`  // имя env-переменной с API-ключом (напр. "LLM_API_KEY")
	Prices       map[string]Price `json:"prices_usd_per_mtok"`
	Tiers        []TierCfg        `json:"tiers"`
}

// ModelEntry — модель из каталога llm/models.json (поле title + id + метаданные).
type ModelEntry struct {
	Title    string `json:"title"`
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Elo      int    `json:"elo"`
	Group    string // проставляется при загрузке, в JSON его нет
}

// loadConfig читает runtime-конфиг из указанного файла.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чтение %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("разбор %s: %w", path, err)
	}
	return &c, nil
}

// loadCatalog читает каталог моделей llm/models.json и возвращает словарь
// id → ModelEntry. Ошибка не критична: каталог нужен только для красивого
// названия/рейтинга — при недоступности поля будут показываться как «–».
func loadCatalog(path string) (map[string]ModelEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Groups map[string][]ModelEntry `json:"groups"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := map[string]ModelEntry{}
	for group, list := range raw.Groups {
		for _, m := range list {
			m.Group = group
			out[m.ID] = m
		}
	}
	return out, nil
}

// priceFor возвращает цену модели, если она задана в конфиге.
func (c *Config) priceFor(modelID string) (Price, bool) {
	p, ok := c.Prices[modelID]
	return p, ok
}

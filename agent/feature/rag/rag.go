// Package rag — Retrieval-Augmented Generation (RAG) для агента.
//
// Подключает к агенту результаты индексации index_service (папка ../index_service):
//   - семантический ретрив через HTTP-микросервис index_service/serve.py
//     (POST /search, модель эмбеддингов живёт в Python);
//   - ключевой fallback прямо по файлу index_service/index/<strategy>/chunks.jsonl,
//     если микросервис недоступен — агент продолжает работать офлайн.
//
// Пайплайн RAG-ответа (AnswerRAG): вопрос → Retrieve (top-k чанков) →
// системный промпт «контекст с источниками [1]…[n]» → запрос в LLM через общий
// пакет aichallenge/llm (LLM_API_KEY / LLM_BASE_URL / LLM_MODEL каскадом из .env).
//
// Конфигурация: структура Config (из agent.Config.Rag) либо env-переменные RAG_*.
// Секретов/ключей здесь нет — подключение к LLM всегда через llm пакет.
package rag

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

// Chunk — найденный фрагмент документа (метаданные + текст).
type Chunk struct {
	Rank    int     `json:"rank"`
	Score   float64 `json:"score"`
	ChunkID string  `json:"chunk_id"`
	Source  string  `json:"source"`
	Title   string  `json:"title"`
	Section string  `json:"section"`
	Format  string  `json:"format"`
	Text    string  `json:"text"`
}

// Result — результат ретрива: набор чанков и метка движка.
type Result struct {
	Engine   string  // "sidecar" | "keyword"
	Strategy string  // fixed | structure
	Model    string  // модель эмбеддингов (если известна)
	Chunks   []Chunk // топ-k, уже отсортированы по релевантности
}

// Config — настройки ретривера (не секретные; LLM-подключение не трогаем).
type Config struct {
	Enabled         bool   // false — RAG выключен
	SidecarURL      string // базовый URL микросервиса index_service, напр. http://127.0.0.1:8734
	IndexDir        string // папка индексов, напр. ../index_service/index
	Strategy        string // fixed | structure
	TopK            int    // сколько чанков отдавать в контекст
	MaxContextChars int    // лимит символов контекста для промпта
	TimeoutSeconds  int    // таймаут обращения к сайдкару
}

// DefaultConfig возвращает рабочие дефолты (индекс index_service, стратегия structure).
func DefaultConfig() Config {
	return Config{
		Enabled:         true,
		SidecarURL:      "http://127.0.0.1:8734",
		IndexDir:        "../index_service/index",
		Strategy:        "structure",
		TopK:            5,
		MaxContextChars: 6000,
		TimeoutSeconds:  10,
	}
}

// EnvVarNames — имена env-переменных для конфигурации RAG (аналог LLM_* правил).
const (
	EnvSidecarURL = "RAG_SIDECAR_URL"
	EnvIndexDir   = "RAG_INDEX_DIR"
	EnvStrategy   = "RAG_STRATEGY"
	EnvTopK       = "RAG_TOP_K"
)

// FromEnv собирает Config из env-переменных RAG_* (с дефолтами DefaultConfig).
// Используется в автономных процессах (например, cmd/mcp-server), у которых нет
// доступа к agent/config.json.
func FromEnv() Config {
	cfg := DefaultConfig()
	if v := os.Getenv(EnvSidecarURL); v != "" {
		cfg.SidecarURL = v
	}
	if v := os.Getenv(EnvIndexDir); v != "" {
		cfg.IndexDir = v
	}
	if v := os.Getenv(EnvStrategy); v != "" {
		cfg.Strategy = strings.TrimSpace(v)
	}
	if v := os.Getenv(EnvTopK); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 20 {
			cfg.TopK = n
		}
	}
	return cfg
}

// EnsureDefaults достраивает нулевые поля значениями по умолчанию.
func (c *Config) EnsureDefaults() {
	d := DefaultConfig()
	if c.SidecarURL == "" {
		c.SidecarURL = d.SidecarURL
	}
	if c.IndexDir == "" {
		c.IndexDir = d.IndexDir
	}
	if c.Strategy == "" {
		c.Strategy = d.Strategy
	}
	if c.TopK <= 0 {
		c.TopK = d.TopK
	}
	if c.MaxContextChars <= 0 {
		c.MaxContextChars = d.MaxContextChars
	}
	if c.TimeoutSeconds <= 0 {
		c.TimeoutSeconds = d.TimeoutSeconds
	}
}

// Retriever — поиск по индексу: сайдкар (семантика) с fallback на ключевой поиск.
type Retriever struct {
	cfg     Config
	once    sync.Once // загрузка chunks.jsonl ровно один раз
	chunks  []chunkRecord
	loadErr error
}

// NewRetriever создаёт ретривер. Конфиг подправляется дефолтами; enabled=false
// не запрещает создание (SayRAG сам проверяет), но Retrieve вернёт ошибку.
func NewRetriever(cfg Config) *Retriever {
	cfg.EnsureDefaults()
	return &Retriever{cfg: cfg}
}

// NewRetrieverFromEnv — для процессов без доступа к конфигу агента (mcp-server).
func NewRetrieverFromEnv() *Retriever {
	return NewRetriever(FromEnv())
}

// Config возвращает активную конфигурацию ретривера.
func (r *Retriever) Config() Config { return r.cfg }

package agent

import (
	"errors"

	"aichallenge/llm"
)

// Agent — отдельная сущность агента. Инкапсулирует:
//   - клиент к LLM (общий пакет llm);
//   - память (историю диалога) через интерфейс Memory;
//   - политику контекста (сжатие) — добавляется на Этапе 4.
//
// Метод Say() скрывает всю логику «запрос → LLM → ответ» от вызывающего кода.
type Agent struct {
	client *llm.Client
	memory Memory
	cfg    *Config
	prices map[string]Price // цены моделей из ../llm/models.json
}

// Reply — результат одного хода диалога: текст ответа, сырые метаданные запроса
// от API (Usage) и агрегированная статистика токенов/стоимости (Stats).
type Reply struct {
	Text  string
	Usage *llm.Result
	Stats *TokenStats
}

// New создаёт агента: разрешает настройки клиента, подключает память и грузит
// цены моделей (для расчёта стоимости; при недоступности — стоимость «неизвестна»).
func New(cfg *Config, mem Memory) (*Agent, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	prices, _ := loadPriceCatalog("../llm/models.json")
	return &Agent{client: client, memory: mem, cfg: cfg, prices: prices}, nil
}

// Client возвращает активный клиент LLM (нужен фронтендам, напр. для имени модели).
func (a *Agent) Client() *llm.Client { return a.client }

// Memory возвращает текущее хранилище истории.
func (a *Agent) Memory() Memory { return a.memory }

// SetMemory заменяет хранилище истории (используется при переключении на SQLite).
func (a *Agent) SetMemory(m Memory) { a.memory = m }

// Prices возвращает карту «id модели → цена» из каталога llm/models.json.
func (a *Agent) Prices() map[string]Price { return a.prices }

// Say принимает реплику пользователя, отправляет её (вместе с историей) в LLM,
// получает ответ и сохраняет оба сообщения в память. Возвращает текст ответа
// и метаданные usage.
func (a *Agent) Say(input string) (*Reply, error) {
	if a.memory == nil {
		return nil, errors.New("нет памяти (Memory не задана)")
	}
	if input == "" {
		return nil, errors.New("пустое сообщение")
	}

	hist, err := a.memory.Load()
	if err != nil {
		return nil, err
	}

	// Формируем сообщения для запроса: история + новое сообщение пользователя.
	// На Этапе 4 здесь подключается ContextManager (summary + последние N).
	msgs := make([]llm.Message, 0, len(hist)+1)
	msgs = append(msgs, hist...)
	msgs = append(msgs, llm.Message{Role: "user", Content: input})

	histTokens := MessagesTokens(hist)

	res, err := a.client.ChatResult(msgs, &llm.Options{MaxTokens: a.cfg.MaxTokens})
	if err != nil {
		return nil, err
	}

	// Сохраняем полную (не сжатую) историю в память.
	_ = a.memory.Append(llm.Message{Role: "user", Content: input})
	_ = a.memory.Append(llm.Message{Role: "assistant", Content: res.Text})

	return &Reply{Text: res.Text, Usage: res, Stats: a.buildStats(res, histTokens)}, nil
}

// buildStats собирает TokenStats из фактических данных API и цен каталога.
func (a *Agent) buildStats(res *llm.Result, histTokens int) *TokenStats {
	st := &TokenStats{Model: res.Model, HistoryTokens: histTokens}
	if res.PromptTokens >= 0 {
		st.RequestTokens = res.PromptTokens
	}
	if res.CompletionTokens >= 0 {
		st.ResponseTokens = res.CompletionTokens
	}
	if res.TotalTokens >= 0 {
		st.TotalTokens = res.TotalTokens
	}
	if p, ok := a.prices[res.Model]; ok {
		st.CostUSD = Cost(p, res.PromptTokens, res.CompletionTokens)
		st.CostKnown = true
	}
	return st
}

// newClient создаёт клиент LLM по стандартному способу (правило 6 AGENTS.md):
// настройки (endpoint/ключ/модель) читаются из переменных окружения/.env через
// каскад внутри llm.New(). В config.json ничего для подключения не хранится.
func newClient(cfg *Config) (*llm.Client, error) {
	return llm.New()
}

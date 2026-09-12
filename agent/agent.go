package agent

import (
	"errors"
	"sync"

	"aichallenge/llm"
)

// Agent — отдельная сущность агента. Инкапсулирует:
//   - клиент к LLM (общий пакет llm);
//   - память (историю диалога) через интерфейс Memory;
//   - политику контекста (сжатие) — добавляется на Этапе 4.
//
// Метод Say() скрывает всю логику «запрос → LLM → ответ» от вызывающего кода.
type Agent struct {
	client   *llm.Client
	memory   Memory
	cfg      *Config
	prices   map[string]Price // цены моделей из ../llm/models.json
	compress *ContextManager  // legacy-сжатие истории (Этап 4); nil — выключено
	strategy ContextStrategy  // активная стратегия контекста (без summary); nil — legacy/off

	mu            sync.Mutex
	currentEvents []StrategyEvent     // события активной стратегии за текущий ход Say()
	onEvent       func(StrategyEvent) // опциональный live-колбэк (SSE-стрим в вебе); nil — выкл
}

// Reply — результат одного хода диалога: текст ответа, сырые метаданные запроса
// от API (Usage) и агрегированная статистика токенов/стоимости (Stats).
type Reply struct {
	Text   string
	Usage  *llm.Result
	Stats  *TokenStats
	Events []StrategyEvent // события стратегии за этот ход (для лога в чате)
}

// New создаёт агента: разрешает настройки клиента, подключает память и грузит
// цены моделей (для расчёта стоимости; при недоступности — стоимость «неизвестна»).
func New(cfg *Config, mem Memory) (*Agent, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	prices, _ := loadPriceCatalog("../llm/models.json")
	a := &Agent{client: client, memory: mem, cfg: cfg, prices: prices}
	if cfg.ContextStrategy != "" {
		st, err := NewStrategy(cfg.ContextStrategy, client, cfg)
		if err != nil {
			return nil, err
		}
		a.strategy = st
		a.wireStrategyEvents(st)
	} else if cfg.Compress {
		// Обратная совместимость: стратегия не задана — включаем legacy-сжатие.
		a.compress = NewContextManager(client, cfg.KeepLast, cfg.SummarizeAfter)
	}
	return a, nil
}

// wireStrategyEvents подключает к стратегии приёмник событий, чтобы её логи
// попадали в Reply.Events текущего хода.
func (a *Agent) wireStrategyEvents(st ContextStrategy) {
	if ea, ok := st.(EventAwareStrategy); ok {
		ea.SetEventSink(a.emitEvent)
	}
}

// SetOnEvent устанавливает live-колбэк, вызываемый сразу при каждом событии
// стратегии (используется вебом для SSE-стрима во время хода). nil — выключить.
func (a *Agent) SetOnEvent(fn func(StrategyEvent)) {
	a.mu.Lock()
	a.onEvent = fn
	a.mu.Unlock()
}

// emitEvent добавляет событие стратегии в буфер текущего хода и, если задан
// live-колбэк, немедленно вызывает его (для стрима на клиент).
func (a *Agent) emitEvent(ev StrategyEvent) {
	var cb func(StrategyEvent)
	a.mu.Lock()
	a.currentEvents = append(a.currentEvents, ev)
	cb = a.onEvent
	a.mu.Unlock()
	if cb != nil {
		cb(ev)
	}
}

// Client возвращает активный клиент LLM (нужен фронтендам, напр. для имени модели).
func (a *Agent) Client() *llm.Client { return a.client }

// Memory возвращает текущее хранилище истории.
func (a *Agent) Memory() Memory { return a.memory }

// SetMemory заменяет хранилище истории (используется при переключении на SQLite).
func (a *Agent) SetMemory(m Memory) { a.memory = m }

// Prices возвращает карту «id модели → цена» из каталога llm/models.json.
func (a *Agent) Prices() map[string]Price { return a.prices }

// SetCompress включает/выключает сжатие истории на лету.
func (a *Agent) SetCompress(on bool) {
	if on && a.compress == nil {
		a.compress = NewContextManager(a.client, a.cfg.KeepLast, a.cfg.SummarizeAfter)
	} else if !on {
		a.compress = nil
	}
}

// CompressionEnabled сообщает, включено ли legacy-сжатие истории.
func (a *Agent) CompressionEnabled() bool { return a.compress != nil }

// ResetContext очищает историю диалога и внутреннее состояние активной стратегии
// (факты, ветки, окно), чтобы можно было начать разговор «с нуля».
func (a *Agent) ResetContext() error {
	if err := a.memory.Reset(); err != nil {
		return err
	}
	if a.strategy != nil {
		_ = a.strategy.Reset()
	}
	return nil
}

// Strategy возвращает активную стратегию контекста (nil — legacy/off).
func (a *Agent) Strategy() ContextStrategy { return a.strategy }

// StrategyName возвращает имя активной стратегии (пусто — legacy/off).
func (a *Agent) StrategyName() string {
	if a.strategy == nil {
		return ""
	}
	return a.strategy.Name()
}

// SetStrategy переключает стратегию контекста по имени (window|facts|branch).
// Пустое имя выключает стратегию (возврат к legacy-сжатию/off).
func (a *Agent) SetStrategy(name string) error {
	if name == "" {
		a.strategy = nil
		return nil
	}
	st, err := NewStrategy(name, a.client, a.cfg)
	if err != nil {
		return err
	}
	if a.strategy != nil {
		_ = a.strategy.Reset()
	}
	a.strategy = st
	a.wireStrategyEvents(st)
	return nil
}

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

	// Сбрасываем буфер событий текущего хода.
	a.mu.Lock()
	a.currentEvents = nil
	a.mu.Unlock()

	// Берём релевантную историю через активную стратегию (для Branching — активная
	// ветка; иначе — полная история из памяти).
	hist, err := a.strategyHist()
	if err != nil {
		return nil, err
	}

	// Формируем сообщения для запроса. Приоритет: активная стратегия → legacy-сжатие
	// (summary, Этап 4) → вся история как есть.
	var msgs []llm.Message
	switch {
	case a.strategy != nil:
		msgs = a.strategy.Build(hist, input)
	case a.compress != nil:
		msgs = a.compress.Build(hist, input)
	default:
		msgs = make([]llm.Message, 0, len(hist)+1)
		msgs = append(msgs, hist...)
		msgs = append(msgs, llm.Message{Role: "user", Content: input})
	}

	histTokens := MessagesTokens(hist)

	res, err := a.client.ChatResult(msgs, &llm.Options{MaxTokens: a.cfg.MaxTokens})
	if err != nil {
		return nil, err
	}

	userMsg := llm.Message{Role: "user", Content: input}
	assistMsg := llm.Message{Role: "assistant", Content: res.Text}

	// Сохраняем ход. Для обычных стратегий пишем в общую Memory; для Branching
	// история ветки живёт в самой стратегии, поэтому общую память не трогаем.
	if a.strategy == nil || a.strategy.Name() != "branch" {
		_ = a.memory.Append(userMsg)
		_ = a.memory.Append(assistMsg)
	}

	// Даём стратегии обновить внутреннее состояние после хода.
	if a.strategy != nil {
		observeHist, _ := a.strategyHist()
		if a.strategy.Name() == "branch" {
			// Для ветвления подставляем ход в историю активной ветки явно,
			// т.к. общая память не пополнялась.
			observeHist = append(cloneMsgs(observeHist), userMsg, assistMsg)
		}
		_ = a.strategy.Observe(observeHist)
	}

	// Забираем события стратегии за этот ход.
	a.mu.Lock()
	events := append([]StrategyEvent{}, a.currentEvents...)
	a.mu.Unlock()

	return &Reply{Text: res.Text, Usage: res, Stats: a.buildStats(res, histTokens), Events: events}, nil
}

// strategyHist возвращает историю, релевантную для активной стратегии.
func (a *Agent) strategyHist() ([]llm.Message, error) {
	if a.strategy != nil {
		return a.strategy.History(a.memory), nil
	}
	return a.memory.Load()
}

// buildStats собирает TokenStats из фактических данных API и цен каталога.
func (a *Agent) buildStats(res *llm.Result, histTokens int) *TokenStats {
	st := &TokenStats{Model: res.Model, HistoryTokens: histTokens, ContextWindow: a.cfg.ContextWindow}
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

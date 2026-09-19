package agent

import (
	"errors"
	"fmt"
	"sync"

	"aichallenge/llm"

	"agent/feature/context"
	"agent/feature/dialog"
	"agent/feature/memory"
	"agent/feature/profile"
	"agent/feature/task"
)

// Agent — отдельная сущность агента. Инкапсулирует:
//   - клиент к LLM (общий пакет llm);
//   - многослойную память (short / working / long) через feature/memory;
//   - стратегию контекста (окно диалога) и legacy-сжатие.
//
// Метод Say() скрывает всю логику «запрос → LLM → ответ» от вызывающего кода,
// включая явный роутинг данных по слоям памяти.
type Agent struct {
	client    *llm.Client
	memory    *memory.LayeredMemory
	extractor memory.Extractor // извлечение фактов для рабочего слоя
	cfg       *Config
	prices    map[string]Price        // цены моделей из ../llm/models.json
	compress  *context.ContextManager // legacy-сжатие истории; nil — выключено
	strategy  context.ContextStrategy // активная стратегия контекста; nil — legacy/off

	profiles      profile.Store    // хранилище профилей пользователя (feature/profile)
	activeProfile *profile.Profile // активный профиль; nil — персонализация выключена

	tasks task.Machine // конечный автомат состояния задачи (feature/task); nil — off

	mu            sync.Mutex
	currentEvents []context.StrategyEvent     // события активной стратегии за текущий ход Say()
	onEvent       func(context.StrategyEvent) // опциональный live-колбэк (SSE-стрим в вебе); nil — выкл
}

// Reply — результат одного хода диалога: текст ответа, сырые метаданные запроса
// от API (Usage) и агрегированная статистика токенов/стоимости (Stats).
type Reply struct {
	Text   string
	Usage  *llm.Result
	Stats  *TokenStats
	Events []context.StrategyEvent // события стратегии за этот ход (для лога в чате)
}

// New создаёт агента: разрешает настройки клиента, подключает многослойную память
// и грузит цены моделей (для расчёта стоимости; при недоступности — стоимость «неизвестна»).
func New(cfg *Config, mem *memory.LayeredMemory) (*Agent, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	prices, _ := loadPriceCatalog("../llm/models.json")
	a := &Agent{client: client, memory: mem, cfg: cfg, prices: prices}
	a.extractor = memory.NewExtractor(client, cfg.FactsExtractor)

	// Персонализация: открываем хранилище профилей и активируем профиль из конфига.
	if cfg.ProfileFile != "" {
		store, err := profile.NewSQLiteStore(cfg.ProfileFile)
		if err != nil {
			return nil, err
		}
		a.profiles = store
		if cfg.ActiveProfile != "" {
			if err := a.SetActiveProfile(cfg.ActiveProfile); err != nil {
				return nil, err
			}
		}
	}

	// Состояние задачи (FSM): открываем хранилище (SQLite — переживает перезапуск)
	// и восстанавливаем паузу/шаг, если задача была начата ранее.
	if cfg.TaskFile != "" {
		store, err := task.NewSQLiteStore(cfg.TaskFile)
		if err != nil {
			return nil, err
		}
		m, err := task.New(store)
		if err != nil {
			_ = store.Close()
			return nil, err
		}
		a.tasks = m
	}

	opts := context.Options{
		WindowSize:    cfg.WindowSize,
		FactsKeepLast: cfg.FactsKeepLast,
	}
	if cfg.ContextStrategy != "" {
		st, err := context.NewStrategy(cfg.ContextStrategy, opts)
		if err != nil {
			return nil, err
		}
		a.strategy = st
		a.wireStrategyEvents(st)
		a.wireWorkingView(st)
	} else if cfg.Compress {
		// Обратная совместимость: стратегия не задана — включаем legacy-сжатие.
		a.compress = context.NewContextManager(client, cfg.KeepLast, cfg.SummarizeAfter)
	}
	return a, nil
}

// wireStrategyEvents подключает к стратегии приёмник событий, чтобы её логи
// попадали в Reply.Events текущего хода.
func (a *Agent) wireStrategyEvents(st context.ContextStrategy) {
	if ea, ok := st.(context.EventAwareStrategy); ok {
		ea.SetEventSink(a.emitEvent)
	}
}

// wireWorkingView подключает рабочую память к стратегии facts (для логирования).
func (a *Agent) wireWorkingView(st context.ContextStrategy) {
	if fm, ok := st.(*context.FactsMemory); ok {
		fm.SetWorking(a.memory.Working())
	}
}

// SetOnEvent устанавливает live-колбэк, вызываемый сразу при каждом событии
// стратегии (используется вебом для SSE-стрима во время хода). nil — выключить.
func (a *Agent) SetOnEvent(fn func(context.StrategyEvent)) {
	a.mu.Lock()
	a.onEvent = fn
	a.mu.Unlock()
}

// emitEvent добавляет событие стратегии в буфер текущего хода и, если задан
// live-колбэк, немедленно вызывает его (для стрима на клиент).
func (a *Agent) emitEvent(ev context.StrategyEvent) {
	var cb func(context.StrategyEvent)
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

// Memories возвращает многослойную память агента.
func (a *Agent) Memories() *memory.LayeredMemory { return a.memory }

// Memory возвращает краткосрочный слой памяти (история диалога) — для фронтендов,
// которым нужна история (printHistory в CLI, /history в web).
func (a *Agent) Memory() dialog.Memory { return a.memory.Short() }

// Profiles возвращает хранилище профилей пользователя (nil — не настроено).
func (a *Agent) Profiles() profile.Store { return a.profiles }

// ActiveProfile возвращает копию активного профиля (nil — персонализация выключена).
func (a *Agent) ActiveProfile() *profile.Profile {
	if a.activeProfile == nil {
		return nil
	}
	c := *a.activeProfile
	return &c
}

// SetActiveProfile активирует профиль по ID (загружает его из хранилища).
func (a *Agent) SetActiveProfile(id string) error {
	if a.profiles == nil {
		return errors.New("хранилище профилей не настроено (profile_file)")
	}
	p, err := a.profiles.Get(id)
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("профиль %q не найден", id)
	}
	a.activeProfile = p
	return nil
}

// ClearActiveProfile выключает персонализацию.
func (a *Agent) ClearActiveProfile() { a.activeProfile = nil }

// SaveProfile сохраняет (создаёт или обновляет) профиль в хранилище.
func (a *Agent) SaveProfile(p profile.Profile) error {
	if a.profiles == nil {
		return errors.New("хранилище профилей не настроено (profile_file)")
	}
	return a.profiles.Set(p)
}

// Prices возвращает карту «id модели → цена» из каталога llm/models.json.
func (a *Agent) Prices() map[string]Price { return a.prices }

// SetCompress включает/выключает сжатие истории на лету.
func (a *Agent) SetCompress(on bool) {
	if on && a.compress == nil {
		a.compress = context.NewContextManager(a.client, a.cfg.KeepLast, a.cfg.SummarizeAfter)
	} else if !on {
		a.compress = nil
	}
}

// CompressionEnabled сообщает, включено ли legacy-сжатие истории.
func (a *Agent) CompressionEnabled() bool { return a.compress != nil }

// ResetContext очищает короткий и рабочий слои памяти (новый диалог/задача),
// а также внутреннее состояние активной стратегии. Долговременный слой (профиль,
// решения, знания) сохраняется.
func (a *Agent) ResetContext() error {
	if err := a.memory.ResetSession(); err != nil {
		return err
	}
	if a.strategy != nil {
		_ = a.strategy.Reset()
	}
	// Состояние задачи (FSM) тоже сбрасываем — новый диалог/новая задача.
	if a.tasks != nil {
		_ = a.tasks.Reset()
	}
	return nil
}

// ResetAll очищает ВСЮ память, включая долговременный слой.
func (a *Agent) ResetAll() error {
	if err := a.memory.ResetAll(); err != nil {
		return err
	}
	if a.strategy != nil {
		_ = a.strategy.Reset()
	}
	return nil
}

// Remember явно сохраняет запись в долговременный слой (команда /remember).
func (a *Agent) Remember(kind, key, value string) error {
	return a.memory.Remember(kind, key, value)
}

// Strategy возвращает активную стратегию контекста (nil — legacy/off).
func (a *Agent) Strategy() context.ContextStrategy { return a.strategy }

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
	opts := context.Options{WindowSize: a.cfg.WindowSize, FactsKeepLast: a.cfg.FactsKeepLast}
	st, err := context.NewStrategy(name, opts)
	if err != nil {
		return err
	}
	if a.strategy != nil {
		_ = a.strategy.Reset()
	}
	a.strategy = st
	a.wireStrategyEvents(st)
	a.wireWorkingView(st)
	return nil
}

// Say принимает реплику пользователя, отправляет её (вместе с историей и слоями
// памяти) в LLM, получает ответ и сохраняет оба сообщения в память. Возвращает
// текст ответа и метаданные usage.
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
	// ветка; иначе — полная история из short-слоя).
	hist, err := a.strategyHist()
	if err != nil {
		return nil, err
	}

	// Собираем сообщения запроса: история (стратегия) + слои памяти + профиль.
	msgs := a.prepareMessages(hist, input)

	histTokens := MessagesTokens(hist)

	res, err := a.client.ChatResult(msgs, &llm.Options{MaxTokens: a.cfg.MaxTokens})
	if err != nil {
		return nil, err
	}

	userMsg := llm.Message{Role: "user", Content: input}
	assistMsg := llm.Message{Role: "assistant", Content: res.Text}

	// Сохраняем ход в short-слой. Для Branching история ветки живёт в самой
	// стратегии, поэтому общую short-память не трогаем.
	if a.strategy == nil || a.strategy.Name() != "branch" {
		_ = a.memory.Short().Append(userMsg)
		_ = a.memory.Short().Append(assistMsg)
	}

	// Явный роутинг: ключевые факты из реплики → рабочий слой (данные задачи).
	// Долговременный слой пополняется отдельно (Remember / /remember).
	if added := a.memory.RouteUserTurn(input, a.extractor); added > 0 {
		a.emitEvent(context.StrategyEvent{Kind: context.EventLog,
			Text: fmt.Sprintf("память: %d новых фактов текущей задачи → рабочий слой", added)})
	}

	// Даём стратегии обновить внутреннее состояние после хода.
	if a.strategy != nil {
		observeHist, _ := a.strategyHist()
		if a.strategy.Name() == "branch" {
			// Для ветвления подставляем ход в историю активной ветки явно,
			// т.к. общая память не пополнялась.
			observeHist = append(cloneAgentMsgs(observeHist), userMsg, assistMsg)
		}
		_ = a.strategy.Observe(observeHist)
	}

	// Забираем события стратегии за этот ход.
	a.mu.Lock()
	events := append([]context.StrategyEvent{}, a.currentEvents...)
	a.mu.Unlock()

	return &Reply{Text: res.Text, Usage: res, Stats: a.buildStats(res, histTokens), Events: events}, nil
}

// prepareMessages собирает сообщения запроса в порядке:
// [профиль] → [long-память] → [working-память] → история (стратегия) → ввод.
// Приоритет истории: активная стратегия → legacy-сжатие → вся история как есть.
func (a *Agent) prepareMessages(hist []llm.Message, input string) []llm.Message {
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
	// Инжекция слоёв памяти: system-блок long + system-блок working перед историей.
	msgs = a.memory.Prepend(msgs)
	// Состояние задачи (FSM): текущий этап/шаг/ожидаемое действие — чтобы агент
	// продолжал задачу без повторных объяснений. Идёт выше памяти, но ниже профиля.
	if a.tasks != nil {
		if block := a.tasks.SystemBlock(); block != "" {
			msgs = append([]llm.Message{{Role: "system", Content: block}}, msgs...)
		}
	}
	// Персонализация: активный профиль — самый первый system-блок (приоритет над памятью).
	if a.activeProfile != nil {
		if block := a.activeProfile.SystemBlock(); block != "" {
			msgs = append([]llm.Message{{Role: "system", Content: block}}, msgs...)
		}
	}
	return msgs
}

// strategyHist возвращает историю, релевантную для активной стратегии.
func (a *Agent) strategyHist() ([]llm.Message, error) {
	if a.strategy != nil {
		return a.strategy.History(a.memory.Short()), nil
	}
	return a.memory.Short().Load()
}

// cloneAgentMsgs — локальная копия глубокого клонирования слайса сообщений
// (дублирует context.cloneMsgs, недоступный извне пакета).
func cloneAgentMsgs(in []llm.Message) []llm.Message {
	out := make([]llm.Message, len(in))
	copy(out, in)
	return out
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

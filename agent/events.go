package agent

// StrategyEvent — событие стратегии управления контекстом, возникающее во время
// одного хода диалога (Build/Observe). Показывается в веб-чате как живой лог,
// чтобы было видно, что делает стратегия и что произойдёт дальше.
type StrategyEvent struct {
	Kind string `json:"kind"` // "log" | "warn" | "predict"
	Text string `json:"text"`
}

// Константы видов событий стратегий.
const (
	EventLog     = "log"     // что произошло сейчас
	EventWarn    = "warn"    // внимание / граничная ситуация
	EventPredict = "predict" // предсказание: «скоро произойдёт …»
)

// EventSinkFunc — функция приёма события стратегии.
type EventSinkFunc func(ev StrategyEvent)

// EventAwareStrategy — стратегия, способная сообщать о своих внутренних событиях.
// Агент вызывает SetEventSink при создании/переключении стратегии, а затем
// стратегия эмитит события во время Build()/Observe().
type EventAwareStrategy interface {
	SetEventSink(fn EventSinkFunc)
}

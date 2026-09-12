package agent

import (
	"fmt"

	"aichallenge/llm"
)

// SlidingWindow — стратегия контекста «скользящее окно» (Стратегия 1).
//
// Хранит только последние N сообщений, всё старше — отбрасывается на уровне
// запроса. Память продолжает хранить полную историю; стратегия просто режет
// слайс в Build(). Самый дешёвый по токенам вариант, но ранние детали диалога
// (цель/дедлайн) теряются, если история длиннее окна.
type SlidingWindow struct {
	size int           // сколько последних сообщений отправлять в запрос
	sink EventSinkFunc // приёмник событий стратегии (nil — события не шлются)
}

// NewSlidingWindow создаёт окно заданного размера (по умолчанию 10).
func NewSlidingWindow(size int) *SlidingWindow {
	if size <= 0 {
		size = 10
	}
	return &SlidingWindow{size: size}
}

// Name возвращает имя стратегии.
func (w *SlidingWindow) Name() string { return "window" }

// SetEventSink подключает приёмник событий (реализация EventAwareStrategy).
func (w *SlidingWindow) SetEventSink(fn EventSinkFunc) { w.sink = fn }

// emit отправляет событие стратегии, если подключён приёмник.
func (w *SlidingWindow) emit(kind, text string) {
	if w.sink != nil {
		w.sink(StrategyEvent{Kind: kind, Text: text})
	}
}

// History возвращает всю историю из памяти (стратегия ничего своего не хранит).
func (w *SlidingWindow) History(mem Memory) []llm.Message {
	hist, _ := mem.Load()
	return hist
}

// Build берёт последние N сообщений истории + текущий ввод пользователя,
// попутно сообщая о том, что происходит с окном и что произойдёт дальше.
func (w *SlidingWindow) Build(hist []llm.Message, input string) []llm.Message {
	n := len(hist)
	if n > w.size {
		drop := n - w.size
		w.emit(EventLog, fmt.Sprintf("окно %d: отбрасываю %d старых сообщений, оставляю последние %d", w.size, drop, w.size))
		hist = hist[n-w.size:]
		// После этого хода в память добавится user+assistant.
		if n+2 > w.size {
			w.emit(EventPredict, "скоро: при следующем ходе история снова превысит окно — старые сообщения будут отброшены")
		}
	} else {
		w.emit(EventLog, fmt.Sprintf("окно %d: история %d сообщений — всё помещается в запрос", w.size, n))
		if n+2 > w.size {
			w.emit(EventPredict, fmt.Sprintf("скоро: после этого хода история станет %d, превысит окно %d — начну отбрасывать старые сообщения", n+2, w.size))
		}
	}
	msgs := append(append([]llm.Message{}, hist...), llm.Message{Role: "user", Content: input})
	return msgs
}

// Observe — no-op: скользящее окно не ведёт собственного состояния.
func (w *SlidingWindow) Observe(hist []llm.Message) error { return nil }

// Reset — no-op.
func (w *SlidingWindow) Reset() error { return nil }

// State возвращает параметры окна (для отладки/сравнения).
func (w *SlidingWindow) State() string { return fmt.Sprintf("window size=%d", w.size) }

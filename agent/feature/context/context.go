package context

import (
	"fmt"

	"aichallenge/llm"

	"agent/feature/dialog"
)

// ContextStrategy — интерфейс переключаемых стратегий управления контекстом
// (без сжатия-сводки). Каждая стратегия получает полную историю из short-term
// памяти (dialog.Memory), но сама решает, какие сообщения отправить в LLM.
//
// Многослойная память (working/long) инжектируется агентом отдельно; здесь —
// только диалоговое окно.
type ContextStrategy interface {
	// Name возвращает имя стратегии (window | facts | branch).
	Name() string
	// History возвращает историю, которую стратегия считает релевантной для
	// текущего состояния. Для SlidingWindow/Facts — это mem.Load(); для
	// Branching — история активной ветки (ветки живут в самой стратегии).
	History(mem dialog.Memory) []llm.Message
	// Build готовит сообщения запроса: история + новое сообщение пользователя.
	Build(hist []llm.Message, input string) []llm.Message
	// Observe вызывается после сохранения хода (user+assistant), чтобы стратегия
	// могла обновить внутреннее состояние. hist — полная релевантная история ПОСЛЕ хода.
	Observe(hist []llm.Message) error
	// Reset сбрасывает внутреннее состояние стратегии.
	Reset() error
	// State возвращает снапшот состояния стратегии (для отладки/сравнения).
	State() string
}

// Options — параметры стратегий контекста, которые агент берёт из своего Config.
// Вынесено отдельно, чтобы пакет context не зависел от конфига ядра (без циклов).
type Options struct {
	WindowSize    int // SlidingWindow: сколько последних сообщений слать
	FactsKeepLast int // Facts: сколько последних сообщений слать вместе с рабочим контекстом
}

// StrategyNames — список доступных стратегий (для селектора в UI/CLI).
var StrategyNames = []string{"window", "facts", "branch"}

// NewStrategy создаёт стратегию по имени. Поддерживаемые имена:
// window | facts | branch. Невалидное имя — ошибка.
func NewStrategy(name string, opts Options) (ContextStrategy, error) {
	switch name {
	case "window":
		return NewSlidingWindow(opts.WindowSize), nil
	case "facts":
		return NewFactsMemory(opts.FactsKeepLast), nil
	case "branch":
		return NewBranching(), nil
	default:
		return nil, fmt.Errorf("неизвестная стратегия контекста %q (ожидается window | facts | branch)", name)
	}
}

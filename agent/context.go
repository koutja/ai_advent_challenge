package agent

import (
	"fmt"

	"aichallenge/llm"
)

// ContextStrategy — интерфейс переключаемых стратегий управления контекстом
// (без сжатия-сводки). Каждая стратегия получает полную историю из Memory, но
// сама решает, какие сообщения отправить в LLM и как вести собственное состояние.
//
// Подробный дизайн — в plans/context-management-plan.md.
type ContextStrategy interface {
	// Name возвращает имя стратегии (window | facts | branch).
	Name() string
	// History возвращает историю, которую стратегия считает релевантной для
	// текущего состояния. Для SlidingWindow/Facts — это mem.Load(); для
	// Branching — история активной ветки (ветки живут в самой стратегии).
	History(mem Memory) []llm.Message
	// Build готовит сообщения запроса: история + новое сообщение пользователя.
	Build(hist []llm.Message, input string) []llm.Message
	// Observe вызывается после сохранения хода (user+assistant), чтобы стратегия
	// могла обновить внутреннее состояние (например, извлечь факты или дописать
	// ход в активную ветку). hist — полная релевантная история ПОСЛЕ хода.
	Observe(hist []llm.Message) error
	// Reset сбрасывает внутреннее состояние стратегии.
	Reset() error
	// State возвращает снапшот состояния стратегии (для отладки/сравнения).
	State() string
}

// NewStrategy создаёт стратегию по имени. Поддерживаемые имена:
// window | facts | branch. Невалидное имя — ошибка.
func NewStrategy(name string, client *llm.Client, cfg *Config) (ContextStrategy, error) {
	switch name {
	case "window":
		return NewSlidingWindow(cfg.WindowSize), nil
	case "facts":
		mode := cfg.FactsExtractor
		if mode == "" {
			mode = ExtractorLLM
		}
		return NewFactsMemory(client, cfg.FactsKeepLast, cfg.FactsKeyMax, mode), nil
	case "branch":
		return NewBranching(), nil
	default:
		return nil, fmt.Errorf("неизвестная стратегия контекста %q (ожидается window | facts | branch)", name)
	}
}

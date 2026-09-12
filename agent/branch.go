package agent

import (
	"fmt"
	"sort"
	"strings"

	"aichallenge/llm"
)

// Branching — стратегия контекста «ветвление» (Стратегия 3).
//
// Владеет собственными копиями истории по веткам (branches map[id]→[]Message)
// и активной веткой. Позволяет сохранять контрольные точки (Checkpoint),
// создавать ветки-копии (Branch) и переключаться между ними (Switch).
// Полная история ветки → максимальная точность и стабильность, но самый высокий
// расход токенов.
type Branching struct {
	branches map[string][]llm.Message // id ветки -> история
	active   string                   // активная ветка
	nextID   int                      // счётчик для авто-имён (cpN/brN)
	sink     EventSinkFunc            // приёмник событий стратегии (nil — события не шлются)
}

// NewBranching создаёт стратегию с начальной веткой "main".
func NewBranching() *Branching {
	return &Branching{
		branches: map[string][]llm.Message{"main": {}},
		active:   "main",
		nextID:   1,
	}
}

// Name возвращает имя стратегии.
func (b *Branching) Name() string { return "branch" }

// SetEventSink подключает приёмник событий (реализация EventAwareStrategy).
func (b *Branching) SetEventSink(fn EventSinkFunc) { b.sink = fn }

// emit отправляет событие стратегии, если подключён приёмник.
func (b *Branching) emit(kind, text string) {
	if b.sink != nil {
		b.sink(StrategyEvent{Kind: kind, Text: text})
	}
}

// History возвращает историю активной ветки (не трогая общую Memory).
func (b *Branching) History(mem Memory) []llm.Message {
	return cloneMsgs(b.branches[b.active])
}

// Build собирает запрос: полная история активной ветки + ввод пользователя.
func (b *Branching) Build(hist []llm.Message, input string) []llm.Message {
	b.emit(EventLog, fmt.Sprintf("branch: ветка %q, отправляю полную историю (%d сообщений) — максимум точности", b.active, len(hist)))
	return append(cloneMsgs(hist), llm.Message{Role: "user", Content: input})
}

// Observe аппендит ход (уже включённый в hist) в активную ветку.
// При вызове из Agent.Say hist = активная ветка + только что добавленный ход.
func (b *Branching) Observe(hist []llm.Message) error {
	b.branches[b.active] = cloneMsgs(hist)
	return nil
}

// Reset очищает все ветки и возвращается к единственной "main".
func (b *Branching) Reset() error {
	b.branches = map[string][]llm.Message{"main": {}}
	b.active = "main"
	b.nextID = 1
	return nil
}

// State возвращает сводку состояния (активная ветка + список веток).
func (b *Branching) State() string {
	return fmt.Sprintf("active=%s branches=[%s]", b.active, strings.Join(b.Branches(), ","))
}

// Active возвращает id активной ветки.
func (b *Branching) Active() string { return b.active }

// Branches возвращает отсортированный список id всех веток.
func (b *Branching) Branches() []string {
	keys := make([]string, 0, len(b.branches))
	for k := range b.branches {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Checkpoint сохраняет снапшот активной ветки под именем name (контрольная точка)
// и переключает активность на неё. Если name пустое — генерируется cpN.
func (b *Branching) Checkpoint(name string) error {
	if name == "" {
		name = fmt.Sprintf("cp%d", b.nextID)
		b.nextID++
	}
	b.branches[name] = cloneMsgs(b.branches[b.active])
	b.active = name
	return nil
}

// Branch создаёт новую ветку-копию от активной (или от указанной контрольной точки)
// под именем name и переключается на неё. Если name пустое — генерируется brN.
func (b *Branching) Branch(name string) error {
	if name == "" {
		name = fmt.Sprintf("br%d", b.nextID)
		b.nextID++
	}
	b.branches[name] = cloneMsgs(b.branches[b.active])
	b.active = name
	return nil
}

// Switch переключает активную ветку по имени. Ошибка, если ветки нет.
func (b *Branching) Switch(name string) error {
	if _, ok := b.branches[name]; !ok {
		return fmt.Errorf("ветки %q нет (доступны: %s)", name, strings.Join(b.Branches(), ", "))
	}
	b.active = name
	return nil
}

// cloneMsgs возвращает глубокую копию слайса сообщений.
func cloneMsgs(in []llm.Message) []llm.Message {
	out := make([]llm.Message, len(in))
	copy(out, in)
	return out
}

package agent

import (
	stdctx "context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"aichallenge/llm"

	"agent/feature/rag"
)

// reviewSystemPrompt — системный промпт ревьюера кода.
const reviewSystemPrompt = `Ты — опытный ревьюер кода в репозитории AI Advent Challenge. Проведи ревью предоставленного diff и верни структурированный отчёт строго с тремя секциями:

### Потенциальные баги
- ...

### Архитектурные проблемы
- ...

### Рекомендации
- ...

Правила:
1. Опирайся ТОЛЬКО на предоставленный diff и контекст проекта.
2. Каждый пункт конкретный и actionable (файл/строка/что исправить).
3. Не выдумывай проблемы, которых нет в diff.
4. Если проблем в секции нет — напиши «Проблем не найдено».
5. Отвечай на русском, кратко и по делу.`

// reviewSections — обязательные секции отчёта ревью (в порядке вывода).
var reviewSections = []string{
	"Потенциальные баги",
	"Архитектурные проблемы",
	"Рекомендации",
}

// ReviewResult — результат ревью кода.
type ReviewResult struct {
	Text            string      // полный текст отчёта от LLM
	Bugs            []string    // секция «Потенциальные баги»
	Architecture    []string    // секция «Архитектурные проблемы»
	Recommendations []string    // секция «Рекомендации»
	FormatOK        bool        // все три секции найдены
	Sources         []rag.Chunk // RAG-контекст (конвенции проекта)
	Engine          string      // движок ретрива: sidecar|keyword
	Usage           *llm.Result
	Stats           *TokenStats
}

// Review — AI-ревью кода: изменения (diff + список файлов) + RAG-контекст
// проекта (README/docs/код — конвенции) → LLM-ревью со строгими секциями.
//
// diff — текст изменений (git diff, уже ограниченный по строкам), files —
// список изменённых файлов (name-status), используется как запрос в RAG.
// Ход не сохраняется в память (stateless).
func (a *Agent) Review(diff, files string) (*ReviewResult, error) {
	diff = strings.TrimSpace(diff)
	if diff == "" {
		return nil, errors.New("пустой diff: нечего ревьюить")
	}
	if a.rag == nil {
		return nil, errors.New("RAG не настроен: добавьте секцию rag в config.json")
	}
	if a.client == nil {
		return nil, errors.New("LLM-клиент не инициализирован")
	}

	// RAG: конвенции проекта по запросу на основе изменённых файлов.
	result, err := a.rag.Retrieve(stdctx.Background(), reviewQuery(files))
	if err != nil {
		return nil, fmt.Errorf("review: ретрив контекста: %w", err)
	}
	contextText := rag.BuildContext(result.Chunks, a.rag.Config().MaxContextChars)

	// Сообщения: system (ревью-промпт + контекст проекта) → user (diff).
	msgs := []llm.Message{
		{Role: "system", Content: reviewSystemPrompt +
			"\n\nКонтекст проекта (конвенции, документация):\n" + strings.TrimSpace(contextText)},
		{Role: "user", Content: reviewUserPrompt(files, diff)},
	}

	res, err := a.client.ChatResult(msgs, &llm.Options{MaxTokens: a.cfg.MaxTokens})
	if err != nil {
		return nil, err
	}

	rr := &ReviewResult{
		Text:    res.Text,
		Sources: result.Chunks,
		Engine:  result.Engine,
		Usage:   res,
	}
	rr.Bugs, rr.Architecture, rr.Recommendations, rr.FormatOK = parseReviewSections(res.Text)

	// Статистика токенов/стоимости (аналогично SayChat).
	st := &TokenStats{Model: a.client.Model(), ContextWindow: a.cfg.ContextWindow}
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
	rr.Stats = st

	return rr, nil
}

// reviewQuery строит RAG-запрос по изменённым файлам (конвенции проекта).
func reviewQuery(files string) string {
	f := strings.TrimSpace(files)
	if f == "" {
		return "конвенции и структура репозитория"
	}
	const maxFiles = 10
	lines := nonEmptyReviewLines(f)
	if len(lines) > maxFiles {
		lines = lines[:maxFiles]
	}
	return "конвенции и правила проекта для изменённых файлов: " + strings.Join(lines, ", ")
}

// reviewUserPrompt собирает запрос пользователя: файлы + diff.
func reviewUserPrompt(files, diff string) string {
	var sb strings.Builder
	sb.WriteString("Изменённые файлы:\n")
	if strings.TrimSpace(files) == "" {
		sb.WriteString("(не указаны)\n")
	} else {
		sb.WriteString(strings.TrimSpace(files) + "\n")
	}
	sb.WriteString("\nDiff:\n```diff\n")
	sb.WriteString(diff)
	sb.WriteString("\n```")
	return sb.String()
}

// nonEmptyReviewLines разбивает текст на непустые строки.
func nonEmptyReviewLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

// parseReviewSections разбирает отчёт ревью на секции по заголовкам
// «### Потенциальные баги» / «### Архитектурные проблемы» / «### Рекомендации».
// Возвращает списки пунктов и флаг FormatOK — все три секции найдены.
func parseReviewSections(text string) (bugs, arch, recs []string, ok bool) {
	type secPos struct {
		start int // начало содержимого секции (конец строки заголовка)
		end   int // начало следующего заголовка (его маркер #)
	}
	pos := make(map[string]secPos, len(reviewSections))
	for _, sec := range reviewSections {
		re := regexp.MustCompile(`(?im)^#{1,6}\s*` + regexp.QuoteMeta(sec) + `\s*:?\s*$`)
		loc := re.FindStringIndex(text)
		if loc == nil {
			continue
		}
		start := loc[1]
		if lineEnd := strings.IndexByte(text[loc[1]:], '\n'); lineEnd >= 0 {
			start = loc[1] + lineEnd
		}
		pos[sec] = secPos{start: start, end: loc[0]}
	}
	if len(pos) < len(reviewSections) {
		return nil, nil, nil, false
	}

	// Содержимое секции — до начала заголовка следующей (маркер # не попадает).
	order := append([]string{}, reviewSections...)
	sort.SliceStable(order, func(i, j int) bool { return pos[order[i]].end < pos[order[j]].end })

	sections := make(map[string][]string, len(reviewSections))
	for i, sec := range order {
		start := pos[sec].start
		end := len(text)
		if i+1 < len(order) {
			end = pos[order[i+1]].end
		}
		sections[sec] = bullets(text[start:end])
	}
	return sections[reviewSections[0]], sections[reviewSections[1]], sections[reviewSections[2]], true
}

// bullets извлекает пункты из содержимого секции (строки с "-"/"•"/"*").
func bullets(s string) []string {
	out := []string{}
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		ln = strings.TrimLeft(ln, "-•*")
		ln = strings.TrimSpace(ln)
		if ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"aichallenge/llm"
)

// filePlanPrompt — промпт для построения плана файловых операций.
const filePlanPrompt = `Ты — ассистент, выполняющий задачи с файлами репозитория. Построй план для цели пользователя. Верни ТОЛЬКО JSON без пояснений:

{
  "scenario": "find|generate|check|docs|diff",
  "search": [{"pattern": "regexp", "path": "каталог или пусто", "file_pattern": "regexp или пусто"}],
  "read": ["путь/файл"],
  "output_file": "путь/файл или пусто"
}

Сценарии:
- find — найти все места использования чего-либо (search обязателен);
- generate — сгенерировать новый файл (README/ADR/changelog; output_file обязателен);
- check — проверить соответствие правилам/инвариантам (read обязателен);
- docs — обновить документацию по изменениям кода (read + output_file);
- diff — подготовить список изменений (search/read опциональны).

Правила:
- search.pattern — регулярное выражение (например "llm\\.New");
- read — пути относительно корня репозитория;
- output_file — путь относительно корня (только для generate/docs);
- Не выдумывай пути: используй известные каталоги (agent/, llm/, day_01/...).`

// fileGeneratePrompt — промпт для подготовки итогового результата.
const fileGeneratePrompt = `Ты — ассистент, выполняющий задачи с файлами репозитория. По цели, плану и собранному контексту (файлы, совпадения) подготовь итоговый результат:
- для сценариев find/diff/check — отчёт в Markdown (заголовок, список файлов/строк, выводы);
- для generate/docs — содержимое нового/обновлённого файла (README/ADR/changelog/документация) в Markdown, БЕЗ пояснений вокруг.
Отвечай на русском. Опирайся только на предоставленный контекст.`

// FileSearch — один поиск в плане.
type FileSearch struct {
	Pattern     string
	Path        string
	FilePattern string
}

// FilePlan — план файловых операций.
type FilePlan struct {
	Scenario   string
	Searches   []FileSearch
	Reads      []string
	OutputFile string
}

// FilePlanResult — результат планирования.
type FilePlanResult struct {
	Plan     *FilePlan
	Raw      string // сырой ответ LLM (для отладки)
	Fallback bool   // план получен fallback-парсером (LLM не вернул JSON)
}

// FilePlan строит план файловых операций по цели: LLM возвращает JSON-план,
// при невалидном JSON — детерминированный fallback по ключевым словам.
func (a *Agent) FilePlan(goal string) (*FilePlanResult, error) {
	if a.client == nil {
		return nil, errors.New("LLM-клиент не инициализирован")
	}
	g := strings.TrimSpace(goal)
	if g == "" {
		return nil, errors.New("пустая цель")
	}
	msgs := []llm.Message{
		{Role: "system", Content: filePlanPrompt},
		{Role: "user", Content: "Цель: " + g},
	}
	res, err := a.client.ChatResult(msgs, &llm.Options{MaxTokens: 300})
	if err != nil {
		return nil, err
	}
	if plan, ok := parseFilePlan(res.Text); ok && planLooksValid(plan) {
		return &FilePlanResult{Plan: plan, Raw: res.Text}, nil
	}
	// LLM не вернул валидный JSON-план (или «эхо-промпта») — детерминированный fallback.
	return &FilePlanResult{Plan: fallbackFilePlan(g), Raw: res.Text, Fallback: true}, nil
}

// planLooksValid — план не является «эхом промпта» (мусорный сценарий).
func planLooksValid(plan *FilePlan) bool {
	if strings.Contains(plan.Scenario, "|") {
		return false
	}
	switch plan.Scenario {
	case "find", "check", "diff", "generate", "docs":
		return true
	}
	return false
}

// FileGenerate готовит итоговый результат (отчёт или содержимое файла) по цели,
// плану и собранному контексту.
func (a *Agent) FileGenerate(goal string, plan *FilePlan, context string) (string, error) {
	if a.client == nil {
		return "", errors.New("LLM-клиент не инициализирован")
	}
	msgs := []llm.Message{
		{Role: "system", Content: fileGeneratePrompt},
		{Role: "user", Content: fileGenerateUser(goal, plan, context)},
	}
	res, err := a.client.ChatResult(msgs, &llm.Options{MaxTokens: a.cfg.MaxTokens})
	if err != nil {
		return "", err
	}
	return res.Text, nil
}

// fileGenerateUser собирает запрос пользователя для генерации результата.
func fileGenerateUser(goal string, plan *FilePlan, context string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Цель: %s\n", goal)
	fmt.Fprintf(&sb, "Сценарий: %s\n", plan.Scenario)
	if plan.OutputFile != "" {
		fmt.Fprintf(&sb, "Файл для записи: %s\n", plan.OutputFile)
	}
	sb.WriteString("\nКонтекст (файлы и совпадения):\n")
	if strings.TrimSpace(context) == "" {
		sb.WriteString("(контекст пуст)\n")
	} else {
		sb.WriteString(strings.TrimSpace(context) + "\n")
	}
	return sb.String()
}

// parseFilePlan разбирает JSON-план из ответа LLM (ищет блок между { и }).
func parseFilePlan(text string) (*FilePlan, bool) {
	start := strings.Index(text, "{")
	if start < 0 {
		return nil, false
	}
	end := strings.LastIndex(text, "}")
	if end <= start {
		return nil, false
	}
	var raw struct {
		Scenario string `json:"scenario"`
		Search   []struct {
			Pattern     string `json:"pattern"`
			Path        string `json:"path"`
			FilePattern string `json:"file_pattern"`
		} `json:"search"`
		Read       []string `json:"read"`
		OutputFile string   `json:"output_file"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err != nil {
		return nil, false
	}
	if strings.TrimSpace(raw.Scenario) == "" {
		return nil, false
	}
	plan := &FilePlan{Scenario: raw.Scenario, Reads: raw.Read, OutputFile: raw.OutputFile}
	for _, s := range raw.Search {
		plan.Searches = append(plan.Searches, FileSearch{
			Pattern: s.Pattern, Path: s.Path, FilePattern: s.FilePattern,
		})
	}
	return plan, true
}

// fallbackFilePlan — детерминированный план по ключевым словам цели.
func fallbackFilePlan(goal string) *FilePlan {
	g := strings.ToLower(goal) // для определения сценария
	plan := &FilePlan{Scenario: "find"}
	switch {
	case strings.Contains(g, "changelog") || strings.Contains(g, "readme") ||
		strings.Contains(g, "adr") || strings.Contains(g, "сгенерир"):
		plan.Scenario = "generate"
	case strings.Contains(g, "провер") || strings.Contains(g, "инвариант") ||
		strings.Contains(g, "правил"):
		plan.Scenario = "check"
	case strings.Contains(g, "документац") || strings.Contains(g, "обнов"):
		plan.Scenario = "docs"
	case strings.Contains(g, "diff") || strings.Contains(g, "изменени") ||
		strings.Contains(g, "список"):
		plan.Scenario = "diff"
	}
	if plan.Scenario == "generate" || plan.Scenario == "docs" {
		if strings.Contains(g, "changelog") {
			plan.OutputFile = "agent/docs/changelog.md"
		} else {
			plan.OutputFile = "agent/docs/generated.md"
		}
	}
	// Поиск по ключевым словам цели: сначала идентификаторы с точкой (llm.New),
	// затем остальные слова без стоп-слов (до 2). Регистр исходной цели сохраняем.
	words := strings.Fields(goal)
	var pats []string
	for _, w := range words {
		if fileStopWords[w] || len(w) <= 3 {
			continue
		}
		if strings.Contains(w, ".") {
			pats = append(pats, regexp.QuoteMeta(w))
			if len(pats) >= 2 {
				break
			}
		}
	}
	if len(pats) == 0 {
		for _, w := range words {
			if fileStopWords[w] || len(w) <= 3 {
				continue
			}
			pats = append(pats, regexp.QuoteMeta(w))
			if len(pats) >= 2 {
				break
			}
		}
	}
	if len(pats) > 0 {
		plan.Searches = append(plan.Searches, FileSearch{Pattern: strings.Join(pats, "|")})
	}
	return plan
}

// fileStopWords — стоп-слова, не несущие поискового смысла (для fallback-плана).
var fileStopWords = map[string]bool{
	"найти": true, "все": true, "места": true, "использования": true,
	"используется": true, "использовании": true, "использование": true,
	"сгенерировать": true, "генерация": true, "генерации": true,
	"changelog": true, "readme": true, "adr": true,
	"проверить": true, "соответствие": true, "файлов": true, "правилам": true,
	"инвариантам": true, "инварианты": true, "правила": true,
	"обновить": true, "документацию": true, "документации": true,
	"документация": true, "изменениям": true, "изменениях": true,
	"кода": true, "код": true, "подготовить": true, "список": true,
	"изменений": true, "последним": true, "коммитам": true,
	"по": true, "для": true, "и": true, "в": true, "на": true, "с": true,
	"из": true, "как": true, "что": true, "где": true, "проекта": true,
	"репозитория": true, "файле": true, "файлах": true, "файл": true,
	"файла": true, "компонента": true, "api": true, "основе": true,
	"новый": true, "нового": true, "обновления": true,
}

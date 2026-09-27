package mcpx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// defaultOutputDir — куда save_to_file пишет файлы (git игнорирует **/results/).
const defaultOutputDir = "results/pipeline"

// Doc — документ локального корпуса для инструмента search.
type Doc struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// corpus — детерминированный набор документов (без сети и LLM).
var corpus = []Doc{
	{ID: "doc-001", Title: "Что такое MCP", Snippet: "Model Context Protocol — открытый стандарт для подключения инструментов к LLM-агентам."},
	{ID: "doc-002", Title: "Go SDK для MCP", Snippet: "Официальный Go SDK позволяет писать MCP-серверы и клиенты на языке Go."},
	{ID: "doc-003", Title: "Планировщик в агенте", Snippet: "Планировщик выполняет отложенные и периодические задачи по расписанию."},
	{ID: "doc-004", Title: "Композиция инструментов", Snippet: "Пайплайн из нескольких MCP-инструментов передаёт данные от шага к шагу."},
	{ID: "doc-005", Title: "Агрегация метрик", Snippet: "Сводка считает count, min, max, avg и latest по собранным точкам данных."},
	{ID: "doc-006", Title: "Хранение результатов", Snippet: "Результаты сохраняются в JSON или файлы в папке results, игнорируемой git."},
	{ID: "doc-007", Title: "Зачем нужен Go (Golang)", Snippet: "Go (Golang) нужен для быстрых серверов, консольных утилит и инструментов вроде этого агента."},
	{ID: "doc-008", Title: "Как подключить MCP-инструменты", Snippet: "Чтобы подключить инструменты, соберите mcp-server, запустите агента и вызовите /mcp-call."},
	{ID: "doc-009", Title: "JavaScript и TypeScript", Snippet: "JavaScript и TypeScript используются в веб-приложениях, в том числе в интерфейсе этого агента."},
	{ID: "doc-010", Title: "Python для скриптов", Snippet: "Python удобен для скриптов, анализа данных и прототипирования инструментов."},
	{ID: "doc-011", Title: "SQLite как хранилище", Snippet: "SQLite хранит историю и память агента в одном файле без отдельного сервера."},
}

// SearchResult — результат инструмента search.
type SearchResult struct {
	Query string `json:"query"`
	Total int    `json:"total"`
	Docs  []Doc  `json:"docs"`
}

// allDocs возвращает статический корпус плюс сгенерированные документы
// (файл MCP_CORPUS_FILE / results/pipeline/corpus.json).
func allDocs() []Doc {
	docs := make([]Doc, 0, len(corpus)+8)
	docs = append(docs, corpus...)
	if raw, err := os.ReadFile(corpusFilePath()); err == nil {
		var f struct {
			Docs []GeneratedDoc `json:"docs"`
		}
		if json.Unmarshal(raw, &f) == nil {
			for _, g := range f.Docs {
				docs = append(docs, Doc{ID: g.ID, Title: g.Title, Snippet: g.Snippet})
			}
		}
	}
	return docs
}

// Search ищет документы по запросу: запрос разбивается на слова (токены),
// каждый токен ищется в заголовке/сниппете документа регистронезависимо.
// Документы ранжируются по числу совпавших токенов — больше совпадений выше.
// Пустой запрос возвращает все документы. Возвращает не более limit результатов.
func Search(query string, limit int) SearchResult {
	if limit <= 0 {
		limit = 5
	}
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	res := SearchResult{Query: query}

	type hit struct {
		doc   Doc
		score int
	}
	var hits []hit
	for _, d := range allDocs() {
		hay := strings.ToLower(d.Title + " " + d.Snippet)
		if len(tokens) == 0 {
			hits = append(hits, hit{doc: d})
			continue
		}
		score := 0
		for _, tok := range tokens {
			if strings.Contains(hay, tok) {
				score++
			}
		}
		if score > 0 {
			hits = append(hits, hit{doc: d, score: score})
		}
	}
	// Ни одного точного совпадения — пробуем префиксное (словоформы).
	if len(hits) == 0 {
		for i, d := range prefixHits(tokens) {
			hits = append(hits, hit{doc: d, score: len(tokens) - i})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	for _, h := range hits {
		res.Docs = append(res.Docs, h.doc)
	}
	res.Total = len(res.Docs)
	return res
}

// словоТрим убирает пунктуацию с краёв слова.
func trimWord(w string) string { return strings.Trim(w, ".,!?()«»\"'`:-") }

// prefixHits — второй проход: если точных совпадений нет, ищем по префиксу слов,
// чтобы поймать словоформы («инструмент» → «инструментов», «скрипт» → «скриптов»).
//
// Важно: и токен, и слово должны быть длиной >= 3, иначе короткие слова ложно
// совпадают по префиксу (например, предлог «с» является префиксом «сЕРИАЛ»).
func prefixHits(tokens []string) []Doc {
	var docs []Doc
	for _, d := range allDocs() {
		words := strings.Fields(strings.ToLower(d.Title + " " + d.Snippet))
		score := 0
		for _, tok := range tokens {
			if len(tok) < 3 {
				continue
			}
			for _, w := range words {
				w = trimWord(w)
				if len(w) < 3 {
					continue
				}
				if strings.HasPrefix(w, tok) || strings.HasPrefix(tok, w) {
					score++
					break
				}
			}
		}
		if score > 0 {
			docs = append(docs, d)
		}
	}
	return docs
}

// SummarizeResult — результат инструмента summarize.
type SummarizeResult struct {
	Summary string `json:"summary"`
	Words   int    `json:"words"`
	Sources int    `json:"sources"`
}

// Summarize строит детерминированную сводку: берёт первые предложения текста,
// пока суммарно не наберёт max_words слов (по умолчанию 40). Sources — число
// непустых абзацев во входном тексте (для пайплайна это количество документов).
func Summarize(text string, maxWords int) SummarizeResult {
	if maxWords <= 0 {
		maxWords = 40
	}
	sources := 0
	for _, p := range strings.Split(text, "\n\n") {
		if strings.TrimSpace(p) != "" {
			sources++
		}
	}

	sentences := splitSentences(text)
	var parts []string
	words := 0
	for _, s := range sentences {
		n := countWords(s)
		if n == 0 {
			continue
		}
		if words+n > maxWords && len(parts) > 0 {
			break
		}
		parts = append(parts, s)
		words += n
		if words >= maxWords {
			break
		}
	}
	sum := strings.Join(parts, " ")
	if sum == "" { // fallback: первые max_words слов как есть
		fields := strings.Fields(text)
		if len(fields) > maxWords {
			fields = fields[:maxWords]
		}
		sum = strings.Join(fields, " ")
	}
	return SummarizeResult{Summary: sum, Words: countWords(sum), Sources: sources}
}

// splitSentences разбивает текст на предложения (по . ! ? с пробелом).
func splitSentences(text string) []string {
	repl := strings.NewReplacer(". ", ".\n", "! ", "!\n", "? ", "?\n")
	var out []string
	for _, p := range strings.Split(repl.Replace(text), "\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func countWords(s string) int { return len(strings.Fields(s)) }

// SaveFileResult — результат инструмента save_to_file.
type SaveFileResult struct {
	Filename string `json:"filename"`
	Path     string `json:"path"`
	Bytes    int    `json:"bytes"`
}

// SaveFile пишет content в <dir>/<filename>, где dir — env MCP_OUTPUT_DIR или
// results/pipeline. Имя санитизируется (убираются пути и опасные символы).
func SaveFile(filename, content string) (SaveFileResult, error) {
	dir := os.Getenv("MCP_OUTPUT_DIR")
	if dir == "" {
		dir = defaultOutputDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SaveFileResult{}, fmt.Errorf("save_to_file: mkdir: %w", err)
	}
	name := sanitizeFilename(filename)
	if name == "" {
		return SaveFileResult{}, fmt.Errorf("save_to_file: имя файла пустое после санитизации")
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return SaveFileResult{}, fmt.Errorf("save_to_file: write: %w", err)
	}
	return SaveFileResult{Filename: name, Path: path, Bytes: len(content)}, nil
}

// sanitizeFilename оставляет только безопасные символы и отбрасывает пути
// (защита от path traversal: ../ и /).
func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "._- ")
}

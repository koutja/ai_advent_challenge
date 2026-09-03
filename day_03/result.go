package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Result — результат одной стратегии; сохраняется в JSON.
type Result struct {
	Method      string    `json:"method"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	DurationMS  int64     `json:"duration_ms"`
	OK          bool      `json:"ok"`
	Error       string    `json:"error,omitempty"`
	Result      string    `json:"result"`
	CombosFound int       `json:"combos_found"`
	Combos      []string  `json:"combos,omitempty"`
}

// resultsDir — каталог, куда складываются JSON-файлы результатов.
const resultsDir = "results"

func resultsFilePath(method string) string {
	return filepath.Join(resultsDir, method+".json")
}

// writeResult финализирует результат (время, длительность) и сохраняет JSON.
func writeResult(r *Result) error {
	r.FinishedAt = time.Now()
	r.DurationMS = r.FinishedAt.Sub(r.StartedAt).Milliseconds()
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(resultsFilePath(r.Method), data, 0o644)
}

// finishResult заполняет поля Result из ответа или ошибки и пишет JSON.
func finishResult(r *Result, out string, err error) error {
	if err != nil {
		r.OK = false
		r.Error = err.Error()
	} else {
		r.OK = true
		r.Result = out
		r.Combos = parseCombos(out)
		r.CombosFound = len(r.Combos)
	}
	return writeResult(r)
}

// loadResults читает все *.json из каталога results.
func loadResults() ([]Result, error) {
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("каталог %q не найден — сначала запустите стратегии (make run-all)", resultsDir)
		}
		return nil, err
	}
	var out []Result
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(resultsDir, e.Name()))
		if err != nil {
			return nil, err
		}
		var r Result
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("%s: %v", e.Name(), err)
		}
		// Пересчитываем комбинации из сохранённого текста — так отчёт не зависит
		// от того, каким парсером писался файл (устойчиво к старым JSON).
		if r.OK {
			r.Combos = parseCombos(r.Result)
			r.CombosFound = len(r.Combos)
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Method < out[j].Method })
	return out, nil
}

var (
	numRe   = regexp.MustCompile(`\d+`)
	tupleRe = regexp.MustCompile(`\(\s*(\d+)\s*[,;]\s*(\d+)\s*[,;]\s*(\d+)\s*\)`)
)

// drinkForms — словоформы названий напитков (модель пишет «чая», «чаем» и т.п.).
var drinkForms = map[string][]string{
	"кофе":  {"кофе"},
	"чай":   {"чай", "чая", "чаю", "чаем"},
	"какао": {"какао"},
}

// parseCombos извлекает список комбинаций из текста. Поддерживаются два формата:
//  1. «кофе X, чай Y, какао Z» (или «X кофе, Y чая, Z какао»);
//  2. кортеж (кофе, чай, какао) вида «(3, 2, 0)».
func parseCombos(text string) []string {
	// Модель часто выдаёт LaTeX-экранирование: `3,\ 2` — убираем обратные слэши,
	// чтобы `\ ` превратился в обычный пробел.
	text = strings.ReplaceAll(text, "\\", "")
	seen := map[string]bool{}
	var out []string
	add := func(norm string) {
		if norm == "" || seen[norm] {
			return
		}
		seen[norm] = true
		out = append(out, norm)
	}

	// Формат со словами напитков.
	for _, chunk := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == ';' }) {
		lower := strings.ToLower(chunk)
		if !mentionsDrink(lower, "кофе") && !mentionsDrink(lower, "какао") {
			continue
		}
		if !mentionsDrink(lower, "чай") {
			continue
		}
		add(normalizeComboLine(chunk))
	}

	// Кортежи (кофе, чай, какао) — с ',' или ';'.
	for _, m := range tupleRe.FindAllStringSubmatch(text, -1) {
		add("кофе " + m[1] + ", чай " + m[2] + ", какао " + m[3])
	}
	return out
}

// normalizeComboLine приводит строку к каноническому виду «кофе N, чай M, какао K».
func normalizeComboLine(line string) string {
	c, t, k := wordNum(line, "кофе"), wordNum(line, "чай"), wordNum(line, "какао")
	if c == "" && t == "" && k == "" {
		return ""
	}
	return fmt.Sprintf("кофе %s, чай %s, какао %s", zeroIfEmpty(c), zeroIfEmpty(t), zeroIfEmpty(k))
}

// mentionsDrink проверяет, есть ли в тексте упоминание напитка в любой словоформе.
func mentionsDrink(s, key string) bool {
	low := strings.ToLower(s)
	for _, f := range drinkForms[key] {
		if strings.Contains(low, f) {
			return true
		}
	}
	return false
}

// wordNum возвращает число, примыкающее к напитку (до или после ключевой словоформы):
// «кофе 3» и «3 кофе», «2 чая» и т.п. распознаются одинаково.
func wordNum(s, key string) string {
	for _, f := range drinkForms[key] {
		if n := numNearWord(s, f); n != "" {
			return n
		}
	}
	return ""
}

// numNearWord ищет число рядом со словоформой word.
func numNearWord(s, word string) string {
	ki := strings.Index(strings.ToLower(s), word)
	if ki < 0 {
		return ""
	}
	// Число сразу после слова («кофе 3»).
	rest := strings.TrimLeft(s[ki+len(word):], " \t:")
	if rest != "" && rest[0] >= '0' && rest[0] <= '9' {
		if m := numRe.FindString(rest); m != "" {
			return m
		}
	}
	// Число непосредственно перед словом («3 кофе», «2 чая»).
	before := s[:ki]
	if idx := numRe.FindAllStringIndex(before, -1); len(idx) > 0 {
		last := idx[len(idx)-1]
		return before[last[0]:last[1]]
	}
	return ""
}

func zeroIfEmpty(v string) string {
	if v == "" {
		return "0"
	}
	return v
}

var comboRe = regexp.MustCompile(`кофе\s+(\d+),\s*чай\s+(\d+),\s*какао\s+(\d+)`)

// comboValues разбирает каноническую строку «кофе C, чай T, какао K».
func comboValues(combo string) (c, t, k int, ok bool) {
	m := comboRe.FindStringSubmatch(combo)
	if len(m) != 4 {
		return 0, 0, 0, false
	}
	c, _ = strconv.Atoi(m[1])
	t, _ = strconv.Atoi(m[2])
	k, _ = strconv.Atoi(m[3])
	return c, t, k, true
}

// validCombo проверяет комбинацию по условиям задачи:
// ровно 5 напитков и сумма 900 ₽ (кофе 200, чай 150, какао 180).
func validCombo(combo string) bool {
	c, t, k, ok := comboValues(combo)
	if !ok {
		return false
	}
	return c >= 0 && t >= 0 && k >= 0 &&
		c+t+k == 5 &&
		200*c+150*t+180*k == 900
}

// countValidCombos возвращает число корректных комбинаций в списке.
func countValidCombos(combos []string) int {
	n := 0
	for _, s := range combos {
		if validCombo(s) {
			n++
		}
	}
	return n
}

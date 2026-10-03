package rag

import (
	"regexp"
	"strings"
)

// UnknownText — детерминированный ответ режима «не знаю» (без вызова LLM):
// возвращается, когда после этапа 2 не осталось чанков либо top-1 score ниже
// порога UnknownBelow. Это жёсткий анти-галлюцинационный гейт.
const UnknownText = "Я не знаю. В найденных документах нет информации по этому вопросу. Уточните, пожалуйста, что именно вы хотите узнать."

// citeSystemPrompt — строгий промпт Дня 24: модель обязана вернуть три секции
// (Ответ / Источники / Цитаты) и сказать «не знаю», если контекст не отвечает
// на вопрос. Цитаты должны быть дословными — это проверяется машинно.
const citeSystemPrompt = `Ты — ассистент, отвечающий на вопрос строго по предоставленному контексту из документов проекта. Правила:
1. Отвечай на русском, кратко и по делу.
2. Опирайся ТОЛЬКО на контекст ниже. Не додумывай, не используй внешние знания.
3. Если в контексте нет ответа на вопрос — ответь ровно: «Я не знаю. Уточните, пожалуйста, вопрос.» — и больше ничего (без секций).
4. Если ответ есть — выведи строго три секции в таком порядке и без лишнего текста:

Ответ:
<краткий ответ по существу, со ссылками [1], [2] на использованные чанки>

Источники:
[1] <путь> (секция: <секция>, chunk: <chunk_id>)
[2] <путь> (секция: <секция>, chunk: <chunk_id>)

Цитаты:
[1] «дословный фрагмент из чанка [1], подтверждающий ответ»
[2] «дословный фрагмент из чанка [2], подтверждающий ответ»

5. В секции «Цитаты» приводи ДОСЛОВНЫЕ фрагменты из соответствующих чанков контекста (копируй текст, не перефразируй). Каждый фрагмент — 1-2 предложения.
6. Номера [n] должны соответствовать нумерации чанков в контексте выше.`

// CitationItem — одна строка из секции «Источники» или «Цитаты».
type CitationItem struct {
	Ref     int    // номер [n]
	Source  string // путь файла (для источников) либо текст цитаты (для цитат)
	Section string // секция (только для источников)
	ChunkID string // chunk_id (только для источников)
}

// Citation — разобранный на секции ответ модели.
type Citation struct {
	Response string         // секция «Ответ:»
	Sources  []CitationItem // секция «Источники:»
	Quotes   []CitationItem // секция «Цитаты:» (Source = текст цитаты)
	FormatOK bool           // все три секции найдены и распознаны
}

// ParseCitation разбирает ответ модели на три секции. Устойчива к регистру
// заголовков и мелким отклонениям; FormatOK=false, если какой-то секции нет.
// Блоки режутся по началу заголовков, а сама строка-заголовок пропускается —
// иначе «Цитаты:» утечёт в блок «Источники» как строка-продолжение.
func ParseCitation(text string) Citation {
	low := strings.ToLower(text)
	a := strings.Index(low, "ответ:")
	b := strings.Index(low, "источники:")
	c := strings.Index(low, "цитаты:")

	cit := Citation{}
	if a < 0 || b < 0 || c < 0 || !(a < b && b < c) {
		// Секций нет или порядок нарушен — ответ не соответствует формату.
		cit.Response = strings.TrimSpace(text)
		return cit
	}

	cit.Response = strings.TrimSpace(text[skipHeader(text, a):b])
	srcBlock := text[skipHeader(text, b):c]
	quoteBlock := text[skipHeader(text, c):]
	cit.Sources = parseSourceLines(srcBlock)
	cit.Quotes = parseQuoteLines(quoteBlock)
	cit.FormatOK = cit.Response != "" && len(cit.Sources) > 0 && len(cit.Quotes) > 0
	return cit
}

// skipHeader возвращает индекс после строки-заголовка секции (маркер + остаток
// строки до перевода). Используется, чтобы не включать заголовок в блок.
func skipHeader(s string, markerStart int) int {
	i := strings.Index(s[markerStart:], "\n")
	if i < 0 {
		return len(s)
	}
	return markerStart + i + 1
}

// refPrefixRe — префикс строки элемента: "[1] ...".
var refPrefixRe = regexp.MustCompile(`^\s*\[(\d+)\]\s*`)

// groupedItem — разобранный элемент секции (источник или цитата): номер [n] и
// текст (для цитат — с дописанными строками-продолжениями, т.к. модель часто
// переносит цитату на несколько строк).
type groupedItem struct {
	ref  int
	text string
}

// parseGrouped разбивает блок на элементы: каждый начинается строкой "[n] ...",
// последующие строки без префикса "[" дописываются к тексту элемента через пробел
// (многострочные цитаты). Пустые строки пропускаются.
func parseGrouped(block string) []groupedItem {
	var items []groupedItem
	for _, raw := range strings.Split(block, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if m := refPrefixRe.FindStringSubmatch(line); m != nil {
			rest := strings.TrimSpace(strings.TrimPrefix(line, m[0]))
			items = append(items, groupedItem{ref: atoiSafe(m[1]), text: rest})
			continue
		}
		// строка-продолжение предыдущего элемента (многострочная цитата).
		if len(items) > 0 {
			items[len(items)-1].text += " " + line
		}
	}
	return items
}

// parseSourceLines разбирает блок «Источники:» в список CitationItem.
func parseSourceLines(block string) []CitationItem {
	items := parseGrouped(block)
	out := make([]CitationItem, 0, len(items))
	for _, it := range items {
		path, section, chunkID := splitSourceMeta(it.text)
		out = append(out, CitationItem{Ref: it.ref, Source: path, Section: section, ChunkID: chunkID})
	}
	return out
}

// splitSourceMeta отделяет путь от скобок "(секция: X, chunk: Y)".
func splitSourceMeta(s string) (path, section, chunkID string) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "("); i >= 0 && strings.HasSuffix(s, ")") {
		path = strings.TrimSpace(s[:i])
		section, chunkID = parseSourceMeta(s[i+1 : len(s)-1])
	} else {
		path = s
	}
	return path, section, chunkID
}

// parseSourceMeta разбирает "секция: X, chunk: Y" из скобок.
func parseSourceMeta(meta string) (section, chunkID string) {
	meta = strings.TrimSpace(meta)
	if meta == "" {
		return "", ""
	}
	for _, part := range strings.Split(meta, ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(strings.ToLower(part), "секция:"):
			section = strings.TrimSpace(part[len("секция:"):])
		case strings.HasPrefix(strings.ToLower(part), "chunk:"):
			chunkID = strings.TrimSpace(part[len("chunk:"):])
		}
	}
	return section, chunkID
}

// parseQuoteLines разбирает блок «Цитаты:» в список CitationItem (Source = текст).
func parseQuoteLines(block string) []CitationItem {
	items := parseGrouped(block)
	out := make([]CitationItem, 0, len(items))
	for _, it := range items {
		quote := stripQuotes(it.text)
		if quote == "" {
			continue
		}
		out = append(out, CitationItem{Ref: it.ref, Source: quote})
	}
	return out
}

// stripQuotes убирает парные кавычки « » " " ' ' вокруг фрагмента.
// Важно: длина префикса/суффикса берётся через len(pair[n]), а не 1 —
// кавычки « » — многобайтовые (2 байта в UTF-8), и срез s[1:len-1] разрезал
// их посередине, оставляя мусорные байты, из-за чего дословная проверка цитат
// всегда проваливалась.
func stripQuotes(s string) string {
	for _, pair := range [][2]string{{"«", "»"}, {"\u201c", "\u201d"}, {"\"", "\""}, {"'", "'"}} {
		if strings.HasPrefix(s, pair[0]) && strings.HasSuffix(s, pair[1]) {
			return strings.TrimSpace(s[len(pair[0]) : len(s)-len(pair[1])])
		}
	}
	return s
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// --- Валидация анти-галлюцинаций ---

// normalize сворачивает все пробелы/переносы в одиночные и приводит к нижнему
// регистру — для дословного сравнения цитаты с текстом чанка.
func normalize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}

// sourceMatches проверяет, что путь из ответа совпадает с путём найденного
// чанка (сравнение «заканчивается на» / «содержит», как в SourceFound).
func sourceMatches(answerPath, chunkPath string) bool {
	a := strings.ReplaceAll(answerPath, "\\", "/")
	a = strings.TrimSpace(a)
	c := strings.ReplaceAll(chunkPath, "\\", "/")
	if a == "" {
		return false
	}
	return strings.HasSuffix(c, a) || strings.Contains(c, a)
}

// ValidateSources проверяет каждый источник из ответа: путь должен совпадать
// с путём хотя бы одного найденного чанка. Возвращает (реальных, всего).
// Несовпадение = потенциальная галлюцинация источника.
func ValidateSources(cit Citation, chunks []Chunk) (real, total int) {
	for _, s := range cit.Sources {
		total++
		for _, c := range chunks {
			if sourceMatches(s.Source, c.Source) {
				real++
				break
			}
		}
	}
	return real, total
}

// ValidateQuotes проверяет каждую цитату: она обязана дословно (с точностью до
// пробелов/регистра) встречаться в тексте хотя бы одного найденного чанка.
// Возвращает (дословных, всего). Несовпадение = потенциальная галлюцинация.
// Проверка по всем чанкам, а не по номеру [n]: нумерация модели ненадёжна, а
// смысл проверки — «существует ли такая цитата в документах вообще».
func ValidateQuotes(cit Citation, chunks []Chunk) (verbatim, total int) {
	for _, q := range cit.Quotes {
		total++
		needle := normalize(q.Source)
		if needle == "" {
			continue
		}
		for _, c := range chunks {
			if strings.Contains(normalize(c.Text), needle) {
				verbatim++
				break
			}
		}
	}
	return verbatim, total
}

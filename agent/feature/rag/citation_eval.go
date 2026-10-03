package rag

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aichallenge/llm"
)

// CitationCase — результат проверки одного вопроса на обязательные источники
// и цитаты (День 24: анти-галлюцинации).
type CitationCase struct {
	QuestionID     string
	Question       string
	Answer         string
	Unknown        bool   // сработал гейт «не знаю» (жёсткий или мягкий)
	FormatOK       bool   // модель выдала все 3 секции
	HasSources     bool   // секция «Источники» есть и непуста
	HasQuotes      bool   // секция «Цитаты» есть и непуста
	SourcesReal    int    // сколько источников совпали с найденными чанками
	SourcesTotal   int    // всего источников в ответе
	QuotesVerbatim int    // сколько цитат дословно встретились в чанках
	QuotesTotal    int    // всего цитат в ответе
	Grounded       string // "yes" | "partial" | "no" | "" (judge выключен)
	FactHits       int
	FactTotal      int
	TopScore       float64 // top-1 score (для калибровки порога UnknownBelow)
	Engine         string
	Retrieved      []Chunk
	Usage          *llm.Result
}

// RunCitationEval прогоняет вопросы через RAG со строгим форматом (Ответ/
// Источники/Цитаты) и проверяет: наличие секций, достоверность источников
// (среди найденных чанков), дословность цитат, смысловое соответствие (judge).
func RunCitationEval(
	ctx context.Context,
	client *llm.Client,
	retriever *Retriever,
	qs []Question,
	maxTokens int,
	judge bool,
) []CitationCase {
	out := make([]CitationCase, 0, len(qs))
	for _, q := range qs {
		c := CitationCase{QuestionID: q.ID, Question: q.Question}
		a, err := AnswerRAG(ctx, client, retriever, q.Question, maxTokens)
		if err != nil {
			c.Answer = fmt.Sprintf("(ошибка: %v)", err)
			out = append(out, c)
			continue
		}
		c.Answer = a.Text
		c.Unknown = a.Unknown
		c.FormatOK = a.FormatOK
		c.Engine = a.Engine
		c.Retrieved = a.Sources
		c.Usage = a.Usage
		if len(a.Sources) > 0 {
			c.TopScore = a.Sources[0].Score
		}
		if a.Citation != nil {
			c.HasSources = len(a.Citation.Sources) > 0
			c.HasQuotes = len(a.Citation.Quotes) > 0
			c.SourcesReal, c.SourcesTotal = ValidateSources(*a.Citation, a.Sources)
			c.QuotesVerbatim, c.QuotesTotal = ValidateQuotes(*a.Citation, a.Sources)
		}
		c.FactHits, c.FactTotal = FactCoverage(a.Text, q.Expectation)
		if judge && !a.Unknown && a.Citation != nil && a.FormatOK {
			c.Grounded = judgeGrounding(client, q.Question, a.Citation.Response, a.Citation.Quotes)
		}
		out = append(out, c)
	}
	return out
}

// judgeGrounding — LLM-as-judge: подтверждается ли смысл ответа приведёнными
// цитатами (каждый факт ответа опирается на цитату). yes | partial | no.
// Парсит как русские (да/частично/нет), так и английские (yes/partial/no)
// ответы — модель может отвечать на любом языке.
func judgeGrounding(client *llm.Client, question, response string, quotes []CitationItem) string {
	var qb strings.Builder
	for _, q := range quotes {
		qb.WriteString(fmt.Sprintf("[%d] %s\n", q.Ref, q.Source))
	}
	prompt := "Вопрос: " + question + "\n\n" +
		"Ответ ассистента:\n" + response + "\n\n" +
		"Цитаты из документов:\n" + qb.String() + "\n" +
		"Совпадает ли смысл ответа приведённым цитатам (каждый факт ответа подтверждается цитатой)? " +
		"Ответь ОДНИМ словом: да (всё подтверждено), частично (часть не подтверждена) или нет (ответ не по цитатам)."
	res, err := chatWithRetry(client,
		[]llm.Message{{Role: "user", Content: prompt}},
		&llm.Options{MaxTokens: 20}, 3)
	if err != nil {
		return ""
	}
	low := strings.ToLower(strings.TrimSpace(res.Text))
	// Берём первое слово — модель может добавить пояснение.
	first := low
	if i := strings.IndexAny(low, " \n\t.,;:!"); i > 0 {
		first = low[:i]
	}
	switch first {
	case "да", "yes":
		return "yes"
	case "частично", "partial":
		return "partial"
	case "нет", "no":
		return "no"
	default:
		// Fallback: поиск по подстроке во всём ответе.
		switch {
		case strings.Contains(low, "частично") || strings.Contains(low, "partial"):
			return "partial"
		case strings.Contains(low, "нет") || strings.Contains(low, "no"):
			return "no"
		case strings.Contains(low, "да") || strings.Contains(low, "yes"):
			return "yes"
		default:
			return ""
		}
	}
}

// RenderCitationReport формирует markdown-отчёт проверки цитат/источников.
// cases — основные вопросы (ожидаются ответы с цитатами); unknownCases —
// внекорпусные вопросы (ожидается режим «не знаю»).
func RenderCitationReport(cases, unknownCases []CitationCase, cfg Config, judge bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Цитаты, источники и анти-галлюцинации (День 24)\n\n")
	fmt.Fprintf(&b, "_Сформировано %s_\n\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "Индекс: `%s` (%s), порог «не знаю» UnknownBelow: %.2f, MinScore: %.2f\n",
		cfg.IndexDir, cfg.Strategy, cfg.UnknownBelow, cfg.MinScore)
	fmt.Fprintf(&b, "Строгий формат (Ответ/Источники/Цитаты): %v, LLM-as-judge (grounded): %v\n\n", cfg.CitationsRequired, judge)

	// --- Сводка по основным вопросам ---
	fmt.Fprintf(&b, "## Сводка по вопросам (%d)\n\n", len(cases))
	fmt.Fprintf(&b, "| id | формат | источники (реально/всего) | цитаты (дословно/всего) | grounded | факты | top score | движок |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|---|---|\n")
	var fmtOK, hasSrc, hasQt, srcReal, srcTot, qVerbatim, qTot, factsHits, factsTotal int
	var unknown int
	for _, c := range cases {
		if c.FormatOK {
			fmtOK++
		}
		if c.HasSources {
			hasSrc++
		}
		if c.HasQuotes {
			hasQt++
		}
		if c.Unknown {
			unknown++
		}
		srcReal += c.SourcesReal
		srcTot += c.SourcesTotal
		qVerbatim += c.QuotesVerbatim
		qTot += c.QuotesTotal
		factsHits += c.FactHits
		factsTotal += c.FactTotal
		fmt.Fprintf(&b, "| %s | %v | %d/%d | %d/%d | %s | %d/%d | %.3f | %s |\n",
			c.QuestionID, mark(c.FormatOK), c.SourcesReal, c.SourcesTotal,
			c.QuotesVerbatim, c.QuotesTotal, orDash(c.Grounded),
			c.FactHits, c.FactTotal, c.TopScore, orDash(c.Engine))
	}
	fmt.Fprintf(&b, "\n**Итого:** формат соблюдён %d/%d, источники есть %d/%d, цитаты есть %d/%d; "+
		"источников достоверно %d/%d, цитат дословно %d/%d; факты %d/%d; «не знаю» %d.\n\n",
		fmtOK, len(cases), hasSrc, len(cases), hasQt, len(cases),
		srcReal, srcTot, qVerbatim, qTot, factsHits, factsTotal, unknown)

	// --- Сводка по внекорпусным вопросам (режим «не знаю») ---
	if len(unknownCases) > 0 {
		fmt.Fprintf(&b, "## Режим «не знаю» (внекорпусные вопросы, %d)\n\n", len(unknownCases))
		fmt.Fprintf(&b, "| id | вопрос | «не знаю»? | top score | движок |\n|---|---|---|---|---|\n")
		var fired int
		for _, c := range unknownCases {
			if c.Unknown {
				fired++
			}
			fmt.Fprintf(&b, "| %s | %s | %v | %.3f | %s |\n",
				c.QuestionID, shortQ(c.Question), mark(c.Unknown), c.TopScore, orDash(c.Engine))
		}
		fmt.Fprintf(&b, "\n**Итого:** гейт «не знаю» сработал %d/%d (порог UnknownBelow=%.2f).\n\n",
			fired, len(unknownCases), cfg.UnknownBelow)
	}

	// --- Вывод ---
	fmt.Fprintf(&b, "## Вывод\n\n")
	fmt.Fprintf(&b, "- Источники в ответах: %d/%d вопросов.\n", hasSrc, len(cases))
	fmt.Fprintf(&b, "- Цитаты в ответах: %d/%d вопросов.\n", hasQt, len(cases))
	fmt.Fprintf(&b, "- Достоверность источников: %d/%d (галлюцинаций источников: %d).\n",
		srcReal, srcTot, srcTot-srcReal)
	fmt.Fprintf(&b, "- Дословность цитат: %d/%d (галлюцинаций цитат: %d).\n",
		qVerbatim, qTot, qTot-qVerbatim)
	if judge {
		yes, partial, no := 0, 0, 0
		for _, c := range cases {
			switch c.Grounded {
			case "yes":
				yes++
			case "partial":
				partial++
			case "no":
				no++
			}
		}
		fmt.Fprintf(&b, "- Смысловое соответствие (judge): yes %d, partial %d, no %d.\n", yes, partial, no)
	}
	if len(unknownCases) > 0 {
		fired := 0
		for _, c := range unknownCases {
			if c.Unknown {
				fired++
			}
		}
		fmt.Fprintf(&b, "- Режим «не знаю» на внекорпусных: %d/%d.\n", fired, len(unknownCases))
	}

	// --- Детали по вопросам ---
	fmt.Fprintf(&b, "\n## Детали\n\n")
	for _, c := range cases {
		fmt.Fprintf(&b, "### %s — %s\n\n", c.QuestionID, c.Question)
		fmt.Fprintf(&b, "формат: %v | источники: %d/%d | цитаты: %d/%d | grounded: %s | top: %.3f | движок: %s\n\n",
			mark(c.FormatOK), c.SourcesReal, c.SourcesTotal, c.QuotesVerbatim, c.QuotesTotal,
			orDash(c.Grounded), c.TopScore, orDash(c.Engine))
		fmt.Fprintf(&b, "```\n%s\n```\n\n---\n\n", c.Answer)
	}
	return b.String()
}

func mark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

func shortQ(q string) string {
	q = strings.TrimSpace(q)
	r := []rune(q)
	if len(r) > 50 {
		return string(r[:50]) + "…"
	}
	return q
}

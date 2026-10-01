package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"aichallenge/llm"
)

// Question — контрольный вопрос из rag_questions.json: сам вопрос, ожидание
// (фразы-факты, которые должны быть в ответе) и ожидаемые источники (файлы).
type Question struct {
	ID          string   `json:"id"`
	Question    string   `json:"question"`
	Expectation []string `json:"expectation"` // фразы-маркеры правильного ответа
	Sources     []string `json:"sources"`     // релятивные пути файлов-источников
}

// LoadQuestions читает JSON с вопросами. Поддерживаются и голый массив,
// и обёртка {"questions": [...]}.
func LoadQuestions(path string) ([]Question, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var qs []Question
	if err := json.Unmarshal(raw, &qs); err != nil {
		var wrapper struct {
			Questions []Question `json:"questions"`
		}
		if werr := json.Unmarshal(raw, &wrapper); werr != nil {
			return nil, err
		}
		qs = wrapper.Questions
	}
	if len(qs) == 0 {
		return nil, fmt.Errorf("нет вопросов в %s", path)
	}
	return qs, nil
}

// FactCoverage считает, сколько фраз-ожиданий встретилось в ответе
// (регистронезависимо). Возвращает (попадания, всего).
func FactCoverage(answer string, expectations []string) (hits, total int) {
	low := strings.ToLower(answer)
	for _, e := range expectations {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		total++
		if strings.Contains(low, strings.ToLower(e)) {
			hits++
		}
	}
	return hits, total
}

// SourceFound проверяет, что среди retrieved-чанков есть хотя бы один
// ожидаемый источник (сравнение «заканчивается на путь»).
func SourceFound(chunks []Chunk, expected []string) bool {
	for _, c := range chunks {
		src := strings.ReplaceAll(c.Source, "\\", "/")
		for _, exp := range expected {
			exp = strings.Trim(strings.ReplaceAll(exp, "\\", "/"), "/")
			if exp == "" {
				continue
			}
			if strings.HasSuffix(src, exp) || strings.Contains(src, exp) {
				return true
			}
		}
	}
	return false
}

// EvalCase — результат ответа в одном режиме (без RAG или с RAG).
type EvalCase struct {
	QuestionID string
	Answer     string
	FactHits   int
	FactTotal  int
	Engine     string // sidecar | keyword; пусто — без RAG
	Retrieved  []Chunk
	Usage      *llm.Result
}

// Comparison — пара ответов на один контрольный вопрос.
type Comparison struct {
	Question       Question
	NoRAG          EvalCase
	RAG            EvalCase
	RAGSourceFound bool   // ожидаемый источник найден ретривером
	JudgeVerdict   string // "rag" | "norag" | "tie" | "" (judge выключен)
}

// RunComparison прогоняет контрольные вопросы в обоих режимах: сначала обычный
// ответ (без RAG), затем RAG-ответ. judge=true — дополнительно LLM-as-judge.
func RunComparison(
	ctx context.Context,
	client *llm.Client,
	retriever *Retriever,
	qs []Question,
	maxTokens int,
	judge bool,
) []Comparison {
	out := make([]Comparison, 0, len(qs))
	for _, q := range qs {
		comp := Comparison{Question: q}

		if a, err := AnswerPlain(client, q.Question, maxTokens); err == nil {
			comp.NoRAG = EvalCase{QuestionID: q.ID, Answer: a.Text, Usage: a.Usage}
		} else {
			comp.NoRAG = EvalCase{QuestionID: q.ID, Answer: fmt.Sprintf("(ошибка: %v)", err)}
		}

		if a, err := AnswerRAG(ctx, client, retriever, q.Question, maxTokens); err == nil {
			comp.RAG = EvalCase{
				QuestionID: q.ID, Answer: a.Text, Engine: a.Engine,
				Retrieved: a.Sources, Usage: a.Usage,
			}
			comp.RAGSourceFound = SourceFound(a.Sources, q.Sources)
		} else {
			comp.RAG = EvalCase{QuestionID: q.ID, Answer: fmt.Sprintf("(ошибка: %v)", err)}
		}

		comp.NoRAG.FactHits, comp.NoRAG.FactTotal = FactCoverage(comp.NoRAG.Answer, q.Expectation)
		comp.RAG.FactHits, comp.RAG.FactTotal = FactCoverage(comp.RAG.Answer, q.Expectation)

		if judge && comp.NoRAG.Usage != nil && comp.RAG.Usage != nil && comp.RAG.Engine != "" {
			comp.JudgeVerdict = judgeAnswers(client, q.Question, comp.NoRAG.Answer, comp.RAG.Answer)
		}
		out = append(out, comp)
	}
	return out
}

// judgeAnswers просит LLM выбрать лучший ответ (LLM-as-judge).
func judgeAnswers(client *llm.Client, question, noRAG, ragA string) string {
	prompt := "Вопрос: " + question + "\n\n" +
		"Ответ БЕЗ RAG:\n" + noRAG + "\n\n" +
		"Ответ С RAG:\n" + ragA + "\n\n" +
		"Какой ответ точнее и полнее опирается на факты? Ответь ОДНИМ словом: rag, norag или tie."
	res, err := client.Chat([]llm.Message{{Role: "user", Content: prompt}}, nil)
	if err != nil {
		return ""
	}
	low := strings.ToLower(strings.TrimSpace(res))
	switch {
	case strings.Contains(low, "norag"):
		return "norag"
	case strings.Contains(low, "rag"):
		return "rag"
	default:
		return "tie"
	}
}

// RenderReport формирует markdown-отчёт сравнения (для results/rag_comparison.md).
func RenderReport(comps []Comparison, cfg Config, judge bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Сравнение качества: без RAG vs с RAG\n\n")
	fmt.Fprintf(&b, "_Сформировано %s_\n\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "Индекс: `%s` (стратегия chunking: %s)\n", cfg.IndexDir, cfg.Strategy)
	fmt.Fprintf(&b, "Ретривер: микросервис index_service (sidecar) с ключевым fallback (keyword)\n")
	fmt.Fprintf(&b, "LLM-as-judge: %v\n\n", judge)

	// Сводная таблица.
	var f0, f1, n1 int // факты no-rag, факты rag, источников найдено
	fmt.Fprintf(&b, "## Сводка\n\n| id | факты без RAG | факты с RAG | источник найден | движок RAG | судья |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|\n")
	for _, c := range comps {
		f0 += c.NoRAG.FactHits
		f1 += c.RAG.FactHits
		if c.RAGSourceFound {
			n1++
		}
		fmt.Fprintf(&b, "| %s | %d/%d | %d/%d | %v | %s | %s |\n",
			c.Question.ID, c.NoRAG.FactHits, c.NoRAG.FactTotal,
			c.RAG.FactHits, c.RAG.FactTotal, c.RAGSourceFound,
			orDash(c.RAG.Engine), orDash(c.JudgeVerdict))
	}
	fmt.Fprintf(&b, "\n**Итого:** покрытие ожиданий — без RAG %d/%d, с RAG %d/%d; "+
		"ожидаемые источники найдены ретривером в %d/%d вопросах.\n\n",
		f0, totalFacts(comps, true), f1, totalFacts(comps, false), n1, len(comps))

	// Детали по вопросам.
	fmt.Fprintf(&b, "## Детали\n\n")
	for _, c := range comps {
		fmt.Fprintf(&b, "### %s — %s\n\n", c.Question.ID, c.Question.Question)
		if len(c.Question.Expectation) > 0 {
			fmt.Fprintf(&b, "Ожидание: %s\n", strings.Join(c.Question.Expectation, ", "))
		}
		if len(c.Question.Sources) > 0 {
			fmt.Fprintf(&b, "Ожидаемые источники: %s\n\n", strings.Join(c.Question.Sources, ", "))
		}
		fmt.Fprintf(&b, "**Без RAG** (%d/%d фактов):\n%s\n\n", c.NoRAG.FactHits, c.NoRAG.FactTotal, c.NoRAG.Answer)
		fmt.Fprintf(&b, "**С RAG** (движок %s, %d/%d фактов, источник найден: %v):\n%s\n\n",
			orDash(c.RAG.Engine), c.RAG.FactHits, c.RAG.FactTotal, c.RAGSourceFound, c.RAG.Answer)
		if c.JudgeVerdict != "" {
			fmt.Fprintf(&b, "Вердикт LLM-as-judge: **%s**\n\n", c.JudgeVerdict)
		}
		fmt.Fprintf(&b, "---\n\n")
	}
	return b.String()
}

func totalFacts(comps []Comparison, noRAG bool) int {
	n := 0
	for _, c := range comps {
		if noRAG {
			n += c.NoRAG.FactTotal
		} else {
			n += c.RAG.FactTotal
		}
	}
	return n
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

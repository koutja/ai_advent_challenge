package main

import (
	"fmt"
	"strings"
)

// fmtVal форматирует число; если данных нет (sентинел -1), возвращает «–».
func fmtVal(v int) string {
	if v < 0 {
		return "–"
	}
	return fmt.Sprintf("%d", v)
}

// fmtDur форматирует длительность в мс.
func fmtDur(ms int) string {
	if ms < 0 {
		return "–"
	}
	return fmt.Sprintf("%d мс (%.2f с)", ms, float64(ms)/1000)
}

// fmtCost форматирует стоимость; если цена не рассчитана — «–».
func fmtCost(r *TierResult) string {
	if !r.CostKnown {
		return "–"
	}
	return fmt.Sprintf("$%.6f", r.CostUSD)
}

// labelOrID возвращает читаемое имя модели для заголовков и выводов.
func labelOrID(r *TierResult) string {
	if r.ID != "" {
		return r.ID
	}
	if r.Title != "" {
		return r.Title
	}
	if r.Label != "" {
		return r.Label
	}
	return r.Tier
}

// excerpt обрезает ответ для компактного отображения.
func excerpt(s string, n int) string {
	if s == "" {
		return "(пустой ответ)"
	}
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

// formatReport собирает человекочитаемый markdown-отчёт: таблица сравнения,
// полные ответы и короткий вывод.
func formatReport(results []*TierResult, query string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Отчёт: сравнение версий моделей\n\n")
	fmt.Fprintf(&b, "**Запрос:** %q\n\n", query)
	fmt.Fprintf(&b, "| Тир | Модель (id) | Название | Провайдер | Elo | Время | Токены (промпт/ответ/итого) | Стоимость |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|---|---|\n")
	for _, r := range results {
		id := r.ID
		if id == "" {
			id = "–"
		}
		title := r.Title
		if title == "" {
			title = "–"
		}
		prov := r.Provider
		if prov == "" {
			prov = "–"
		}
		elo := "–"
		if r.Elo > 0 {
			elo = fmt.Sprintf("%d", r.Elo)
		}
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s | %s | %s / %s / %s | %s |\n",
			r.Tier, id, title, prov, elo,
			fmtDur(r.DurationMs), fmtVal(r.PromptTokens), fmtVal(r.CompletionTokens), fmtVal(r.TotalTokens),
			fmtCost(r))
		if r.Err != "" {
			fmt.Fprintf(&b, "\n> ⚠ **%s**: %s\n", r.Tier, r.Err)
		}
	}

	b.WriteString("\n## Ответы\n\n")
	for _, r := range results {
		fmt.Fprintf(&b, "### %s — %s\n\n", r.Tier, labelOrID(r))
		if r.Err != "" {
			fmt.Fprintf(&b, "_Не удалось получить ответ: %s_\n\n", r.Err)
			continue
		}
		fmt.Fprintf(&b, "%s\n\n", r.Text)
	}

	b.WriteString("## Вывод\n\n")
	b.WriteString(buildConclusion(results))
	b.WriteString("\n")

	return b.String()
}

// buildConclusion формирует короткий вывод о различиях между моделями:
// кто быстрее, кто дороже, у кого выше рейтинг/расход токенов. Если данных
// недостаточно — честно пишет об этом, но не падает.
func buildConclusion(results []*TierResult) string {
	var lines []string

	fastIdx, slowIdx := -1, -1
	highEloIdx := -1
	maxTokIdx := -1
	for i, r := range results {
		if r.Err != "" || r.Skipped {
			continue
		}
		if r.DurationMs >= 0 {
			if fastIdx == -1 || r.DurationMs < results[fastIdx].DurationMs {
				fastIdx = i
			}
			if slowIdx == -1 || r.DurationMs > results[slowIdx].DurationMs {
				slowIdx = i
			}
		}
		if r.Elo > 0 && (highEloIdx == -1 || r.Elo > results[highEloIdx].Elo) {
			highEloIdx = i
		}
		if r.TotalTokens >= 0 && (maxTokIdx == -1 || r.TotalTokens > results[maxTokIdx].TotalTokens) {
			maxTokIdx = i
		}
	}

	if fastIdx >= 0 {
		lines = append(lines, fmt.Sprintf("- Быстрее всех ответила **%s** (%s).", labelOrID(results[fastIdx]), fmtDur(results[fastIdx].DurationMs)))
	}
	if slowIdx >= 0 && slowIdx != fastIdx {
		lines = append(lines, fmt.Sprintf("- Дольше всех отвечала **%s** (%s).", labelOrID(results[slowIdx]), fmtDur(results[slowIdx].DurationMs)))
	}
	if highEloIdx >= 0 {
		lines = append(lines, fmt.Sprintf("- Самая «сильная» по рейтингу Elo — **%s** (%d): обычно качественнее отвечает, но как правило медленнее и дороже.", labelOrID(results[highEloIdx]), results[highEloIdx].Elo))
	}
	if maxTokIdx >= 0 {
		lines = append(lines, fmt.Sprintf("- Наибольший расход токенов — у **%s** (%d).", labelOrID(results[maxTokIdx]), results[maxTokIdx].TotalTokens))
	}

	cheapIdx, expIdx := -1, -1
	for i, r := range results {
		if !r.CostKnown {
			continue
		}
		if cheapIdx == -1 || r.CostUSD < results[cheapIdx].CostUSD {
			cheapIdx = i
		}
		if expIdx == -1 || r.CostUSD > results[expIdx].CostUSD {
			expIdx = i
		}
	}
	if cheapIdx >= 0 {
		lines = append(lines, fmt.Sprintf("- Дешевле всего обошёлся **%s** (%s).", labelOrID(results[cheapIdx]), fmtCost(results[cheapIdx])))
	}
	if expIdx >= 0 && expIdx != cheapIdx {
		lines = append(lines, fmt.Sprintf("- Дороже всего — **%s** (%s).", labelOrID(results[expIdx]), fmtCost(results[expIdx])))
	}

	if len(lines) == 0 {
		lines = append(lines, "- Данных для сравнения нет: все прогоны завершились ошибкой или тиры не настроены. Проверьте ключ, `id` моделей и доступность API.")
	} else {
		lines = append(lines, "\n> Оценка качества субъективна — сравнивайте тексты ответов в разделе «Ответы». Полнота замера зависит от того, что вернул API: если `usage` или цена недоступны, в таблице стоит «–».")
	}

	return strings.Join(lines, "\n")
}

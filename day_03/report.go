package main

import (
	"fmt"
	"strings"
)

// runReport читает results/*.json и строит таблицу, сравнение и статистику ответов.
func runReport() error {
	results, err := loadResults()
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return fmt.Errorf("нет результатов в %q — сначала запустите стратегии (make run-all)", resultsDir)
	}

	fmt.Println("=== ОТЧЁТ ПО РЕЗУЛЬТАТАМ СТРАТЕГИЙ ===")

	// Таблица общих показателей.
	fmt.Printf("\n%-12s | %-5s | %-10s | %-18s | %s\n", "Метод", "OK", "Время", "Комбинаций", "Статус")
	fmt.Println(strings.Repeat("-", 78))
	for _, r := range results {
		status := "OK"
		if !r.OK {
			status = "ошибка: " + truncate(r.Error, 36)
		}
		fmt.Printf("%-12s | %-5t | %-10s | %-18d | %s\n",
			r.Method, r.OK, fmt.Sprintf("%d мс", r.DurationMS), r.CombosFound, status)
	}

	// Найденные комбинации по каждому методу (✓ — удовлетворяет условиям, ✗ — нет).
	fmt.Println("\n--- Найденные комбинации (по методам; ✓ верно / ✗ неверно) ---")
	for _, r := range results {
		if !r.OK {
			fmt.Printf("%-12s: <не выполнен>\n", r.Method)
			continue
		}
		if len(r.Combos) == 0 {
			fmt.Printf("%-12s: <комбинации не распознаны>\n", r.Method)
			continue
		}
		var parts []string
		for _, c := range r.Combos {
			mark := "✗"
			if validCombo(c) {
				mark = "✓"
			}
			parts = append(parts, mark+" "+c)
		}
		fmt.Printf("%-12s: %s\n", r.Method, strings.Join(parts, " | "))
	}

	// Статистика: сколько способов дали одинаковый набор комбинаций.
	fmt.Println("\n--- Статистика ответов на одну задачу ---")
	groups := map[string][]string{}
	for _, r := range results {
		if !r.OK {
			continue
		}
		key := strings.Join(r.Combos, ";")
		groups[key] = append(groups[key], r.Method)
	}
	i := 1
	for key, methods := range groups {
		label := key
		if label == "" {
			label = "<пустой ответ>"
		}
		fmt.Printf("%d) %d способ(а): %s\n   ответ: %s\n", i, len(methods), strings.Join(methods, ", "), label)
		i++
	}

	// Вердикт: ориентируемся на число КОРРЕКТНЫХ комбинаций.
	bestMethod := ""
	bestValid := -1
	totalOK := 0
	for _, r := range results {
		if !r.OK {
			continue
		}
		totalOK++
		valid := countValidCombos(r.Combos)
		if valid > bestValid {
			bestValid = valid
			bestMethod = r.Method
		}
	}
	fmt.Printf("\nВердикт: корректный ответ (все 2 комбинации) дал метод %q (валидных: %d). Успешных прогонов: %d/%d.\n",
		bestMethod, bestValid, totalOK, len(results))
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

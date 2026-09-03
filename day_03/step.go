package main

import (
	"fmt"
	"time"

	"aichallenge/llm"
)

// runStep — «решай пошагово»: модель объясняет каждый шаг.
func runStep(client *llm.Client, rep *Reporter) error {
	r := &Result{Method: "step", StartedAt: time.Now()}
	rep.printf("step", "=== Стратегия: ПОШАГОВОЕ РЕШЕНИЕ ===\n")
	rep.begin("step", "составляю уравнения...")

	prompt := defaultTask + "\n\nРешай пошагово: сначала составь уравнения, затем найди все решения и проверь каждое. Объясни каждый шаг."
	out, err := client.Chat([]llm.Message{{Role: "user", Content: prompt}}, nil)
	if err != nil {
		finishResult(r, "", err)
		rep.done("step", "ошибка: "+truncate(err.Error(), 40), false)
		return err
	}
	rep.step("step", "ищу решения...")
	rep.output("step", out+"\n")

	if err := finishResult(r, out, nil); err != nil {
		return err
	}
	rep.done("step", fmt.Sprintf("готово: %d комбинаций", r.CombosFound), true)
	return nil
}

package main

import (
	"fmt"
	"time"

	"aichallenge/llm"
)

// runDirect — прямой ответ без дополнительных инструкций.
func runDirect(client *llm.Client, rep *Reporter) error {
	r := &Result{Method: "direct", StartedAt: time.Now()}
	rep.printf("direct", "=== Стратегия: ПРЯМОЙ ОТВЕТ ===\n")
	rep.begin("direct", "прямой ответ без инструкций...")

	out, err := client.Chat([]llm.Message{{Role: "user", Content: defaultTask}}, nil)
	if err != nil {
		finishResult(r, "", err)
		rep.done("direct", "ошибка: "+truncate(err.Error(), 40), false)
		return err
	}
	rep.output("direct", out+"\n")

	if err := finishResult(r, out, nil); err != nil {
		return err
	}
	rep.done("direct", fmt.Sprintf("готово: %d комбинаций", r.CombosFound), true)
	return nil
}

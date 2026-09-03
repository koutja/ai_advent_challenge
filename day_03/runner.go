package main

import (
	"fmt"

	"aichallenge/llm"
)

// runAll запускает все стратегии последовательно с общим Reporter:
// в терминале обновляется интерактивная панель статусов, а детальный вывод
// каждого метода пишется в logs/<метод>.log.
//
// Ошибки отдельных стратегий не прерывают прогон — они фиксируются в их
// JSON-результате (ok=false) и на панели (✗).
func runAll(client *llm.Client) error {
	rep := newReporter([]string{"direct", "step", "prompt-gen", "panel"})
	resetLogs(rep.logDir)

	_ = runDirect(client, rep)
	_ = runStep(client, rep)
	_ = runPromptGen(client, rep)
	_ = runPanel(client, rep)

	rep.finish()
	fmt.Println("Все стратегии завершены. Запустите: make report")
	return nil
}

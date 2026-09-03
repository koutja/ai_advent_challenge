// Day 03 — Разные способы рассуждения (последовательно + интерактивный прогресс).
//
// Одна математическая задача решается через API несколькими способами.
// Каждый способ — отдельный .go файл; результат пишется в JSON в каталог results/,
// детальный вывод — в logs/<метод>.log. Команда report строит статистику.
//
// CLI:
//   - day_03 direct       → results/direct.json
//   - day_03 step         → results/step.json
//   - day_03 prompt-gen   → results/prompt-gen.json
//   - day_03 panel        → results/panel.json (последовательная передача между ролями)
//   - day_03 run-all      → последовательный запуск всех с интерактивным прогрессом
//   - day_03 report       → сравнение и статистика по results/*.json
//
// Клиент, чтение .env и вызов /chat/completions — в общем пакете llm (../llm).
package main

import (
	"fmt"
	"os"

	"aichallenge/llm"
)

const usage = "Использование: day_03 <direct|step|prompt-gen|panel|run-all|report>"

func main() {
	if len(os.Args) < 2 {
		die(usage)
	}
	mode := os.Args[1]

	// report не требует API-клиента — читает только JSON-файлы.
	if mode == "report" {
		if err := runReport(); err != nil {
			die("Ошибка (report): %v", err)
		}
		return
	}

	client, err := llm.New()
	if err != nil {
		die("%v", err)
	}

	// run-all ведёт свой репортёр (детали в logs/).
	if mode == "run-all" {
		if err := runAll(client); err != nil {
			die("Ошибка (run-all): %v", err)
		}
		return
	}

	// Одиночные подкоманды: спиннер + счётчик времени, ответ в stdout.
	rep := newReporterStdout(mode)
	defer rep.finish()

	var runErr error
	switch mode {
	case "direct":
		runErr = runDirect(client, rep)
	case "step":
		runErr = runStep(client, rep)
	case "prompt-gen":
		runErr = runPromptGen(client, rep)
	case "panel":
		runErr = runPanel(client, rep)
	default:
		die("Неизвестный режим %q\n%s", mode, usage)
	}
	if runErr != nil {
		die("Ошибка (%s): %v", mode, runErr)
	}
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

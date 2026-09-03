package main

import (
	"fmt"
	"sync"

	"aichallenge/llm"
)

// runAll запускает все стратегии ПАРАЛЛЕЛЬНО (каждая в своей горутине) с общим
// Reporter: в терминале обновляются НА МЕСТЕ строки по каждому способу
// (спиннер, текущий шаг, счётчик времени), а детальный вывод каждого метода
// пишется в logs/<метод>.log. Ошибка одного метода не прерывает остальные.
func runAll(client *llm.Client) error {
	rep := newReporter([]string{"direct", "step", "prompt-gen", "panel"})
	resetLogs(rep.logDir)
	rep.startTicker()

	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); _ = runDirect(client, rep) }()
	go func() { defer wg.Done(); _ = runStep(client, rep) }()
	go func() { defer wg.Done(); _ = runPromptGen(client, rep) }()
	go func() { defer wg.Done(); _ = runPanel(client, rep) }()
	wg.Wait()

	rep.finish()
	fmt.Println("Все стратегии завершены. Результаты в results/, логи в logs/. Запустите: make report")
	return nil
}

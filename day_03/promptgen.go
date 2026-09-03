package main

import (
	"fmt"
	"time"

	"aichallenge/llm"
)

// runPromptGen — модель сначала составляет промпт, затем решает по нему (2 вызова).
func runPromptGen(client *llm.Client, rep *Reporter) error {
	r := &Result{Method: "prompt-gen", StartedAt: time.Now()}
	rep.printf("prompt-gen", "=== Стратегия: МОДЕЛЬ СОСТАВЛЯЕТ ПРОМПТ ===\n")
	rep.begin("prompt-gen", "составляю промпт...")

	genPrompt := "Составь подробный, самостоятельный промпт, который решит следующую задачу. " +
		"Промпт должен быть обращён ко второй модели и содержать всю информацию и требования к формату ответа: " +
		"перечислить все комбинации в виде 'кофе X, чай Y, какао Z' и указать их количество. " +
		"Верни ТОЛЬКО текст промпта.\n\nЗадача:\n" + defaultTask

	gen, err := client.Chat([]llm.Message{{Role: "user", Content: genPrompt}}, nil)
	if err != nil {
		finishResult(r, "", err)
		rep.done("prompt-gen", "ошибка: "+truncate(err.Error(), 40), false)
		return err
	}
	rep.printf("prompt-gen", "\n--- Сгенерированный промпт ---\n%s\n\n", gen)

	rep.step("prompt-gen", "решаю по сгенерированному промпту...")
	out, err := client.Chat([]llm.Message{{Role: "user", Content: gen}}, nil)
	if err != nil {
		finishResult(r, "", err)
		rep.done("prompt-gen", "ошибка: "+truncate(err.Error(), 40), false)
		return err
	}
	rep.printf("prompt-gen", "--- Решение ---\n%s\n", out)

	if err := finishResult(r, out, nil); err != nil {
		return err
	}
	rep.done("prompt-gen", fmt.Sprintf("готово: %d комбинаций", r.CombosFound), true)
	return nil
}

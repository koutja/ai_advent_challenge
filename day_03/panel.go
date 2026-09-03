package main

import (
	"fmt"
	"strings"
	"time"

	"aichallenge/llm"
)

// panelExpert — один эксперт панели.
type panelExpert struct {
	Name string
	Role string
}

// panelExperts — последовательная передача задачи между ролями:
// Математик → Программист → Ревизор → Итоговый список.
// Каждая роль делает одну итерацию в отдельном запросе; ответ предыдущей
// роли дополняет запрос к следующей (см. runPanel).
var panelExperts = []panelExpert{
	{Name: "Математик", Role: "Ты — математик. Запиши систему уравнений и неравенств задачи. Определи, по каким переменным вести перебор и какие у них границы. Докажи, что за пределами этих границ решений нет."},
	{Name: "Программист", Role: "Ты — программист. Опиши алгоритм перебора как код: циклы, условия, проверки. Прогони его мысленно и выпиши все комбинации, которые проходят проверки, в формате (кофе, чай, какао)."},
	{Name: "Ревизор", Role: "Ты — ревизор. Возьми каждую комбинацию от программиста и подставь обратно в условия: ровно 5 напитков, ровно 900 ₽, все количества целые и неотрицательные. Отметь лишние и добавь пропущенные, если программист что-то упустил."},
}

// runPanel — группа экспертов с последовательной передачей результата между ролями.
func runPanel(client *llm.Client, rep *Reporter) error {
	r := &Result{Method: "panel", StartedAt: time.Now()}
	rep.printf("panel", "=== Стратегия: ГРУППА ЭКСПЕРТОВ (последовательная передача) ===\n")

	var dialogue []string
	rep.begin("panel", "математик записывает уравнения...")

	// 1) Математик
	maths, err := expertStep(client, rep, panelExperts[0], "")
	if err != nil {
		finishResult(r, "", err)
		rep.done("panel", "ошибка: "+truncate(err.Error(), 40), false)
		return err
	}
	dialogue = append(dialogue, maths)

	// 2) Программист видит вывод математика
	rep.step("panel", "программист описывает алгоритм...")
	prog, err := expertStep(client, rep, panelExperts[1], strings.Join(dialogue, "\n"))
	if err != nil {
		finishResult(r, "", err)
		rep.done("panel", "ошибка: "+truncate(err.Error(), 40), false)
		return err
	}
	dialogue = append(dialogue, prog)

	// 3) Ревизор видит выводы математика и программиста
	rep.step("panel", "ревизор подставляет комбинации...")
	rev, err := expertStep(client, rep, panelExperts[2], strings.Join(dialogue, "\n"))
	if err != nil {
		finishResult(r, "", err)
		rep.done("panel", "ошибка: "+truncate(err.Error(), 40), false)
		return err
	}
	dialogue = append(dialogue, rev)

	// 4) Координатор сводит итоговый список всех комбинаций
	rep.step("panel", "собираю итоговый список...")
	final, err := finalList(client, rep, strings.Join(dialogue, "\n"))
	if err != nil {
		finishResult(r, "", err)
		rep.done("panel", "ошибка: "+truncate(err.Error(), 40), false)
		return err
	}

	full := "ДИАЛОГ:\n" + strings.Join(dialogue, "\n\n") + "\n\nИТОГОВЫЙ СПИСОК:\n" + final
	if err := finishResult(r, full, nil); err != nil {
		return err
	}
	rep.printf("panel", "\n--- ИТОГОВЫЙ СПИСОК ---\n%s\n", final)
	rep.done("panel", fmt.Sprintf("готово: %d комбинаций", r.CombosFound), true)
	return nil
}

// expertStep вызывает одного эксперта, передавая ему выводы предыдущих коллег.
func expertStep(client *llm.Client, rep *Reporter, e panelExpert, prior string) (string, error) {
	content := "Задача:\n" + defaultTask
	if prior != "" {
		content += "\n\nВыводы предыдущих коллег (учти их, не повторяйся):\n" + prior
	}
	content += "\n\nВыполни ровно одну итерацию своей работы и дай краткий вывод (несколько предложений)."

	out, err := client.Chat([]llm.Message{
		{Role: "system", Content: e.Role},
		{Role: "user", Content: content},
	}, &llm.Options{Temperature: float64Ptr(0.4)})
	if err != nil {
		return "", err
	}
	rep.printf("panel", "\n--- %s ---\n%s\n", e.Name, out)
	return "[" + e.Name + "]\n" + out, nil
}

// finalList — координатор собирает итоговый список всех комбинаций.
func finalList(client *llm.Client, rep *Reporter, prior string) (string, error) {
	content := "Задача:\n" + defaultTask
	content += "\n\nВыводы экспертов:\n" + prior
	content += "\n\nСведи итоги и перечисли финальный список ВСЕХ возможных комбинаций. " +
		"Каждую комбинацию выведи отдельной строкой строго в формате: кофе X, чай Y, какао Z. Больше ничего не добавляй."

	out, err := client.Chat([]llm.Message{
		{Role: "system", Content: "Ты — координатор экспертной группы. Твоя задача — финальная сверка и итоговый список всех комбинаций."},
		{Role: "user", Content: content},
	}, &llm.Options{Temperature: float64Ptr(0.3)})
	if err != nil {
		return "", err
	}
	return out, nil
}

func float64Ptr(v float64) *float64 { return &v }

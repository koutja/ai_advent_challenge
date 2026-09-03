// day_04 — Температура: один и тот же запрос при разных значениях temperature.
//
// Сравниваем ответы модели на один запрос при temperature = 0, 0.7, 1.2
// по осям «точность / креативность / разнообразие» и делаем выводы о том,
// для каких задач лучше подходит каждая настройка.
//
// Использует общий клиент пакета llm (см. ../llm). Параметр temperature
// передаётся через llm.Options.Temperature (nil — не отправлять).
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"aichallenge/llm"
)

// defaultQuery — вопрос по умолчанию, если пользователь не передал свой.
const defaultQuery = "Главный вопрос жизни, Вселенной и всего такого"

// temps — набор значений температуры для сравнения.
var temps = []float64{0, 0.7, 1.2}

// tempProfile описывает, для каких задач подходит конкретная настройка.
type tempProfile struct {
	t     float64
	use   string
	notes string
}

// profiles — статичные выводы по каждой температуре (печатаются в конце).
var profiles = []tempProfile{
	{
		t:     0,
		use:   "Точные, фактологические задачи: извлечение данных, классификация, кодирование, перевод без творчества, ответы по чёткому шаблону, FAQ-поддержка.",
		notes: "Максимальная детерминированность и точность. Один и тот же запрос даёт практически одинаковые ответы. Минус — отсутствие креативности и однообразие формулировок.",
	},
	{
		t:     0.7,
		use:   "Универсальный «золотой» режим: диалоги, объяснения, генерация обычного текста, черновики. Хороший баланс точности и живости языка.",
		notes: "Среднее разнообразие при сохранении связности. Ответы различаются формулировками, но остаются осмысленными и по существу.",
	},
	{
		t:     1.2,
		use:   "Креативные и открытые задачи: сторителлинг, идеи, брейншторм, шутки, художественные тексты, нестандартные аналогии.",
		notes: "Высокое разнообразие и неожиданные ходы. Риск: меньше точности, возможны «фантазии», отклонения от фактов и многословие.",
	},
}

func main() {
	client, err := llm.New()
	if err != nil {
		die("%v", err)
	}

	mode, query, singleTemp, err := parseArgs(os.Args[1:])
	if err != nil {
		die("%v\n\n%s", err, usage())
	}
	if mode == "help" {
		fmt.Print(usage())
		return
	}

	if mode == "single" {
		runSingle(client, query, singleTemp)
		return
	}
	runAll(client, query)
}

// parseArgs разбирает аргументы командной строки: первый аргумент — режим,
// остальные — текст запроса.
func parseArgs(args []string) (mode, query string, singleTemp float64, err error) {
	if len(args) == 0 {
		return "all", defaultQuery, 0, nil
	}
	switch args[0] {
	case "--help", "-h":
		return "help", "", 0, nil
	case "--run-all":
		return "all", joinQuery(args[1:]), 0, nil
	case "--single":
		if len(args) < 2 {
			return "", "", 0, fmt.Errorf("режим --single требует значение температуры")
		}
		t, perr := strconv.ParseFloat(args[1], 64)
		if perr != nil || t < 0 || t > 2 {
			return "", "", 0, fmt.Errorf("некорректная температура %q (ожидается число от 0 до 2)", args[1])
		}
		return "single", joinQuery(args[2:]), t, nil
	default:
		return "all", joinQuery(args), 0, nil
	}
}

func joinQuery(parts []string) string {
	if len(parts) == 0 {
		return defaultQuery
	}
	return strings.Join(parts, " ")
}

// runAll прогоняет запрос при всех значениях температуры и печатает выводы.
func runAll(client *llm.Client, query string) {
	fmt.Printf("Запрос: %q\n", query)
	fmt.Printf("Модель: %s\n", client.Model())
	fmt.Println(strings.Repeat("─", 60))
	for _, t := range temps {
		out, err := chat(client, query, t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠ Ошибка (temperature=%g): %v\n", t, err)
			continue
		}
		printAnswer(t, out)
		fmt.Println()
	}
	printRecommendations()
}

// runSingle прогоняет запрос при одном значении температуры.
func runSingle(client *llm.Client, query string, t float64) {
	fmt.Printf("Запрос: %q\n", query)
	fmt.Printf("Модель: %s | temperature = %g\n", client.Model(), t)
	fmt.Println(strings.Repeat("─", 60))
	out, err := chat(client, query, t)
	if err != nil {
		die("Ошибка (temperature=%g): %v", t, err)
	}
	printAnswer(t, out)
}

// chat отправляет один запрос пользователя с заданной температурой.
func chat(client *llm.Client, query string, t float64) (string, error) {
	return client.Chat(
		[]llm.Message{{Role: "user", Content: query}},
		&llm.Options{Temperature: &t},
	)
}

func printAnswer(t float64, answer string) {
	fmt.Printf("=== temperature = %g ===\n", t)
	fmt.Println(strings.TrimSpace(answer))
}

// printRecommendations печатает статичные выводы по каждой настройке.
func printRecommendations() {
	fmt.Println("=== ВЫВОДЫ: для каких задач какая настройка ===")
	for _, p := range profiles {
		fmt.Printf("\n▶ temperature = %g\n", p.t)
		fmt.Printf("  Подходит для: %s\n", p.use)
		fmt.Printf("  Особенности:  %s\n", p.notes)
	}
}

func usage() string {
	return `Температура — сравнение ответов на один запрос при разных temperature.

Использование:
  go run . [запрос...]                 — прогон при temperature 0, 0.7, 1.2 (по умолчанию)
  go run . --single <temp> [запрос...] — только одно значение temperature
  go run . --help                       — эта справка

Примеры:
  go run .                                   # вопрос по умолчанию «Главный вопрос жизни...»
  go run . "Напиши короткое стихотворение"   # все три температуры
  go run . --single 1.2 "Придумай имя для кота"
`
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

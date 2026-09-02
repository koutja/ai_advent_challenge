// Day 02 — Контроль формата ответа.
//
// Один и тот же запрос отправляется в LLM с разным уровнем управления:
//   - "--free":     без ограничений (ничего не добавляем в запрос);
//   - "--limited":  явный формат ответа + ограничение длины + условие завершения;
//   - "--run-all":  оба режима последовательно + сводное сравнение.
//
// Клиент, чтение .env и вызов /chat/completions — в общем пакете llm (../llm).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"aichallenge/llm"
)

const (
	// Предзаданный текст для демонстрации. Можно переопределить аргументом.
	defaultPrompt = "Найди песню по подсказке: группа из Ливерпуля, известная хитом 'Let It Be'. Верни название, исполнителя, жанр и год выхода."

	// Параметры для режима "--limited".
	limitedMaxTokens = 120
	stopSequence     = "</output>"
)

// songInfo описывает структурированный ответ для сравнения.
// Поля объявлены как json.RawMessage: модель может вернуть year и строкой ("1970"),
// и числом (1970) — это не должно ломать разбор всего JSON.
type songInfo struct {
	Song    json.RawMessage `json:"song"`
	Artist  json.RawMessage `json:"artist"`
	Genre   json.RawMessage `json:"genre"`
	Year    json.RawMessage `json:"year"`
	Parsed  bool            `json:"-"`
	RawText string          `json:"-"`
}

func main() {
	client, err := llm.New()
	if err != nil {
		die("%v", err)
	}

	mode, prompt, err := parseArgs(os.Args[1:])
	if err != nil {
		die("Ошибка: %v", err)
	}

	switch mode {
	case "free":
		runFree(client, prompt)
	case "limited":
		runLimited(client, prompt)
	case "run-all":
		runAll(client, prompt)
	default:
		die("Неизвестный режим: %s", mode)
	}
}

// runFree выполняет запрос без каких-либо ограничений.
func runFree(client *llm.Client, prompt string) {
	fmt.Println("=== Режим: БЕЗ ОГРАНИЧЕНИЙ ===")
	fmt.Println("Запрос:", prompt)
	fmt.Println("---")
	out, err := client.Chat([]llm.Message{{Role: "user", Content: prompt}}, nil)
	if err != nil {
		die("Ошибка (запрос free): %v", err)
	}
	fmt.Println(out)
}

// limitedPrompt собирает текст запроса с тремя уровнями контроля.
func limitedPrompt(prompt string) string {
	return prompt +
		"\n\nОтветь СТРОГО в формате JSON с полями: song, artist, genre, year." +
		" Не используй markdown. Не более 40 слов." +
		" Заверши ответ маркером " + stopSequence + "."
}

// runLimited выполняет тот же запрос с добавлением трёх уровней контроля.
func runLimited(client *llm.Client, prompt string) {
	fmt.Println("=== Режим: С ОГРАНИЧЕНИЯМИ ===")
	fmt.Println("Запрос:", prompt)
	fmt.Println("Промпт дополнен: явный формат (JSON), лимит слов, стоп-маркер.")
	fmt.Println("Параметры API: max_tokens=", limitedMaxTokens, " stop=", stopSequence,
		" response_format=json_object")
	fmt.Println("---")

	out, err := client.Chat(
		[]llm.Message{{Role: "user", Content: limitedPrompt(prompt)}},
		&llm.Options{MaxTokens: limitedMaxTokens, Stop: []string{stopSequence}, JSONMode: true},
	)
	if err != nil {
		die("Ошибка (запрос limited): %v", err)
	}
	fmt.Println(out)
	fmt.Printf("\nОбрезано ли по стоп-последовательности %q? %v\n",
		stopSequence, !strings.Contains(out, stopSequence))
}

// runAll выполняет оба режима и выводит сравнение.
func runAll(client *llm.Client, prompt string) {
	freeInfo := songInfo{RawText: callQuiet(client, "free", prompt)}
	limInfo := songInfo{RawText: callQuiet(client, "limited", prompt)}

	freeInfo.Parsed = json.Unmarshal(extractJSON(freeInfo.RawText), &freeInfo) == nil
	limInfo.Parsed = json.Unmarshal(extractJSON(limInfo.RawText), &limInfo) == nil

	fmt.Println("=== СВОДНОЕ СРАВНЕНИЕ ===")
	fmt.Printf("\n[1] БЕЗ ОГРАНИЧЕНИЙ (длина: %d симв.)\n", len(freeInfo.RawText))
	fmt.Println(freeInfo.RawText)

	fmt.Printf("\n[2] С ОГРАНИЧЕНИЯМИ (длина: %d симв.)\n", len(limInfo.RawText))
	fmt.Println(limInfo.RawText)

	fmt.Println("\n--- Таблица сравнения ---")
	fmt.Printf("%-28s | %-24s | %-24s\n", "Критерий", "Без ограничений", "С ограничениями")
	fmt.Println(strings.Repeat("-", 82))
	fmt.Printf("%-28s | %-24s | %-24s\n", "Длина (символов)", itoa(len(freeInfo.RawText)), itoa(len(limInfo.RawText)))
	fmt.Printf("%-28s | %-24s | %-24s\n", "JSON распарсен", boolStr(freeInfo.Parsed), boolStr(limInfo.Parsed))
	fmt.Printf("%-28s | %-24s | %-24s\n", "Поле 'song'", displayField(freeInfo.Song), displayField(limInfo.Song))
	fmt.Printf("%-28s | %-24s | %-24s\n", "Поле 'artist'", displayField(freeInfo.Artist), displayField(limInfo.Artist))
	fmt.Printf("%-28s | %-24s | %-24s\n", "Поле 'genre'", displayField(freeInfo.Genre), displayField(limInfo.Genre))
	fmt.Printf("%-28s | %-24s | %-24s\n", "Поле 'year'", displayField(freeInfo.Year), displayField(limInfo.Year))
	fmt.Printf("%-28s | %-24s | %-24s\n", "Стоп-маркер применился", "-", boolStr(!strings.Contains(limInfo.RawText, stopSequence)))
	fmt.Println("\nВывод: 'С ограничениями' возвращает стабильный структурированный JSON;")
	fmt.Println("'Без ограничений' — свободный текст, который сложнее обрабатывать автоматически.")
}

// callQuiet выполняет один режим и возвращает только текст ответа.
func callQuiet(client *llm.Client, mode, prompt string) string {
	switch mode {
	case "free":
		out, err := client.Chat([]llm.Message{{Role: "user", Content: prompt}}, nil)
		if err != nil {
			die("Ошибка (запрос free): %v", err)
		}
		return out
	case "limited":
		out, err := client.Chat(
			[]llm.Message{{Role: "user", Content: limitedPrompt(prompt)}},
			&llm.Options{MaxTokens: limitedMaxTokens, Stop: []string{stopSequence}, JSONMode: true},
		)
		if err != nil {
			die("Ошибка (запрос limited): %v", err)
		}
		return out
	}
	return ""
}

// extractJSON пытается извлечь JSON-объект из текста ответа.
func extractJSON(s string) []byte {
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			return []byte(s[i : j+1])
		}
	}
	return []byte(s)
}

// displayField выводит значение поля: срезает кавычки (строка) или оставляет
// число как есть; пустое поле показывает как '-'.
func displayField(v json.RawMessage) string {
	if len(v) == 0 {
		return "-"
	}
	s := string(v)
	if i := strings.IndexByte(s, '"'); i == 0 {
		s = s[1:]
	}
	if j := strings.LastIndexByte(s, '"'); j >= 0 && j == len(s)-1 {
		s = s[:j]
	}
	if s == "" {
		return "-"
	}
	return "'" + s + "'"
}

func boolStr(b bool) string {
	if b {
		return "да"
	}
	return "нет"
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// parseArgs разбирает аргументы: первый флаг задаёт режим,
// остальные объединяются в пользовательский запрос (если есть).
func parseArgs(args []string) (mode, prompt string, err error) {
	if len(args) == 0 {
		return "", "", fmt.Errorf("не указан режим. Используйте --free | --limited | --run-all")
	}

	first := args[0]
	rest := args[1:]

	switch first {
	case "--free":
		mode = "free"
	case "--limited":
		mode = "limited"
	case "--run-all":
		mode = "run-all"
	default:
		return "", "", fmt.Errorf("неизвестный флаг %q", first)
	}

	if len(rest) > 0 {
		prompt = strings.Join(rest, " ")
	} else {
		prompt = defaultPrompt
	}
	return mode, prompt, nil
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// Программа отправляет запрос в LLM через OpenAI-совместимый API
// и выводит ответ в консоль.
//
// Клиент, чтение .env и параметры подключения вынесены в общий пакет llm
// (см. ../llm), подключаемый через replace в go.mod.
//
// Запрос можно передать аргументом командной строки или ввести с клавиатуры.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"aichallenge/llm"
)

func main() {
	client, err := llm.New()
	if err != nil {
		die("%v", err)
	}

	prompt := readPrompt()
	if prompt == "" {
		die("Ошибка: запрос не может быть пустым.")
	}

	out, err := client.Chat([]llm.Message{{Role: "user", Content: prompt}}, nil)
	if err != nil {
		die("Ошибка (запрос к LLM): %v", err)
	}
	fmt.Println(out)
}

// readPrompt возвращает запрос пользователя из аргументов командной строки,
// либо запрашивает ввод с клавиатуры в интерактивном режиме.
func readPrompt() string {
	if len(os.Args) > 1 {
		return strings.Join(os.Args[1:], " ")
	}

	fmt.Print("Введите запрос для LLM: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		die("Ошибка (чтение ввода): %v", err)
	}
	return strings.TrimSpace(line)
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

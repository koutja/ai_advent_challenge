// <DAY> — <краткое описание задачи>.
//
// Использует общий клиент пакета llm (см. ../llm). Настройки читаются каскадом
// в llm.New(): окружение -> локальный .env -> корневой .env -> дефолты.
package main

import (
	"fmt"
	"os"

	"aichallenge/llm"
)

func main() {
	client, err := llm.New()
	if err != nil {
		die("%v", err)
	}

	// TODO: разберите аргументы/флаги (паттерн parseArgs — первый аргумент режим,
	// остальные — запрос/задача), соберите сообщения.
	prompt := "Привет! Расскажи о себе одним предложением."

	out, err := client.Chat([]llm.Message{{Role: "user", Content: prompt}}, nil)
	if err != nil {
		die("Ошибка (запрос): %v", err)
	}
	fmt.Println(out)
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
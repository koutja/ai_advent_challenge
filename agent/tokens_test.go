package agent

import (
	"testing"

	"aichallenge/llm"
)

func TestEstimateTokens(t *testing.T) {
	if EstimateTokens("") != 0 {
		t.Fatal("пустой текст должен давать 0 токенов")
	}
	// ~4 символа на токен: 8 символов -> 2 токена.
	if got := EstimateTokens("12345678"); got != 2 {
		t.Fatalf("ожидали 2 токена, получили %d", got)
	}
	// Кириллица считается по рунам, а не байтам.
	if got := EstimateTokens("привет"); got == 0 {
		t.Fatal("непустая строка не должна давать 0")
	}
}

func TestMessagesTokens(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "12345678"},
		{Role: "assistant", Content: "12345678"},
	}
	if got := MessagesTokens(msgs); got != 4 {
		t.Fatalf("ожидали 4 токена, получили %d", got)
	}
}

func TestCost(t *testing.T) {
	p := Price{Input: 1.0, Output: 2.0} // $ за 1 млн
	// 1_000_000 входных + 500_000 выходных -> 1.00 + 1.00 = 2.00
	if got := Cost(p, 1_000_000, 500_000); got != 2.0 {
		t.Fatalf("ожидали 2.0, получили %v", got)
	}
	// Отрицательные значения (usage недоступно) считаем как 0.
	if got := Cost(p, -1, -1); got != 0 {
		t.Fatalf("ожидали 0 для недоступного usage, получили %v", got)
	}
}

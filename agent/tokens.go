package agent

import (
	"encoding/json"
	"os"

	"aichallenge/llm"
)

// Price — цена за 1 миллион токенов (USD): вход и выход.
type Price struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// TokenStats — метрики токенов и стоимости одного хода диалога.
//
// HistoryTokens — эвристическая оценка токенов всей сохранённой истории до этого
// хода; RequestTokens / ResponseTokens / TotalTokens — фактические значения из
// ответа API (если провайдер их отдаёт; иначе 0).
type TokenStats struct {
	Model          string
	HistoryTokens  int
	RequestTokens  int
	ResponseTokens int
	TotalTokens    int
	CostUSD        float64
	CostKnown      bool
}

// EstimateTokens — эвристическая оценка количества токенов по длине текста
// (~4 символа на токен). Точные значения даёт API через usage, эта функция нужна
// для локальной оценки до запроса и для всей истории.
func EstimateTokens(text string) int {
	n := len([]rune(text))
	if n <= 0 {
		return 0
	}
	return (n + 3) / 4
}

// MessagesTokens — суммарная оценка токенов всех сообщений.
func MessagesTokens(msgs []llm.Message) int {
	total := 0
	for _, m := range msgs {
		total += EstimateTokens(m.Content)
	}
	return total
}

// Cost возвращает стоимость в USD за prompt+completion токенов по цене модели.
func Cost(p Price, prompt, completion int) float64 {
	if prompt < 0 {
		prompt = 0
	}
	if completion < 0 {
		completion = 0
	}
	return (float64(prompt)*p.Input + float64(completion)*p.Output) / 1e6
}

// loadPriceCatalog читает цены моделей из каталога ../llm/models.json
// (поле price_usd_per_mtok у моделей). Ошибка некритична: при недоступности
// стоимость просто считается «неизвестной».
func loadPriceCatalog(path string) (map[string]Price, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Groups map[string][]struct {
			ID    string `json:"id"`
			Price Price  `json:"price_usd_per_mtok"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := map[string]Price{}
	for _, list := range raw.Groups {
		for _, m := range list {
			if m.ID != "" && m.Price.Input > 0 {
				out[m.ID] = m.Price
			}
		}
	}
	return out, nil
}

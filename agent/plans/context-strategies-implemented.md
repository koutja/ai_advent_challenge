# Стратегии управления контекстом — итог этапа

> Единый файл по итогам реализации: объединяет дизайн из
> [`context-management-plan.md`](context-management-plan.md) и статус из
> [`context-strategies-checklist.md`](context-strategies-checklist.md).
> Все пункты чек-листа ниже — фактически реализованы и покрыты юнит-тестами
> (`make test` зелёный).

## Цель

Дать агенту переключаемые стратегии управления контекстом **без сжатия-сводки**
и сравнить их на одном сценарии («собираем ТЗ»): Sliding Window / Facts / Branching.
Сравнение — по качеству ответа, стабильности, расходу токенов и удобству пользователя.
Существующий `ContextManager` (summary-подход Этапа 4) оставлен для обратной
совместимости, но в новую модель не вводится.

---

## Базовая архитектура

### Интерфейс `ContextStrategy` ([`context.go`](../context.go))

```go
type ContextStrategy interface {
	Name() string
	// History возвращает релевантную историю: для Branching — активная ветка,
	// для остальных — mem.Load().
	History(mem Memory) []llm.Message
	Build(hist []llm.Message, input string) []llm.Message
	Observe(hist []llm.Message) error
	Reset() error
	State() string
}
```

### Селектор ([`context.go`](../context.go))

`NewStrategy(name, client, cfg)` создаёт стратегию по имени `window | facts | branch`;
невалидное имя → ошибка.

### Изменения в `Agent` ([`agent.go`](../agent.go))

- Добавлено поле `strategy ContextStrategy`; legacy `compress *ContextManager` сохранён.
- Методы `SetStrategy(name) error`, `StrategyName() string`, `Strategy() ContextStrategy`.
- `Say()`:
  1. `hist := a.strategy.History(a.memory)` (для Branching — активная ветка);
  2. `msgs := a.strategy.Build(hist, input)`;
  3. отправка в LLM;
  4. сохранение хода (общая `Memory`, кроме Branching — ветки живут в RAM стратегии);
  5. `a.strategy.Observe(обновлённая история)`.
- Обратная совместимость: если `cfg.ContextStrategy == ""` и `cfg.Compress == true` —
  включается legacy `ContextManager`.

### Конфиг ([`config.go`](../config.go), [`config.json`](../config.json))

```go
ContextStrategy string `json:"context_strategy"` // window | facts | branch (пусто = off/legacy)
WindowSize      int    `json:"window_size"`
FactsKeepLast   int    `json:"facts_keep_last"`
FactsKeyMax     int    `json:"facts_key_max"`
FactsExtractor  string `json:"facts_extractor"` // "llm" | "heuristic"
```

Дефолты: стратегия `window`, `window_size=10`, `facts_keep_last=10`,
`facts_key_max=50`, `facts_extractor=llm`. В `config.json` и `config.demo.json`
добавлены соответствующие поля.

---

## Стратегия 1: Sliding Window ([`window.go`](../window.go))

Режет историю до последних N сообщений, всё старше отбрасывается на уровне запроса.

```go
func (w *SlidingWindow) Build(hist []llm.Message, input string) []llm.Message {
	n := len(hist)
	if n > w.size {
		hist = hist[n-w.size:]
	}
	return append(append([]llm.Message{}, hist...), llm.Message{Role: "user", Content: input})
}
```

- Самый дешёвый по токенам, но ранние детали (цель/дедлайн) теряются при N < длины.
- `Observe`/`Reset` — no-op.

---

## Стратегия 2: Facts / Key-Value Memory ([`facts.go`](../facts.go))

System-блок «Факты диалога» + последние N сообщений. Извлечение фактов — два режима:

- **LLM-режим** (`ExtractorLLM`, по умолчанию): отдельный вызов LLM с промптом
  «извлеки ключ-значение: цель, ограничения, предпочтения, решения, договорённости».
  Точнее, но стоит дополнительных токенов (учитывается в сравнении).
- **Хьюристический режим** (`ExtractorHeuristic`): регулярки по маркерам
  (`цель:`, `ограничение:`, `дедлайн:`, …). Детерминировано, без LLM; корректно
  разделяет несколько фактов на одной строке.

```go
func (f *FactsMemory) Build(hist []llm.Message, input string) []llm.Message {
	var msgs []llm.Message
	if len(f.facts) > 0 {
		msgs = append(msgs, llm.Message{Role: "system", Content: "Факты диалога:\n" + f.render()})
	}
	if n := len(hist); n > f.keepLast {
		hist = hist[n-f.keepLast:]
	}
	msgs = append(msgs, hist...)
	msgs = append(msgs, llm.Message{Role: "user", Content: input})
	return msgs
}
```

- `Observe` извлекает факты из последнего user-сообщения и мержит в `f.facts`
  (лимит ключей `keyMax` вытесняет самый «старый» по алфавиту ключ).
- `State()`/`Facts()` возвращают текущие факты.
- Держит важные детали дольше Sliding Window при низком расходе на вывод.

---

## Стратегия 3: Branching ([`branch.go`](../branch.go))

Checkpoint-ы и ветки диалога; стратегия владеет собственными копиями истории.

```go
type Branching struct {
	branches map[string][]llm.Message // id ветки -> история
	active   string                   // активная ветка
	nextID   int
}
// Checkpoint(name) — снапшот активной ветки (имя = контрольная точка)
// Branch(name)     — новая ветка-копия от активной + переключение
// Switch(name)     — переключение активной ветки
// Active() / Branches() []string
```

- `History(mem)` возвращает `branches[active]`; `Observe` аппендит ход в активную ветку.
- Общая `Memory` при активной ветке не пополняется (ветки живут в RAM стратегии).
- Полная история ветки → максимальная точность и стабильность, но самый высокий расход токенов.

---

## Интеграция в CLI ([`cmd/cli/main.go`](../cmd/cli/main.go))

Команды REPL:

| Команда | Описание |
|---------|----------|
| `/strategy` | показать текущую стратегию |
| `/strategy window\|facts\|branch` | переключить стратегию |
| `/checkpoint <имя>` | контрольная точка в Branching |
| `/branch <имя>` | ветка-копия от активной + переключение |
| `/switch <имя>` | переключение между ветками |
| `/facts` | показать текущие факты (Facts) |
| `/reset`, `/compress`, `/exit` | существующие (compres — legacy) |

Флаг сравнения:

```bash
go run ./cmd/cli --compare-strategies   # или make run-compare-strategies
```

Логика сценария/метрик/анализа вынесена в пакет `agent` ([`compare.go`](../compare.go)):
`CompareStrategies(cfg)` возвращает `[]StrategyResult`, `AnalyzeStrategies(results)`
возвращает текстовый анализ с рекомендацией. Используется и CLI, и вебом.

## Веб-интерфейс

- Флаг `--strategy window|facts|branch` — стратегия по умолчанию при старте
  (`go run ./cmd/web --strategy branch`).
- Эндпоинт `GET /strategy` — текущая стратегия + доступные;
  `POST /strategy {"strategy":"..."}` — переключение.
- Эндпоинт `POST /compare` — прогон сценария на всех трёх стратегиях, возвращает
  `{results:[...], analysis:"..."}`.
- Страница [`web/index.html`](../web/index.html): селектор стратегии + кнопка
  «Сравнить стратегии»; результат — таблица метрик и блок анализа.

---

## Сравнительный прогон (harness)

`runCompareStrategies(cfg)` в CLI:

1. **Сценарий** — фиксированный скрипт ~12 ходов «собираем ТЗ»: цель, аудитория,
   фичи, ограничения, анти-фичи, дедлайн, предпочтения по стеку, договорённости.
   Плюс финальный проверочный вопрос «Перечисли цель, ограничения и дедлайн».
2. Для каждой стратегии — **свежий** агент (`agent.New` + `InMemory`), тот же сценарий.
3. **Метрики**:
   - **Качество**: доля ключевых терминов сценария, воспроизведённых в проверочном ответе;
   - **Стабильность**: доля категорий (цель/ограничения/дедлайн), не потерянных к концу;
   - **Расход токенов**: сумма `request+completion` из `Usage` по всем ходам;
   - **UX**: число ходов сценария.
4. Вывод: таблица-сравнение в консоль + markdown-отчёт `plans/compare-context.md`.

Ожидаемые тенденции (для проверки прогоном):
- **window** — минимум токенов, риск потери ранних фактов при маленьком N;
- **facts** — баланс: мало токенов на вывод, детали держит, но тратит на извлечение;
- **branch** — максимум токенов и стабильности в ветке, гибкий UX, сложнее управление.

---

## Файлы этапа

| Файл | Действие |
|------|----------|
| [`context.go`](../context.go) | новый: интерфейс `ContextStrategy` + селектор |
| [`window.go`](../window.go) | новый: `SlidingWindow` |
| [`facts.go`](../facts.go) | новый: `FactsMemory` + извлечение фактов (LLM/хьюристика) |
| [`branch.go`](../branch.go) | новый: `Branching` + checkpoint/branch/switch |
| [`agent.go`](../agent.go) | правка: `Say()` через стратегию, `SetStrategy`, `StrategyName`, `Strategy` |
| [`config.go`](../config.go) | правка: `ContextStrategy` + параметры |
| [`config.json`](../config.json), [`config.demo.json`](../config.demo.json) | правка: `context_strategy` |
| [`cmd/cli/main.go`](../cmd/cli/main.go) | правка: команды + `--compare-strategies` (через `agent.CompareStrategies`) |
| [`compare.go`](../compare.go) | новый: `StrategyResult`, `CompareStrategies`, `AnalyzeStrategies` |
| [`compare_test.go`](../compare_test.go) | новый: анализ/оценка (без LLM) |
| [`cmd/web/main.go`](../cmd/web/main.go) | правка: флаг `--strategy`, эндпоинты `/strategy`, `/compare` |
| [`web/index.html`](../web/index.html) | правка: селектор стратегии + кнопка сравнения + панель анализа |
| [`window_test.go`](../window_test.go) | новый: режет до N, сохраняет input, no-op Observe |
| [`facts_test.go`](../facts_test.go) | новый: хьюристика, мерж, сборка запроса, лимит ключей |
| [`branch_test.go`](../branch_test.go) | новый: независимость историй веток |
| [`context_test.go`](../context_test.go) | новый: селектор, `SetStrategy` |
| [`README.md`](../README.md) | правка: документация стратегий, команд и веба |
| [`Makefile`](../Makefile) | правка: цель `run-compare-strategies` |

---

## Чек-лист (статус: выполнено)

### Базовая архитектура
- [x] Интерфейс `ContextStrategy` в `context.go`: `Name`, `Build`, `Observe`, `Reset`, `State` + `History(mem)`.
- [x] Селектор `NewStrategy` (`window | facts | branch`), невалидное имя → ошибка.
- [x] `Agent`: поле `strategy ContextStrategy`; `SetStrategy`, `StrategyName`; legacy `compress` сохранён.

### Стратегия 1: Sliding Window
- [x] Тип `SlidingWindow` с полем `size` (дефолт из конфига).
- [x] `Build`: последние N сообщений + user-ввод.
- [x] `Observe` / `Reset` — no-op.
- [x] Юнит-тест `window_test.go`.

### Стратегия 2: Facts / Key-Value Memory
- [x] Тип `FactsMemory`: `map[string]string`, `keepLast`, `keyMax`, extractor.
- [x] Извлечение фактов двумя режимами: LLM-промпт и хьюристика.
- [x] `Observe`: извлекать факты из последнего user-сообщения и мержить.
- [x] `Build`: system-блок «Факты диалога» + последние N сообщений + ввод.
- [x] `State()` возвращает текущие факты.
- [x] Юнит-тест `facts_test.go` (хьюристика, без LLM).

### Стратегия 3: Branching
- [x] Тип `Branching`: `branches`, `active`, `nextID`.
- [x] `Checkpoint(name)` — снапшот активной ветки.
- [x] `Branch(name)` — новая ветка-копия + переключение.
- [x] `Switch(name)` — переключение активной ветки.
- [x] `History(mem)` возвращает историю активной ветки.
- [x] `Observe` аппендит ход в активную ветку.
- [x] Юнит-тест `branch_test.go`.

### Интеграция
- [x] `Agent.Say()` использует `strategy.History(mem)` + `strategy.Build(...)`, после хода — `strategy.Observe(...)`.
- [x] `config.go`: поле `ContextStrategy` + параметры; дефолт — `window`.
- [x] `config.json`: `"context_strategy": "window"` (и в `config.demo.json`).

### CLI
- [x] Команда `/strategy` — показать/переключить.
- [x] Команды `/checkpoint`, `/branch`, `/switch`.
- [x] Команда `/facts`.
- [x] Флаг `--compare-strategies`.

### Веб
- [x] Селектор стратегии на странице + переключение через `POST /strategy`.
- [x] Кнопка «Сравнить стратегии» (`POST /compare`) — прогон всех трёх стратегий.
- [x] Панель результатов (таблица метрик) + блок текстового анализа с рекомендацией.
- [x] Флаг `--strategy window|facts|branch` — стратегия по умолчанию при старте сервера.
- [x] Логика сравнения вынесена в пакет `agent` (`CompareStrategies`/`AnalyzeStrategies`).

### Тесты
- [x] `context_test.go`: селектор валидными/невалидными именами; `SetStrategy` переключает стратегию.
- [x] `make test` — все тесты зелёные.

### Сравнение (harness)
- [x] Функция `runCompareStrategies`: свежий агент на каждую стратегию.
- [x] Сценарий «собираем ТЗ» ~12 ходов + финальный проверочный вопрос.
- [x] Метрики: качество, стабильность, токены (request+completion), UX (число ходов).
- [x] Вывод: таблица в консоль + отчёт `plans/compare-context.md` (создаётся при `--compare-strategies`).

### Документация
- [x] `README.md`: описание стратегий, команды, флаг сравнения, пример запуска.
- [ ] Итоговый отчёт со сравнением по 4 критериям — заполняется после прогона `--compare-strategies`.
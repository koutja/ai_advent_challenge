# Чек-лист: 3 стратегии управления контекстом

> Сверяемся по этому файлу во время реализации. Подробный дизайн — в
> [`context-management-plan.md`](context-management-plan.md).
> Порядок шагов = порядок реализации. Отмечаем `[x]` по мере готовности.

## Базовая архитектура

- [ ] **Интерфейс `ContextStrategy`** в новом `context.go`:
      `Name()`, `Build(hist, input)`, `Observe(hist)`, `Reset()`, `State()`.
      Плюс для ветвления — метод получения истории `History(mem)`.
- [ ] **Селектор стратегий**: функция создания стратегии по имени
      (`window | facts | branch`), невалидное имя → ошибка.
- [ ] **`Agent`**: поле `strategy ContextStrategy`; методы
      `SetStrategy(name) error`, `StrategyName() string`.
      Legacy `compress *ContextManager` остаётся (обратная совместимость).

## Стратегия 1: Sliding Window (`window.go`)

- [ ] Тип `SlidingWindow` с полем `size` (дефолт из конфига).
- [ ] `Build`: берём последние N сообщений из истории + текущий user-ввод.
- [ ] `Observe` / `Reset` — no-op.
- [ ] Юнит-тест `window_test.go`: режет до N, сохраняет input.

## Стратегия 2: Facts / Key-Value Memory (`facts.go`)

- [ ] Тип `FactsMemory`: `map[string]string`, `keepLast`, extractor.
- [ ] Извлечение фактов двумя режимами: LLM-промпт и хьюристика (маркеры
      «цель:», «ограничение:», «дедлайн:» и т.п.).
- [ ] `Observe`: извлекать факты из последнего user-сообщения и мержить.
- [ ] `Build`: system-блок «Факты диалога» + последние N сообщений + ввод.
- [ ] `State()` возвращает текущие факты (для отладки/сравнения).
- [ ] Юнит-тест `facts_test.go` (хьюристика, без LLM): извлечение + мерж + сборка.

## Стратегия 3: Branching (`branch.go`)

- [ ] Тип `Branching`: `branches map[string][]Message`, `active`, `nextID`.
- [ ] `Checkpoint(name)` — снапшот активной ветки.
- [ ] `Branch(name)` — новая ветка-копия от checkpoint + переключение.
- [ ] `Switch(name)` — переключение активной ветки.
- [ ] `History(mem)` возвращает историю активной ветки.
- [ ] `Observe` аппендит ход в активную ветку.
- [ ] Юнит-тест `branch_test.go`: независимость историй веток.

## Интеграция

- [ ] `Agent.Say()` использует `strategy.History(mem)` + `strategy.Build(...)`,
      после сохранения хода вызывает `strategy.Observe(...)`.
- [ ] [`config.go`](config.go:19): поле `ContextStrategy` + параметры
      (`window_size`, лимит ключей facts). Дефолт — `window`.
- [ ] [`config.json`](config.json): добавить `"context_strategy": "window"`.

## CLI ([`cmd/cli/main.go`](cmd/cli/main.go))

- [ ] Команда `/strategy` — показать/переключить (`window|facts|branch`).
- [ ] Команды `/checkpoint <имя>`, `/branch <имя>`, `/switch <имя>`.
- [ ] Команда `/facts` — показать текущие факты.
- [ ] Флаг `--compare-strategies` — прогон сценария на всех трёх стратегиях.

## Тесты

- [ ] `context_test.go`: селектор валидными/невалидными именами; `Say`
      использует выбранную стратегию.
- [ ] Прогнать `make test` — все тесты зелёные.

## Сравнение (harness)

- [ ] Функция `runCompareStrategies`: свежий агент на каждую стратегию.
- [ ] Сценарий «собираем ТЗ» ~12 ходов + финальный проверочный вопрос
      («перечисли цель, ограничения, дедлайн»).
- [ ] Метрики: качество (балл по фактам), стабильность (потеря деталей),
      токены (сумма request+completion из `Usage`), UX (число команд).
- [ ] Вывод: таблица-сравнение в консоль + отчёт `plans/compare-context.md`.

## Документация

- [ ] [`README.md`](README.md): описание стратегий, команды, флаг сравнения,
      пример запуска `--compare-strategies`.
- [ ] Итоговый отчёт со сравнением по 4 критериям (quality/stability/tokens/UX).
// day_05 — Версии моделей: один и тот же запрос на слабой/средней/сильной
// модели. Замеряем время ответа, количество токенов и стоимость; сравниваем
// качество (рейтинг Elo из каталога llm/models.json) и скорость.
//
// Прогон устойчив к ошибкам: если что-то нельзя получить (usage, цена, ответ),
// в отчёте ставится «–», а не падение. Лог пишется в logs/day05.log, отчёт —
// в results/day05_report.md.
package main

import (
	"aichallenge/llm"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	configPath  = "config.json"
	catalogPath = "../llm/models.json"
	logPath     = "logs/day05.log"
	reportPath  = "results/day05_report.md"
)

func main() {
	mode, query, err := parseArgs(os.Args[1:])
	if err != nil {
		die("%v\n\n%s", err, usage())
	}
	if mode == "help" {
		fmt.Print(usage())
		return
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		die("не удалось прочитать конфиг %s: %v", configPath, err)
	}
	if query == "" {
		query = cfg.DefaultQuery
	}

	// Каталог нужен для названий/рейтинга; если его нет — продолжим, поля будут «–».
	catalog, _ := loadCatalog(catalogPath)

	// Ключи хранятся в .env, а не в экспортированном окружении: загружаем их
	// каскадом (локальный .env → корневой), иначе NewWithConfig не найдёт ключ.
	llm.LoadEnvCascade()

	selected := selectTiers(cfg, mode)

	log, err := newLogger(logPath)
	if err != nil {
		die("не удалось открыть лог %s: %v", logPath, err)
	}
	defer log.close()

	log.logf("START mode=%s query=%q tiers=%s", mode, query, strings.Join(tierNames(selected), ","))

	results := runAll(cfg, selected, catalog, query, log)

	report := formatReport(results, query)

	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		log.logf("ERROR mkdir %s: %v", reportPath, err)
		die("не удалось создать папку для отчёта: %v", err)
	}
	if err := os.WriteFile(reportPath, []byte(report), 0o644); err != nil {
		log.logf("ERROR write report: %v", err)
		die("не удалось сохранить отчёт: %v", err)
	}
	log.logf("REPORT saved %s", reportPath)

	fmt.Println(report)
}

// parseArgs: первый аргумент — режим, остальные — текст запроса.
func parseArgs(args []string) (mode, query string, err error) {
	if len(args) == 0 {
		return "all", "", nil
	}
	switch args[0] {
	case "--help", "-h":
		return "help", "", nil
	case "--run-all":
		return "all", joinQuery(args[1:]), nil
	case "--cheap":
		return "cheap", joinQuery(args[1:]), nil
	case "--tier":
		if len(args) < 2 {
			return "", "", fmt.Errorf("--tier требует имя тира (weak|medium|strong)")
		}
		name := args[1]
		if !validTier(name) {
			return "", "", fmt.Errorf("неизвестный тир %q (ожидается weak|medium|strong)", name)
		}
		return "tier:" + name, joinQuery(args[2:]), nil
	default:
		return "", "", fmt.Errorf("неизвестный режим %q (см. --help)", args[0])
	}
}

func validTier(name string) bool {
	for _, k := range []string{"weak", "medium", "strong"} {
		if name == k {
			return true
		}
	}
	return false
}

func joinQuery(parts []string) string { return strings.Join(parts, " ") }

// selectTiers по выбранному режиму возвращает список тиров для прогона.
func selectTiers(cfg *Config, mode string) []TierCfg {
	switch mode {
	case "cheap": // быстрая отладка на дешёвой модели
		for _, t := range cfg.Tiers {
			if t.Name == "weak" {
				return []TierCfg{t}
			}
		}
		if len(cfg.Tiers) > 0 {
			return cfg.Tiers[:1]
		}
		return nil
	case "all":
		return cfg.Tiers
	default: // tier:<name>
		name := strings.TrimPrefix(mode, "tier:")
		for _, t := range cfg.Tiers {
			if t.Name == name {
				return []TierCfg{t}
			}
		}
		fmt.Fprintf(os.Stderr, "⚠ Тир %q не найден в config.json, запускаю все.\n", name)
		return cfg.Tiers
	}
}

func tierNames(tiers []TierCfg) []string {
	names := make([]string, 0, len(tiers))
	for _, t := range tiers {
		names = append(names, t.Name)
	}
	return names
}

func usage() string {
	return `Использование: go run . [режим] [текст запроса]

Режимы:
  (без аргументов)  прогнать запрос на всех настроенных тирах
  --run-all [текст] то же самое явно
  --cheap [текст]   быстрая отладка: только дешёвая (weak) модель
  --tier <name> [текст] одна конкретная модель: weak | medium | strong
  --help, -h        эта справка

Текст запроса можно передать сразу после режима (остаток аргументов склеивается
в одну строку). Если не задан — берётся default_query из config.json.

Примеры:
  go run . --cheap
  go run . --tier weak "Что такое рекурсия?"
  go run . --run-all "Придумай заголовок статьи"

Отчёт сохраняется в results/day05_report.md, построчный лог — в logs/day05.log.
Конфигурация моделей и цен: config.json; каталог моделей: ../llm/models.json.`
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

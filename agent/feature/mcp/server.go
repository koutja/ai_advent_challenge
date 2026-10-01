// Package mcpx — интеграция Model Context Protocol (MCP) в ядро агента.
//
// Пакет построен на официальном Go SDK: github.com/modelcontextprotocol/go-sdk.
// Компоненты:
//
//   - BuildServer / NewServer — MCP-сервер с инструментами. Один бинарник
//     cmd/mcp-server может обслуживать разные «домены» (--server):
//     tasks | scheduler | knowledge | all. Это позволяет регистрировать
//     несколько MCP-серверов и маршрутизировать запросы между ними (registry.go).
//   - Connect — stdio-клиент, который запускает сервер как дочерний процесс
//     и устанавливает соединение (handshake Initialize). Клиент умеет
//     перечислять инструменты (ListTools) и вызывать их (CallTool).
//
// Домены инструментов:
//   - tasks      — get_task / create_task (встроенный mock HTTP-API, см. api.go);
//   - scheduler  — reminder_add / reminders_status / collect_start /
//     collect_status / summary_start / get_summary (см. store.go, scheduler.go);
//   - knowledge  — search / summarize / save_to_file / generate_document
//     (см. pipeline.go, corpus.go);
//   - all        — демо (get_time, echo) + все домены выше.
package mcpx

import (
	"agent/feature/rag"
	"aichallenge/llm"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Идентификация сервера, который сообщается клиенту при Initialize.
const (
	ServerName    = "agent-mcp-demo"
	ServerVersion = "1.0.0"
)

// Имена доменов MCP-серверов.
const (
	ServerTasks     = "tasks"
	ServerScheduler = "scheduler"
	ServerKnowledge = "knowledge"
)

// NewServer собирает MCP-сервер со всеми инструментами (обратная совместимость).
func NewServer() *mcp.Server {
	return BuildServer("all")
}

// BuildServer собирает MCP-сервер с инструментами указанного домена:
// tasks | scheduler | knowledge | all (по умолчанию all).
func BuildServer(kind string) *mcp.Server {
	switch kind {
	case ServerTasks:
		return registerTasks(newMockAPI())
	case ServerScheduler:
		st := openDefaultStore()
		sch := NewScheduler(st)
		sch.Start()
		return registerScheduler(st)
	case ServerKnowledge:
		return registerKnowledge()
	default:
		api := newMockAPI()
		st := openDefaultStore()
		sch := NewScheduler(st)
		sch.Start()
		s := newBaseServer()
		addDemoTools(s)
		addTasksTools(s, api)
		addSchedulerTools(s, st)
		addKnowledgeTools(s)
		return s
	}
}

// openDefaultStore открывает JSON-хранилище планировщика. Путь — из env
// MCP_DATA_FILE, иначе results/mcp_scheduler_data.json. При любой ошибке
// продолжает работу в памяти (без падения сервера).
func openDefaultStore() *Store {
	path := defaultDataFile
	if p := os.Getenv("MCP_DATA_FILE"); p != "" {
		path = p
	}
	st, err := OpenStore(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp: хранилище %s недоступно (%v), работаю в памяти\n", path, err)
		st, _ = OpenStore("")
	}
	return st
}

func newBaseServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: ServerVersion}, nil)
}

func registerTasks(api *mockAPI) *mcp.Server {
	s := newBaseServer()
	addTasksTools(s, api)
	return s
}

func registerScheduler(st *Store) *mcp.Server {
	s := newBaseServer()
	addSchedulerTools(s, st)
	return s
}

func registerKnowledge() *mcp.Server {
	s := newBaseServer()
	addKnowledgeTools(s)
	return s
}

// addDemoTools регистрирует общие демо-инструменты (только в домене all).
func addDemoTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_time",
		Description: "Возвращает текущее время сервера в формате RFC3339.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, string, error) {
		return nil, time.Now().Format(time.RFC3339), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "echo",
		Description: "Возвращает переданное сообщение без изменений.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, echoOutput, error) {
		return nil, echoOutput{Echo: in.Message}, nil
	})
}

// addTasksTools регистрирует инструменты домена tasks (mock HTTP API).
func addTasksTools(s *mcp.Server, api *mockAPI) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_task",
		Description: "Возвращает задачу из mock API по её id.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in getTaskInput) (*mcp.CallToolResult, Task, error) {
		t, err := api.getTask(in.TaskID)
		if err != nil {
			return nil, Task{}, fmt.Errorf("get_task: %w", err)
		}
		return nil, t, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_task",
		Description: "Создаёт задачу в mock API и возвращает её полную запись.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in createTaskInput) (*mcp.CallToolResult, Task, error) {
		t, err := api.createTask(in.Title, in.Priority, in.Assignee)
		if err != nil {
			return nil, Task{}, fmt.Errorf("create_task: %w", err)
		}
		return nil, t, nil
	})
}

// addKnowledgeTools регистрирует инструменты домена knowledge (пайплайн/корпус).
func addKnowledgeTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "search",
		Description: "Ищет документы в локальном корпусе и возвращает id/title/snippet.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, SearchResult, error) {
		return nil, Search(in.Query, in.Limit), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "summarize",
		Description: "Строит детерминированную сводку текста (первые предложения до лимита слов).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in summarizeInput) (*mcp.CallToolResult, SummarizeResult, error) {
		return nil, Summarize(in.Text, in.MaxWords), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "save_to_file",
		Description: "Сохраняет контент в файл (папка MCP_OUTPUT_DIR или results/pipeline) и возвращает путь.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in saveToFileInput) (*mcp.CallToolResult, SaveFileResult, error) {
		res, err := SaveFile(in.Filename, in.Content)
		if err != nil {
			return nil, SaveFileResult{}, err
		}
		return nil, res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "generate_document",
		Description: "Формирует документ по теме через LLM (LLM_API_KEY/LLM_BASE_URL/LLM_MODEL из .env), добавляет его в корпус и возвращает id/title/snippet. Без LLM — детерминированный fallback.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in generateDocInput) (*mcp.CallToolResult, GeneratedDoc, error) {
		doc, err := GenerateDocument(in.Topic)
		if err != nil {
			return nil, GeneratedDoc{}, err
		}
		return nil, doc, nil
	})

	// --- RAG: доступ к индексу index_service (микросервис serve.py + fallback) ---
	// Конфигурация ретривера для автономного MCP-процесса берётся из env RAG_*;
	// в ядре агента (SayRAG) используется секция rag из agent/config.json.
	mcp.AddTool(s, &mcp.Tool{
		Name:        "rag_search",
		Description: "Семантический поиск по индексу index_service: вопрос → top-k чанков с источниками (source/section). Микросервис serve.py; если недоступен — ключевой поиск по chunks.jsonl.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in ragSearchInput) (*mcp.CallToolResult, ragSearchOutput, error) {
		res, err := rag.NewRetrieverFromEnv().Retrieve(context.Background(), in.Query)
		if err != nil {
			return nil, ragSearchOutput{}, fmt.Errorf("rag_search: %w", err)
		}
		return nil, ragSearchOutput{Engine: res.Engine, Strategy: res.Strategy, Chunks: res.Chunks}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "rag_answer",
		Description: "Первый RAG-запрос: вопрос → поиск чанков в индексе index_service → контекст с источниками + вопрос → ответ LLM со ссылками [1]…[n].",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ragAnswerInput) (*mcp.CallToolResult, ragAnswerOutput, error) {
		client, err := llm.New()
		if err != nil {
			return nil, ragAnswerOutput{}, fmt.Errorf("rag_answer: LLM: %w", err)
		}
		ans, err := rag.AnswerRAG(ctx, client, rag.NewRetrieverFromEnv(), in.Query, 0)
		if err != nil {
			return nil, ragAnswerOutput{}, fmt.Errorf("rag_answer: %w", err)
		}
		return nil, ragAnswerOutput{Answer: ans.Text, Engine: ans.Engine, Sources: ans.Sources}, nil
	})
}

// addSchedulerTools регистрирует инструменты домена scheduler (планировщик).
func addSchedulerTools(s *mcp.Server, st *Store) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "reminder_add",
		Description: "Ставит отложенное напоминание; сработает через in_minutes (0 — при ближайшем тике).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in reminderAddInput) (*mcp.CallToolResult, reminderAddOutput, error) {
		r, err := st.AddReminder(in.Text, time.Now().Add(time.Duration(in.InMinutes)*time.Minute))
		if err != nil {
			return nil, reminderAddOutput{}, fmt.Errorf("reminder_add: %w", err)
		}
		return nil, reminderAddOutput{ID: r.ID, Text: r.Text, DueAt: r.DueAt}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "reminders_status",
		Description: "Агрегированная сводка по напоминаниям: total / pending / fired.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, reminderStatsOutput, error) {
		rs := st.ReminderStats()
		return nil, reminderStatsOutput{Total: rs.Total, Pending: rs.Pending, Fired: rs.Fired}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "collect_start",
		Description: "Запускает периодический сбор точек данных метрики (детерминированные значения).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in collectStartInput) (*mcp.CallToolResult, collectStartOutput, error) {
		if in.Metric == "" {
			return nil, collectStartOutput{}, fmt.Errorf("collect_start: metric обязателен")
		}
		iv := in.IntervalSec
		if iv < 1 {
			iv = 1
		}
		j, err := st.AddJob("collect", in.Metric, iv, in.Iterations, time.Now())
		if err != nil {
			return nil, collectStartOutput{}, fmt.Errorf("collect_start: %w", err)
		}
		return nil, collectStartOutput{ID: j.ID, Metric: j.Metric, IntervalSec: j.IntervalSec, Iterations: j.Iterations, NextRunAt: j.NextRunAt}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "collect_status",
		Description: "Агрегат по точкам данных: count / min / max / avg / latest (можно отфильтровать по metric).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in collectStatusInput) (*mcp.CallToolResult, map[string]metricView, error) {
		out := make(map[string]metricView)
		for _, j := range st.Jobs() {
			if j.Kind != "collect" {
				continue
			}
			if in.Metric != "" && j.Metric != in.Metric {
				continue
			}
			out[j.Metric] = metricViewFromJob(j)
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "summary_start",
		Description: "Запускает периодические снимки сводки (агрегат на текущий момент).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in summaryStartInput) (*mcp.CallToolResult, summaryStartOutput, error) {
		iv := in.IntervalSec
		if iv < 1 {
			iv = 5
		}
		j, err := st.AddJob("summary", "", iv, 0, time.Now())
		if err != nil {
			return nil, summaryStartOutput{}, fmt.Errorf("summary_start: %w", err)
		}
		return nil, summaryStartOutput{ID: j.ID, Kind: j.Kind, IntervalSec: j.IntervalSec, NextRunAt: j.NextRunAt}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_summary",
		Description: "Возвращает агрегированную сводку: напоминания, метрики и число снимков summary.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, getSummaryOutput, error) {
		now := time.Now()
		agg := st.Aggregate(now)
		snaps := st.Summaries()

		// Метрики строим по задачам (с корректными iterations/done/running),
		// как в collect_status; агрегат agg служит источником для reminders.
		metrics := make(map[string]metricView)
		for _, j := range st.Jobs() {
			if j.Kind != "collect" {
				continue
			}
			metrics[j.Metric] = metricViewFromJob(j)
		}
		last := ""
		if len(snaps) > 0 {
			last = snaps[len(snaps)-1].At
		}
		return nil, getSummaryOutput{
			At:             agg.At,
			Reminders:      reminderStatsOutput{Total: agg.Reminders.Total, Pending: agg.Reminders.Pending, Fired: agg.Reminders.Fired},
			Metrics:        metrics,
			Snapshots:      len(snaps),
			LastSnapshotAt: last,
		}, nil
	})
}

// --- Типы ввода/вывода инструментов (схемы выводятся автоматически) ---

type echoInput struct {
	// Message — текст для возврата.
	Message string `json:"message" jsonschema:"текст, который нужно вернуть"`
}

type echoOutput struct {
	// Echo — текст, полученный от клиента.
	Echo string `json:"echo" jsonschema:"возвращённый текст"`
}

type getTaskInput struct {
	// TaskID — идентификатор задачи в mock API (например T-001).
	TaskID string `json:"task_id" jsonschema:"идентификатор задачи, например T-001"`
}

type createTaskInput struct {
	// Title — заголовок создаваемой задачи (обязательно).
	Title string `json:"title" jsonschema:"заголовок задачи (обязательное поле)"`
	// Priority — приоритет: low|medium|high (по умолчанию medium).
	Priority string `json:"priority,omitempty" jsonschema:"приоритет: low|medium|high (по умолчанию medium)"`
	// Assignee — исполнитель задачи (необязательно).
	Assignee string `json:"assignee,omitempty" jsonschema:"исполнитель задачи (необязательно)"`
}

type searchInput struct {
	// Query — поисковый запрос (обязательно).
	Query string `json:"query" jsonschema:"поисковый запрос (обязательное поле)"`
	// Limit — максимум результатов (по умолчанию 5).
	Limit int `json:"limit,omitempty" jsonschema:"максимум результатов; по умолчанию 5"`
}

type summarizeInput struct {
	// Text — текст для сводки.
	Text string `json:"text" jsonschema:"текст, из которого строится сводка"`
	// MaxWords — лимит слов сводки (по умолчанию 40).
	MaxWords int `json:"max_words,omitempty" jsonschema:"лимит слов сводки; по умолчанию 40"`
}

type generateDocInput struct {
	// Topic — тема документа (обязательно).
	Topic string `json:"topic" jsonschema:"тема документа (обязательное поле)"`
}

type saveToFileInput struct {
	// Filename — имя файла без путей (пишется в results/pipeline).
	Filename string `json:"filename" jsonschema:"имя файла без путей (пишется в results/pipeline)"`
	// Content — содержимое файла.
	Content string `json:"content" jsonschema:"содержимое файла"`
}

type ragSearchInput struct {
	// Query — поисковый запрос (обязательно).
	Query string `json:"query" jsonschema:"поисковый запрос (обязательное поле)"`
	// TopK — максимум чанков (по умолчанию из конфига, обычно 5).
	TopK int `json:"top_k,omitempty" jsonschema:"максимум чанков; по умолчанию из конфига"`
}

type ragSearchOutput struct {
	Engine   string      `json:"engine"`   // sidecar | keyword
	Strategy string      `json:"strategy"` // fixed | structure
	Chunks   []rag.Chunk `json:"chunks"`
}

type ragAnswerInput struct {
	// Query — вопрос для RAG-ответа (обязательно).
	Query string `json:"query" jsonschema:"вопрос для RAG-ответа (обязательное поле)"`
}

type ragAnswerOutput struct {
	Answer  string      `json:"answer"`
	Engine  string      `json:"engine"` // sidecar | keyword
	Sources []rag.Chunk `json:"sources,omitempty"`
}

type reminderAddInput struct {
	// Text — текст напоминания (обязательно).
	Text string `json:"text" jsonschema:"текст напоминания (обязательное поле)"`
	// InMinutes — через сколько минут сработает; 0 — при ближайшем тике.
	InMinutes int `json:"in_minutes" jsonschema:"через сколько минут сработает; 0 — сразу (ближайший тик)"`
}

type reminderAddOutput struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	DueAt string `json:"due_at"`
}

type reminderStatsOutput struct {
	Total   int `json:"total"`
	Pending int `json:"pending"`
	Fired   int `json:"fired"`
}

type collectStartInput struct {
	// Metric — имя метрики (например cpu, requests).
	Metric string `json:"metric" jsonschema:"имя метрики, например cpu или requests"`
	// IntervalSec — интервал сбора в секундах (по умолчанию 1).
	IntervalSec int `json:"interval_seconds" jsonschema:"интервал сбора в секундах; по умолчанию 1"`
	// Iterations — сколько точек собрать; 0 — бесконечно (до перезапуска сервера).
	Iterations int `json:"iterations" jsonschema:"сколько точек собрать; 0 — бесконечно (до перезапуска сервера)"`
}

type collectStartOutput struct {
	ID          string `json:"id"`
	Metric      string `json:"metric"`
	IntervalSec int    `json:"interval_seconds"`
	Iterations  int    `json:"iterations"`
	NextRunAt   string `json:"next_run_at"`
}

type collectStatusInput struct {
	// Metric — фильтр по метрике; пусто — показать все.
	Metric string `json:"metric,omitempty" jsonschema:"фильтр по метрике; пусто — все"`
}

// metricView — агрегированный вид серии данных.
type metricView struct {
	Count      int     `json:"count"`
	Min        float64 `json:"min"`
	Max        float64 `json:"max"`
	Avg        float64 `json:"avg"`
	Latest     float64 `json:"latest"`
	Iterations int     `json:"iterations"`
	Done       int     `json:"done"`
	Running    bool    `json:"running"`
}

func metricViewFromJob(j *Job) metricView {
	return metricViewFromStats(statsFromPoints(j.Points), j.Iterations, j.Done)
}

func metricViewFromStats(ms MetricStats, iterations, done int) metricView {
	return metricView{
		Count:      ms.Count,
		Min:        ms.Min,
		Max:        ms.Max,
		Avg:        ms.Avg,
		Latest:     ms.Latest,
		Iterations: iterations,
		Done:       done,
		Running:    iterations == 0 || done < iterations,
	}
}

type summaryStartInput struct {
	// IntervalSec — интервал снимков сводки в секундах (по умолчанию 5).
	IntervalSec int `json:"interval_seconds" jsonschema:"интервал снимков сводки в секундах; по умолчанию 5"`
}

type summaryStartOutput struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	IntervalSec int    `json:"interval_seconds"`
	NextRunAt   string `json:"next_run_at"`
}

type getSummaryOutput struct {
	At             string                `json:"at"`
	Reminders      reminderStatsOutput   `json:"reminders"`
	Metrics        map[string]metricView `json:"metrics"`
	Snapshots      int                   `json:"snapshots"`
	LastSnapshotAt string                `json:"last_snapshot_at,omitempty"`
}

.PHONY: run run-local run-web run-web-local run-stats run-compare run-compare-strategies run-compare-memory run-compare-profiles run-check-invariants run-mcp-tools run-mcp-call run-mcp-demo run-mcp-pipeline run-mcp-orchestrate run-rag run-rag-local run-rag-local-vs-cloud run-rag-optimize opt-create run-rag-eval run-rag-modes run-rag-citations run-chat run-chat-scenarios stop-web build-mcp-server build-web service-install service-stop service-detach service-detach-stop build test reset-history

# ===== Локальная LLM (День 27) =====
# Подключение к локальной модели через Ollama (OpenAI-совместимый эндпоинт).
# Инлайн env имеет приоритет над каскадом .env (llm.LoadEnv не перезаписывает
# уже заданные переменные), поэтому цели работают даже без agent/.env.
# Модель по умолчанию — победитель бенчмарка Дня 29 (равное качество, быстрее,
# стабильнее cold start). Переопределяется: make run-local MODEL=qwen2.5:3b
MODEL ?= qwen2.5:0.5b-opt
LOCAL_ENV := LLM_BASE_URL=http://localhost:11434/v1 LLM_MODEL=$(MODEL) LLM_API_KEY=ollama
# Число прогонов на вопрос в сравнении локальной и облачной моделей (стабильность).
RAG_RUNS ?= 3

# ===== Консольный интерфейс (REPL) =====
run:
	go run ./cmd/cli

# ===== Консольный интерфейс на ЛОКАЛЬНОЙ модели (без облака, День 27) =====
run-local:
	$(LOCAL_ENV) go run ./cmd/cli --config config.local.json

# ===== Сравнение токенов (Этап 3): короткий/длинный/переполненный диалог =====
run-stats:
	go run ./cmd/cli --stats

# ===== Сравнение сжатия (Этап 4): без сжатия vs со сжатием =====
run-compare:
	go run ./cmd/cli --compare

# ===== Сравнение стратегий контекста: window / facts / branch =====
run-compare-strategies:
	go run ./cmd/cli --compare-strategies

# ===== Сравнение влияния долговременной памяти (long vs без) =====
run-compare-memory:
	go run ./cmd/cli --compare-memory

# ===== Сравнение персонализации (профили terse vs detailed) =====
run-compare-profiles:
	go run ./cmd/cli --compare-profiles

# ===== Проверка инвариантов: конфликт запроса и правила → отказ =====
run-check-invariants:
	go run ./cmd/cli --check-invariants

# ===== MCP: подключиться к серверу и вывести список инструментов =====
# Запускает cmd/mcp-server как отдельный процесс (stdio), устанавливает
# соединение и печатает tools/list. Проверка: соединение + список инструментов.
run-mcp-tools:
	go run ./cmd/cli --mcp-tools

# ===== MCP: вызвать инструмент и напечатать результат =====
# Примеры:
#   make run-mcp-call TOOL=get_task  ARGS='{"task_id":"T-001"}'
#   make run-mcp-call TOOL=create_task ARGS='{"title":"Отчёт","priority":"high"}'
run-mcp-call:
	go run ./cmd/cli --mcp-call $(TOOL) --mcp-args '$(ARGS)'

# ===== MCP: демо планировщика (напоминание + периодический сбор + сводка) =====
run-mcp-demo:
	go run ./cmd/cli --mcp-demo

# ===== MCP: пайплайн search → summarize → save_to_file =====
# Пример: make run-mcp-pipeline QUERY=mcp
run-mcp-pipeline:
	go run ./cmd/cli --mcp-pipeline "$(QUERY)"

# ===== MCP: длинный флоу через несколько серверов (tasks/scheduler/knowledge) =====
# Пример: make run-mcp-orchestrate QUERY=mcp
run-mcp-orchestrate:
	go run ./cmd/cli --mcp-orchestrate "$(QUERY)"

# ===== RAG (День 22): первый RAG-запрос =====
# Требует индекс index_service (cd ../index_service && make index) и, для
# семантического движка, запущенный микросервис (make serve в index_service).
# Пример: make run-rag QUERY="как подключить пакет llm"
run-rag:
	go run ./cmd/cli --rag "$(QUERY)"

# ===== Локальный RAG (День 28): retrieval локальный + генерация на локальной модели =====
# Одиночный RAG-ответ локальной моделью. Пример:
#   make run-rag-local QUERY="как подключить пакет llm"
run-rag-local:
	$(LOCAL_ENV) go run ./cmd/cli --config config.local.json --rag "$(QUERY)"

# Сравнение RAG-ответов локальной и облачной моделей на контрольных вопросах.
# Отчёт: agent/results/rag_local_vs_cloud.md.
# Облачные настройки (секреты НЕ коммитятся) берутся из корневого ../.env и
# переэкспортируются под именами LLM_CLOUD_*; если ../.env нет — отчёт только
# по локальной модели. Локальную модель можно переопределить: MODEL=qwen2.5:3b.
run-rag-local-vs-cloud:
	@bash -c 'set -a; [ -f ../.env ] && . ../.env; set +a; \
	export LLM_CLOUD_BASE_URL="$$LLM_BASE_URL" LLM_CLOUD_API_KEY="$$LLM_API_KEY" LLM_CLOUD_MODEL="$$LLM_MODEL"; \
	$(LOCAL_ENV) go run ./cmd/cli --config config.local.json --rag-local-vs-cloud --rag-runs $(RAG_RUNS)'

# ===== Оптимизация локальной LLM (День 29) =====
# Создать оптимизированные варианты модели (параметры запечены в Modelfile):
#   num_ctx 8192 (без усечения RAG-контекста), temperature 0.2, top_p 0.8.
# Для q8-варианта предварительно: ollama pull qwen2.5:0.5b-instruct-q8_0
opt-create:
	ollama create qwen2.5:0.5b-opt -f ollama/qwen2.5-0.5b-opt.Modelfile
	ollama create qwen2.5:0.5b-q8-opt -f ollama/qwen2.5-0.5b-q8-opt.Modelfile

# Бенчмарк «до/после»: baseline / параметры / +промпт / квант q8.
# Отчёт: agent/results/rag_optimization.md. Прогоны: RAG_RUNS=2.
run-rag-optimize:
	$(LOCAL_ENV) go run ./cmd/cli --config config.local.json --rag-optimize --rag-runs $(RAG_RUNS)

# Сравнение качества: без RAG vs с RAG на 10 контрольных вопросах.
# Отчёт: agent/results/rag_comparison.md. Опционально добавить LLM-as-judge:
#   make run-rag-eval JUDGE=1
run-rag-eval:
	go run ./cmd/cli --rag-eval $(if $(JUDGE),--judge,)

# Сравнение режимов пайплайна: base / filter / full (этап 2 + query rewrite).
run-rag-modes:
	go run ./cmd/cli --rag-modes

# Проверка обязательных источников/цитат + режим «не знаю» (День 24).
# Отчёт: agent/results/rag_citations.md. Опционально LLM-as-judge:
#   make run-rag-citations JUDGE=1
run-rag-citations:
	go run ./cmd/cli --rag-citations $(if $(JUDGE),--judge,)

# ===== Мини-чат с RAG + памятью задачи (День 25) =====
# REPL: каждое сообщение → SayChat (ретрив контекста + история + память задачи
# + строгий формат с источниками). Команды: /goal, /facts, /reset, /exit.
run-chat:
	go run ./cmd/cli --chat

# Прогон 2 длинных сценариев (10-15 реплик) с автопроверкой: источники,
# формат, факты, «цель не потеряна». Отчёт: results/chat_scenarios.md.
run-chat-scenarios:
	go run ./cmd/cli --chat-scenarios

# ===== Сборка автономного MCP-сервера (отдельный процесс) в bin/ =====
build-mcp-server:
	mkdir -p bin
	go build -o bin/mcp-server ./cmd/mcp-server

# ===== Веб-интерфейс (опционально) =====
run-web:
	go run ./cmd/web

# ===== Веб-интерфейс на ЛОКАЛЬНОЙ модели (без облака, День 27) =====
run-web-local:
	$(LOCAL_ENV) go run ./cmd/web --config config.local.json

# ===== Приватный сервис (День 30): web-агент на всех интерфейсах =====
# Слушает 0.0.0.0:8080 — доступен с телефона/других устройств в Wi-Fi-сети:
#   http://<LAN-IP-Mac>:8080   (IP: ipconfig getifaddr en0)
# Требуется Ollama на внешнем интерфейсе: OLLAMA_HOST=0.0.0.0 ollama serve.
# ⚠️ Без аутентификации — только для доверенной домашней сети!
run-web-service:
	$(LOCAL_ENV) go run ./cmd/web --config config.service.json

# ===== Постоянная работа сервиса (LaunchAgents, экономия ресурсов) =====
# Устанавливает Ollama и web-агент как фоновые сервисы macOS:
#   - автозапуск при входе в систему, авто-перезапуск при падении;
#   - работают без открытого терминала;
#   - OLLAMA_KEEP_ALIVE=3m — модель выгружается из RAM при простое
#     (~400 МБ освобождается) и загружается при первом запросе (~1–2 с);
#   - бинарник bin/agent-web вместо go run — не держит тулчейн Go.
service-install: build-web
	@pkill -f "ollama serve" 2>/dev/null || true
	@pkill -x agent-web 2>/dev/null || true
	@pkill -x web 2>/dev/null || true
	@sleep 1
	@launchctl unload ~/Library/LaunchAgents/aichallenge.ollama.plist 2>/dev/null || true
	@launchctl unload ~/Library/LaunchAgents/aichallenge.agent-web.plist 2>/dev/null || true
	@mkdir -p ~/Library/LaunchAgents
	sed "s|{{REPO}}|$$(cd .. && pwd)|g" launchd/aichallenge.ollama.plist > ~/Library/LaunchAgents/aichallenge.ollama.plist
	sed "s|{{REPO}}|$$(cd .. && pwd)|g" launchd/aichallenge.agent-web.plist.tpl > ~/Library/LaunchAgents/aichallenge.agent-web.plist
	launchctl load ~/Library/LaunchAgents/aichallenge.ollama.plist
	launchctl load ~/Library/LaunchAgents/aichallenge.agent-web.plist
	@sleep 3
	@curl -s -m 5 http://127.0.0.1:11434/api/tags -o /dev/null -w "ollama: HTTP %{http_code}\n"
	@curl -s -m 5 http://127.0.0.1:8080/ -o /dev/null -w "web-агент: HTTP %{http_code}\n"
	@echo "Готово. С телефона: http://$$(ipconfig getifaddr en0):8080"

# Остановить фоновые сервисы (модель выгружается из памяти).
service-stop:
	launchctl unload ~/Library/LaunchAgents/aichallenge.ollama.plist 2>/dev/null || true
	launchctl unload ~/Library/LaunchAgents/aichallenge.agent-web.plist 2>/dev/null || true
	@echo "Сервисы остановлены. Запустить снова: make service-install"

# ===== Минимальный вариант (без LaunchAgents, ничего не меняет в системе) =====
# Фоновый запуск через nohup: переживает закрытие терминала, но НЕ перезагрузку.
# Предполагается, что Ollama уже работает (приложение в меню-баре или вручную).
# Если нужно открыть Ollama в сеть вручную (тоже фоном):
#   OLLAMA_HOST=0.0.0.0 nohup ollama serve >/tmp/ollama.log 2>&1 &
service-detach: build-web
	@nohup bin/agent-web --config config.service.json >/tmp/aichallenge-agent-web.log 2>&1 &
	@sleep 3
	@curl -s -m 5 http://127.0.0.1:8080/ -o /dev/null -w "web-агент: HTTP %{http_code}\n"
	@echo "Адрес с телефона: http://$$(ipconfig getifaddr en0):8080 | лог: /tmp/aichallenge-agent-web.log"

# Остановить фоновый web-агент (Ollama не трогается — ею управляет приложение).
service-detach-stop:
	@pkill -x agent-web 2>/dev/null || true
	@echo "Фоновый web-агент остановлен."

build-web:
	mkdir -p bin
	go build -o bin/agent-web ./cmd/web

# Остановить ранее запущенный web-сервер (по порту из config.json: web_addr).
# `go run` компилирует бинарник во временную папку, поэтому гасим процесс,
# слушающий порт (по умолчанию 8080).
WEB_PORT ?= 8080
stop-web:
	@pid=$$(lsof -ti tcp:$(WEB_PORT) 2>/dev/null); \
	if [ -n "$$pid" ]; then \
		kill $$pid && echo "web остановлен (pid $$pid, порт $(WEB_PORT))"; \
	else \
		echo "web на порту $(WEB_PORT) не запущен"; \
	fi

# ===== Сборка бинарников в agent/bin/ =====
build:
	mkdir -p bin
	go build -o bin/agent_cli ./cmd/cli
	go build -o bin/agent_web ./cmd/web
	go build -o bin/mcp-server ./cmd/mcp-server

# ===== Тесты =====
test:
	go test ./...

# ===== Сброс истории и рабочих БД (удаление файлов SQLite) =====
reset-history:
	rm -f agent_history.db
	rm -f agent_invariants.db

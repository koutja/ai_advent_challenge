package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// Reporter отображает по одной строке на каждый способ решения и обновляет их
// НА МЕСТЕ (без печати новых блоков): крутящийся спиннер, текущий шаг и
// увеличивающийся счётчик времени для каждого активного метода.
//
// Детальный вывод каждого метода пишется в отдельный лог-файл logs/<метод>.log.
// В одиночном режиме (stdout=true) детали копятся в буфере и печатаются в stdout
// только после завершения метода — чтобы не смешиваться со статусной строкой.
type Reporter struct {
	mu          sync.Mutex
	outMu       sync.Mutex // сериализация перерисовки в терминал
	methods     []string
	steps       map[string]string    // метод -> текущий шаг
	state       map[string]string    // pending | run | done | error
	detail      map[string]string    // краткое резюме по завершении
	started     map[string]time.Time // время старта метода
	buf         map[string]string    // буфер вывода для одиночного режима
	logDir      string
	stdout      bool // детали выводить в stdout, а не в лог-файл
	interactive bool
	repStart    time.Time
	drawn       bool
	rows        int
	tickStop    chan struct{}
}

const frameInterval = 120 * time.Millisecond

var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// newReporter создаёт репортёр для параллельного запуска (run-all):
// детали пишутся в logs/<метод>.log.
func newReporter(methods []string) *Reporter {
	r := &Reporter{
		methods:     methods,
		steps:       map[string]string{},
		state:       map[string]string{},
		detail:      map[string]string{},
		started:     map[string]time.Time{},
		buf:         map[string]string{},
		logDir:      "logs",
		repStart:    time.Now(),
		interactive: term.IsTerminal(int(os.Stdout.Fd())),
	}
	for _, m := range methods {
		r.state[m] = "pending"
	}
	r.hideCursor()
	return r
}

// newReporterStdout — репортёр одиночной подкоманды: спиннер + детали в stdout.
func newReporterStdout(method string) *Reporter {
	r := newReporter([]string{method})
	r.stdout = true
	return r
}

func (r *Reporter) hideCursor() {
	if r.interactive {
		fmt.Print("\033[?25l")
	}
}

// begin отмечает начало работы метода.
func (r *Reporter) begin(method, step string) {
	r.set(method, "run", step, "")
}

// step обновляет текущий шаг метода.
func (r *Reporter) step(method, step string) {
	r.set(method, "run", step, "")
}

// done завершает метод: меняет статус и печатает финальную строку/буфер.
func (r *Reporter) done(method, summary string, ok bool) {
	st := "done"
	if !ok {
		st = "error"
	}
	r.set(method, st, "", summary)
	r.flush(method)
}

func (r *Reporter) set(method, state, step, detail string) {
	r.mu.Lock()
	r.state[method] = state
	if step != "" {
		r.steps[method] = step
	}
	if state == "run" {
		if _, ok := r.started[method]; !ok {
			r.started[method] = time.Now()
		}
	}
	if detail != "" {
		r.detail[method] = detail
	}
	r.mu.Unlock()
	r.render()
}

// finish останавливает тикер и возвращает курсор.
func (r *Reporter) finish() {
	r.stopTicker()
	if r.interactive {
		fmt.Print("\033[?25h\n")
	}
}

// startTicker запускает фоновую перерисовку (для анимации спиннера и таймеров).
func (r *Reporter) startTicker() {
	if !r.interactive {
		return
	}
	r.tickStop = make(chan struct{})
	go func() {
		t := time.NewTicker(frameInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				r.render()
			case <-r.tickStop:
				return
			}
		}
	}()
}

func (r *Reporter) stopTicker() {
	if r.tickStop == nil {
		return
	}
	close(r.tickStop)
	r.tickStop = nil
}

// output направляет текст в stdout-буфер (одиночный режим) либо в лог-файл (run-all).
func (r *Reporter) output(method, text string) {
	if r == nil {
		fmt.Print(text)
		return
	}
	if r.stdout {
		r.mu.Lock()
		r.buf[method] += text
		r.mu.Unlock()
		return
	}
	r.appendLog(method, text)
}

func (r *Reporter) printf(method, format string, args ...any) {
	r.output(method, fmt.Sprintf(format, args...))
}

// flush печатает накопленный буфер метода (одиночный режим) после завершения.
func (r *Reporter) flush(method string) {
	if !r.stdout {
		return
	}
	r.mu.Lock()
	text := r.buf[method]
	r.buf[method] = ""
	r.mu.Unlock()
	if text != "" {
		fmt.Print(text)
	}
}

func (r *Reporter) appendLog(method, text string) {
	if text == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = os.MkdirAll(r.logDir, 0o755)
	f, err := os.OpenFile(filepath.Join(r.logDir, method+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(text)
}

// render перерисовывает панель НА МЕСТЕ: поднимается вверх и перезаписывает
// те же строки (никаких новых блоков/сообщений).
func (r *Reporter) render() {
	r.outMu.Lock()
	defer r.outMu.Unlock()

	r.mu.Lock()
	rows := r.buildRowsLocked()
	r.mu.Unlock()

	out := new(strings.Builder)
	if r.drawn && len(rows) > 0 {
		out.WriteString(fmt.Sprintf("\033[%dA", r.rows))
	}
	for _, row := range rows {
		out.WriteString(row + "\n")
	}
	r.drawn = true
	r.rows = len(rows)
	fmt.Print(out.String())
}

func (r *Reporter) buildRowsLocked() []string {
	now := time.Now().UnixMilli()
	rows := []string{"=== day_03: запуск стратегий (параллельно) ==="}
	for _, m := range r.methods {
		elapsed := int(time.Since(r.started[m]).Seconds())
		switch r.state[m] {
		case "run":
			frame := string(spinnerFrames[(now/int64(frameInterval))%int64(len(spinnerFrames))])
			rows = append(rows, fmt.Sprintf("● %-11s %s %ds %s", m, frame, elapsed, r.steps[m]))
		case "done":
			rows = append(rows, fmt.Sprintf("✓ %-11s %s (%ds)", m, r.detail[m], elapsed))
		case "error":
			rows = append(rows, fmt.Sprintf("✗ %-11s %s (%ds)", m, r.detail[m], elapsed))
		default:
			rows = append(rows, fmt.Sprintf("○ %-11s ожидание...", m))
		}
	}
	rows = append(rows, fmt.Sprintf("прошло: %s", time.Since(r.repStart).Round(time.Second)))
	return rows
}

// resetLogs очищает лог-файлы в каталоге перед новым коллективным прогоном.
func resetLogs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

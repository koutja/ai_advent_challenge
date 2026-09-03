package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/briandowns/spinner"
	"golang.org/x/term"
)

// Reporter показывает живой прогресс в терминале: крутящийся спиннер,
// имя активного метода, текущий шаг и увеличивающийся каждую секунду счётчик времени.
// Детальный вывод каждого метода при этом пишется в отдельный лог-файл logs/<метод>.log.
//
// Если Reporter == nil (одиночный запуск подкоманды) — методы output/printf просто
// печатают в stdout, а визуальные вызовы begin/step/done — no-op.
type Reporter struct {
	mu          sync.Mutex
	methods     []string
	current     string    // активный метод
	stepText    string    // текущий шаг активного метода
	started     time.Time // момент начала активного метода
	logDir      string
	interactive bool // stdout — терминал (TTY)
	stdout      bool // детали выводить в stdout, а не в лог-файл
	sp          *spinner.Spinner
	tickDone    chan struct{}
}

// newReporter создаёт репортёр для коллективного запуска (run-all):
// детали пишутся в logs/<метод>.log.
func newReporter(methods []string) *Reporter {
	r := &Reporter{
		methods:     methods,
		current:     "",
		logDir:      "logs",
		interactive: term.IsTerminal(int(os.Stdout.Fd())),
	}
	r.hideCursor()
	return r
}

// newReporterStdout создаёт репортёр для одиночного подкоманды:
// детали печатаются в stdout (виден ответ), но спиннер и счётчик времени тоже работают.
func newReporterStdout() *Reporter {
	r := &Reporter{
		current:     "",
		logDir:      "logs",
		interactive: term.IsTerminal(int(os.Stdout.Fd())),
		stdout:      true,
	}
	r.hideCursor()
	return r
}

func (r *Reporter) hideCursor() {
	if r.interactive {
		fmt.Print("\033[?25l")
	}
}

// begin запускает спиннер для нового метода.
func (r *Reporter) begin(method, step string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.current = method
	r.setStepLocked(step)
	r.mu.Unlock()
	r.startSpin()
}

// step обновляет текст текущего шага (без перезапуска спиннера).
func (r *Reporter) step(method, step string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.current != method {
		r.mu.Unlock()
		return
	}
	r.setStepLocked(step)
	r.mu.Unlock()
	r.updateSuffix()
}

// done завершает метод: спиннер останавливается и печатается финальная строка.
func (r *Reporter) done(method, summary string, ok bool) {
	if r == nil {
		return
	}
	mark := "✓"
	if !ok {
		mark = "✗"
	}
	elapsed := int(time.Since(r.started).Seconds())

	r.mu.Lock()
	if r.sp != nil {
		if r.tickDone != nil {
			close(r.tickDone)
			r.tickDone = nil
		}
		r.sp.FinalMSG = fmt.Sprintf("%s [%s] %s (%ds)\n", mark, method, summary, elapsed)
		r.sp.Stop()
		r.sp = nil
	} else if !r.interactive {
		// Не-терминальный режим: просто печатаем строку.
		fmt.Printf("%s [%s] %s (%ds)\n", mark, method, summary, elapsed)
	}
	r.current = ""
	r.mu.Unlock()
}

// finish вызывает финальную перерисовку и возвращает курсор.
func (r *Reporter) finish() {
	if r == nil {
		return
	}
	if r.interactive {
		fmt.Print("\033[?25h") // показать курсор
	}
}

func (r *Reporter) setStepLocked(step string) {
	r.stepText = step
	r.started = time.Now()
}

func (r *Reporter) spinSuffix() string {
	el := int(time.Since(r.started).Seconds())
	return fmt.Sprintf(" %ds • %s", el, r.stepText)
}

// startSpin создаёт спиннер и запускает его вместе с тикером счётчика времени.
func (r *Reporter) startSpin() {
	if !r.interactive {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sp != nil {
		return
	}
	s := spinner.New(spinner.CharSets[11], 100*time.Millisecond)
	s.Prefix = "[" + r.current + "] "
	s.Suffix = r.spinSuffix()
	r.sp = s
	r.tickDone = make(chan struct{})
	go r.tickElapsed()
	s.Start()
}

// tickElapsed раз в секунду обновляет счётчик времени в суффиксе спиннера.
func (r *Reporter) tickElapsed() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			r.updateSuffix()
		case <-r.tickDone:
			return
		}
	}
}

func (r *Reporter) updateSuffix() {
	if r == nil {
		return
	}
	r.mu.Lock()
	s := r.sp
	var sfx string
	if s != nil {
		sfx = r.spinSuffix()
	}
	r.mu.Unlock()
	if s != nil {
		s.Suffix = sfx
	}
}

// output направляет текст в stdout (одиночный режим) либо в лог-файл метода (run-all).
func (r *Reporter) output(method, text string) {
	if r == nil || r.stdout {
		fmt.Print(text)
		return
	}
	r.appendLog(method, text)
}

func (r *Reporter) printf(method, format string, args ...any) {
	if r == nil || r.stdout {
		fmt.Printf(format, args...)
		return
	}
	r.appendLog(method, fmt.Sprintf(format, args...))
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

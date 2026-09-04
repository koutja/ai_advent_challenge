package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// logger пишет построчный лог с таймстампами в файл (logs/day05.log).
// Каждый прогон дописывается в конец, чтобы можно было смотреть историю.
type logger struct {
	f *os.File
}

func newLogger(path string) (*logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &logger{f: f}, nil
}

// logf дописывает одну строку с текущим временем.
func (l *logger) logf(format string, args ...any) {
	if l == nil || l.f == nil {
		return
	}
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	_, _ = l.f.WriteString(line)
}

func (l *logger) close() {
	if l != nil && l.f != nil {
		_ = l.f.Close()
	}
}

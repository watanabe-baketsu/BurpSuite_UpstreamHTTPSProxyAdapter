package logging

import (
	"fmt"
	"sync"
	"time"
)

type Level string

const (
	LevelInfo  Level = "INFO"
	LevelWarn  Level = "WARN"
	LevelError Level = "ERROR"
	LevelDebug Level = "DEBUG"
)

type Entry struct {
	Time    string `json:"time"`
	Level   Level  `json:"level"`
	Message string `json:"message"`
}

type EventCallback func(entry Entry)

type Logger struct {
	mu       sync.Mutex
	entries  []Entry
	maxSize  int
	callback EventCallback
}

func New(maxSize int) *Logger {
	return &Logger{
		entries: make([]Entry, 0, maxSize),
		maxSize: maxSize,
	}
}

func (l *Logger) SetCallback(cb EventCallback) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.callback = cb
}

func (l *Logger) log(level Level, format string, args ...interface{}) {
	entry := Entry{
		Time:    time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
		Level:   level,
		Message: fmt.Sprintf(format, args...),
	}
	l.mu.Lock()
	if len(l.entries) >= l.maxSize {
		l.entries = l.entries[1:]
	}
	l.entries = append(l.entries, entry)
	cb := l.callback
	l.mu.Unlock()

	if cb != nil {
		cb(entry)
	}
}

func (l *Logger) Info(format string, args ...interface{})  { l.log(LevelInfo, format, args...) }
func (l *Logger) Warn(format string, args ...interface{})  { l.log(LevelWarn, format, args...) }
func (l *Logger) Error(format string, args ...interface{}) { l.log(LevelError, format, args...) }
func (l *Logger) Debug(format string, args ...interface{}) { l.log(LevelDebug, format, args...) }

func (l *Logger) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

func (l *Logger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = l.entries[:0]
}

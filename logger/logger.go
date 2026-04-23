package logger

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

type Entry struct {
	Level string                 `json:"level"`
	Time  string                 `json:"time"`
	Msg   string                 `json:"msg"`
	Extra map[string]interface{} `json:"extra,omitempty"`
}

type Logger struct {
	mu sync.Mutex
}

func New() *Logger {
	return &Logger{}
}

func (l *Logger) Info(msg string, extra map[string]interface{}) {
	l.log("info", msg, extra)
}

func (l *Logger) Warn(msg string, extra map[string]interface{}) {
	l.log("warn", msg, extra)
}

func (l *Logger) Error(msg string, extra map[string]interface{}) {
	l.log("error", msg, extra)
}

func (l *Logger) log(level, msg string, extra map[string]interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry := Entry{
		Level: level,
		Time:  time.Now().Format(time.RFC3339Nano),
		Msg:   msg,
		Extra: extra,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(entry)
}

// Package logx writes JSON log lines with ts, level and intLevel (log convention) and
// ISO 8601 UTC timestamps (date convention).
package logx

import (
	"encoding/json"
	"io"
	"sort"
	"sync"
	"time"
)

// Levels and their integer values per the log convention.
const (
	Fatal = "FATAL"
	Error = "ERROR"
	Warn  = "WARN"
	Info  = "INFO"
	Debug = "DEBUG"
	Trace = "TRACE"
)

var intLevel = map[string]int{Fatal: 6, Error: 5, Warn: 4, Info: 3, Debug: 2, Trace: 1}

// Fields are the event-specific keys of one log line.
type Fields map[string]any

// Logger writes one JSON object per line to every writer.
type Logger struct {
	mu  sync.Mutex
	out []io.Writer
	Now func() time.Time
}

func New(out ...io.Writer) *Logger {
	return &Logger{out: out, Now: time.Now}
}

// Log writes ts, level and intLevel first, then the fields in key order.
// Empty string values are dropped so optional keys stay absent.
func (l *Logger) Log(level string, f Fields) {
	if l == nil {
		return
	}
	buf := []byte(`{"ts":`)
	buf = appendJSON(buf, l.Now().UTC().Format(time.RFC3339Nano))
	buf = append(buf, `,"level":`...)
	buf = appendJSON(buf, level)
	buf = append(buf, `,"intLevel":`...)
	buf = appendJSON(buf, intLevel[level])
	keys := make([]string, 0, len(f))
	for k, v := range f {
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		if k == "ts" || k == "level" || k == "intLevel" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		buf = append(buf, ',')
		buf = appendJSON(buf, k)
		buf = append(buf, ':')
		buf = appendJSON(buf, f[k])
	}
	buf = append(buf, "}\n"...)
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, w := range l.out {
		_, _ = w.Write(buf)
	}
}

func (l *Logger) Info(f Fields)  { l.Log(Info, f) }
func (l *Logger) Warn(f Fields)  { l.Log(Warn, f) }
func (l *Logger) Error(f Fields) { l.Log(Error, f) }

func appendJSON(buf []byte, v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(err.Error())
	}
	return append(buf, b...)
}

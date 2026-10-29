package promote

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// logEntry is one line of $AIC_WORK/logs/promoter.jsonl. Field order puts
// ts, level and intLevel first (log convention); ts is ISO 8601 UTC (date convention).
type logEntry struct {
	Ts       string  `json:"ts"`
	Level    string  `json:"level"`
	IntLevel int     `json:"intLevel"`
	Event    string  `json:"event"`
	MR       string  `json:"mr"`
	TaskID   string  `json:"task_id"`
	Worktree string  `json:"worktree"`
	Outcome  string  `json:"outcome"`
	Pushed   string  `json:"pushed,omitempty"`
	Checks   []Check `json:"checks"`
}

// logRun appends one line per run. A refusal is a WARN security event.
func logRun(o Options, r Result) {
	e := logEntry{
		Ts: o.Now().UTC().Format(time.RFC3339Nano), Level: "INFO", IntLevel: 3, Event: "promote",
		MR: strconv.Itoa(o.MR), TaskID: o.TaskID, Worktree: o.Worktree, Outcome: "pushed", Pushed: r.Pushed, Checks: r.Checks,
	}
	if !r.OK {
		e.Level, e.IntLevel, e.Outcome = "WARN", 4, "refused"
	}
	b, err := json.Marshal(e)
	if err != nil || o.Work == "" {
		return
	}
	dir := filepath.Join(o.Work, "logs")
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "promoter.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

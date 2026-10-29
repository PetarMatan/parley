package logx

import (
	"bytes"
	"testing"
	"time"
)

func TestLineShape(t *testing.T) {
	var b bytes.Buffer
	l := New(&b)
	l.Now = func() time.Time { return time.Date(2026, 10, 28, 14, 30, 0, 0, time.FixedZone("x", 3600)) }
	l.Warn(Fields{"event": "sender_refused", "sender": "c@example.com", "task_id": "", "status": 403})
	want := `{"ts":"2026-10-28T13:30:00Z","level":"WARN","intLevel":4,"event":"sender_refused","sender":"c@example.com","status":403}` + "\n"
	if b.String() != want {
		t.Fatalf("got  %s\nwant %s", b.String(), want)
	}
}

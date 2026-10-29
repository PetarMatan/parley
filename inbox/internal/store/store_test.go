package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdatePersistsAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox", "store.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Update(func(d *Data) error {
		d.Sessions["s1"] = &Session{SessionID: "s1", User: "a@example.com"}
		d.Messages = append(d.Messages, &Message{Envelope: Envelope{MessageID: "m-1", State: Queued}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A failed update does not save.
	boom := errors.New("boom")
	if err := s.Update(func(d *Data) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	again.View(func(d *Data) {
		if d.Sessions["s1"] == nil || d.Message("m-1") == nil || d.Message("m-2") != nil {
			t.Fatalf("reload lost data: %+v", d)
		}
	})
}

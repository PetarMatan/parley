// Package store keeps sessions, contexts and messages in one JSON file. Every
// change rewrites the file atomically (temp file, fsync, rename).
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/PetarMatan/parley/inbox/internal/rules"
)

// Message states.
const (
	Queued = "queued"
	Pushed = "pushed"
	Acked  = "acked"
)

// Task states (A2A vocabulary).
const (
	TaskSubmitted     = "submitted"
	TaskWorking       = "working"
	TaskCompleted     = "completed"
	TaskInputRequired = "input-required"
	TaskRejected      = "rejected"
	TaskCanceled      = "canceled"
	TaskFailed        = "failed"
)

// Session is one registered Claude Code session.
type Session struct {
	SessionID    string    `json:"session_id"`
	User         string    `json:"user"`
	Host         string    `json:"host"`
	Repo         string    `json:"repo"`
	Branch       string    `json:"branch"`
	Cwd          string    `json:"cwd"`
	Quarantine   bool      `json:"quarantine"`
	RegisteredAt time.Time `json:"registered_at"`
	LastActive   time.Time `json:"last_active"`
}

// Task is one finding and its replies.
type Task struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	Round     int    `json:"round"`
	Class     string `json:"class"`
	Sender    string `json:"sender"`
	Addressee string `json:"addressee"`
	// Sessions maps a participant to the session that last sent or acked in
	// this task; reply routing prefers it.
	Sessions  map[string]string `json:"sessions,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// Context is one merge request conversation.
type Context struct {
	ContextID    string           `json:"context_id"`
	Repo         string           `json:"repo"`
	SourceBranch string           `json:"source_branch"`
	Participants []string         `json:"participants"`
	Tasks        map[string]*Task `json:"tasks"`
}

// Envelope is the wire shape of contract section 4. Nothing else goes in it.
type Envelope struct {
	MessageID string          `json:"message_id"`
	ContextID string          `json:"context_id"`
	TaskID    string          `json:"task_id"`
	Round     int             `json:"round"`
	From      string          `json:"from"`
	To        string          `json:"to"`
	SentAt    string          `json:"sent_at"`
	Payload   json.RawMessage `json:"payload"`
	Tags      rules.Tags      `json:"tags"`
	State     string          `json:"state"`
}

// Message is an envelope plus delivery bookkeeping that never goes on the wire.
type Message struct {
	Envelope Envelope `json:"envelope"`
	Repo     string   `json:"repo"`
	PushedTo string   `json:"pushed_to,omitempty"`
	AckedBy  string   `json:"acked_by,omitempty"`
}

// Data is the whole store. Messages are kept in arrival order.
type Data struct {
	Sessions map[string]*Session `json:"sessions"`
	Contexts map[string]*Context `json:"contexts"`
	Messages []*Message          `json:"messages"`
}

// Message returns the message with id, or nil.
func (d *Data) Message(id string) *Message {
	for _, m := range d.Messages {
		if m.Envelope.MessageID == id {
			return m
		}
	}
	return nil
}

// Store guards Data with a mutex and persists it after each Update.
type Store struct {
	mu   sync.Mutex
	path string
	data *Data
}

// Open loads path, or starts empty when it does not exist.
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: &Data{}}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, s.data); err != nil {
			return nil, err
		}
	}
	if s.data.Sessions == nil {
		s.data.Sessions = map[string]*Session{}
	}
	if s.data.Contexts == nil {
		s.data.Contexts = map[string]*Context{}
	}
	return s, nil
}

// View runs fn under the lock without saving. fn must not keep references.
func (s *Store) View(fn func(*Data)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.data)
}

// Update runs fn under the lock and saves when fn returns nil. On a save
// error the in-memory change stays; the next successful save persists it.
func (s *Store) Update(fn func(*Data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(s.data); err != nil {
		return err
	}
	return s.save()
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.data, "", " ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".store-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

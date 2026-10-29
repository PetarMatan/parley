package promote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Inbox is what the promoter needs from the inbox service. Tests inject a fake.
type Inbox interface {
	// TaskClass returns the finding class (fact, taste, product) of a task.
	TaskClass(ctx context.Context, contextID, taskID string) (string, error)
	// PostNote posts a note.v1 body to the merge request of the context.
	PostNote(ctx context.Context, contextID, body string) error
}

// HTTPInbox talks to the inbox with the promoter's own token.
type HTTPInbox struct {
	BaseURL   string
	TokenFile string
	Client    *http.Client
}

func NewHTTPInbox(baseURL, tokenFile string) *HTTPInbox {
	return &HTTPInbox{BaseURL: strings.TrimRight(baseURL, "/"), TokenFile: tokenFile, Client: &http.Client{Timeout: 10 * time.Second}}
}

func (h *HTTPInbox) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	tok, err := os.ReadFile(h.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("promoter token: %w", err)
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.BaseURL+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("inbox unreachable: %w", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("inbox %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func contextPath(contextID string) string {
	return "/v1/contexts/" + url.PathEscape(contextID)
}

func (h *HTTPInbox) TaskClass(ctx context.Context, contextID, taskID string) (string, error) {
	b, err := h.do(ctx, http.MethodGet, contextPath(contextID), nil)
	if err != nil {
		return "", err
	}
	return classFromContext(b, taskID)
}

func (h *HTTPInbox) PostNote(ctx context.Context, contextID, body string) error {
	_, err := h.do(ctx, http.MethodPost, contextPath(contextID)+"/notes",
		map[string]string{"schema": "note.v1", "context_id": contextID, "body": body})
	return err
}

// classFromContext finds the task's class in a GET /v1/contexts response.
// Contract section 6 does not fix the conversation shape, so it accepts the
// class on the task entry (tasks as a list or a map) or on the task's
// finding.v1 payload in messages.
func classFromContext(b []byte, taskID string) (string, error) {
	var conv struct {
		Tasks    json.RawMessage `json:"tasks"`
		Messages []struct {
			TaskID  string `json:"task_id"`
			Payload struct {
				Schema string `json:"schema"`
				TaskID string `json:"task_id"`
				Class  string `json:"class"`
			} `json:"payload"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(b, &conv); err != nil {
		return "", fmt.Errorf("context response: %w", err)
	}
	type task struct {
		TaskID  string `json:"task_id"`
		ID      string `json:"id"`
		Class   string `json:"class"`
		Finding *struct {
			Class string `json:"class"`
		} `json:"finding"`
	}
	pick := func(t task) string {
		if t.Class != "" {
			return t.Class
		}
		if t.Finding != nil {
			return t.Finding.Class
		}
		return ""
	}
	var list []task
	if json.Unmarshal(conv.Tasks, &list) == nil {
		for _, t := range list {
			if (t.TaskID == taskID || t.ID == taskID) && pick(t) != "" {
				return pick(t), nil
			}
		}
	}
	var byID map[string]task
	if json.Unmarshal(conv.Tasks, &byID) == nil {
		if t, ok := byID[taskID]; ok && pick(t) != "" {
			return pick(t), nil
		}
	}
	for _, m := range conv.Messages {
		if m.Payload.Schema == "finding.v1" && (m.TaskID == taskID || m.Payload.TaskID == taskID) && m.Payload.Class != "" {
			return m.Payload.Class, nil
		}
	}
	return "", fmt.Errorf("task %s has no class in the context", taskID)
}

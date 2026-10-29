// Package mr reads merge requests. Local is the file-based fake of contract
// section 7; a GitLab provider would implement the same interface.
package mr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MR is the fake merge request file.
type MR struct {
	IID          int      `json:"iid"`
	Repo         string   `json:"repo"`
	Title        string   `json:"title"`
	SourceBranch string   `json:"source_branch"`
	TargetBranch string   `json:"target_branch"`
	Author       string   `json:"author"`
	Reviewers    []string `json:"reviewers"`
	Assignees    []string `json:"assignees"`
}

// CanSendFinding is the sender gate for findings: reviewer or assignee.
func (m *MR) CanSendFinding(user string) bool {
	return slices.Contains(m.Reviewers, user) || slices.Contains(m.Assignees, user)
}

// IsMember reports whether user is the author, a reviewer or an assignee.
func (m *MR) IsMember(user string) bool {
	return m.Author == user || m.CanSendFinding(user)
}

// ErrNotFound means no merge request with that iid exists in that repo.
var ErrNotFound = errors.New("merge request not found")

// Provider is what the inbox needs from a merge request system.
type Provider interface {
	Get(repo string, iid int) (*MR, error)
	DiffFiles(m *MR) ([]string, error)
	PostNote(m *MR, sender, body string) error
}

// Local reads $AIC_WORK/mr/<iid>.json and diffs a bare repository.
type Local struct {
	Work string
	// Origin is the default bare repository, $AIC_WORK/origin.git.
	Origin string
	Now    func() time.Time

	mu sync.Mutex // serialises note appends
}

func NewLocal(work string) *Local {
	return &Local{Work: work, Origin: filepath.Join(work, "origin.git"), Now: time.Now}
}

// Get loads the file and checks that it belongs to repo.
func (l *Local) Get(repo string, iid int) (*MR, error) {
	data, err := os.ReadFile(l.file(iid, ".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var m MR
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("mr %d: %w", iid, err)
	}
	if m.Repo != repo || m.IID != iid {
		return nil, ErrNotFound
	}
	norm := func(xs []string) []string {
		for i, x := range xs {
			xs[i] = strings.ToLower(x)
		}
		return xs
	}
	m.Author = strings.ToLower(m.Author)
	m.Reviewers, m.Assignees = norm(m.Reviewers), norm(m.Assignees)
	return &m, nil
}

// GitDir picks $AIC_WORK/<repo basename>.git when it exists (setup.sh links
// shop.git to origin.git), else the default origin. This lets a second repo
// exist next to the first for the two-repository check.
func (l *Local) GitDir(repo string) string {
	cand := filepath.Join(l.Work, path.Base(repo)+".git")
	if st, err := os.Stat(cand); err == nil && st.IsDir() {
		return cand
	}
	return l.Origin
}

// DiffFiles runs git diff --name-only target...source, per request.
func (l *Local) DiffFiles(m *MR) ([]string, error) {
	if strings.HasPrefix(m.TargetBranch, "-") || strings.HasPrefix(m.SourceBranch, "-") {
		return nil, fmt.Errorf("mr %d: bad branch name", m.IID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "--git-dir", l.GitDir(m.Repo), "diff", "--name-only", "--no-renames",
		m.TargetBranch+"..."+m.SourceBranch, "--")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	var files []string
	for _, f := range strings.Split(string(out), "\n") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// PostNote appends a note with sender and time to $AIC_WORK/mr/<iid>.notes.md.
func (l *Local) PostNote(m *MR, sender, body string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.file(m.IID, ".notes.md"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	ts := l.Now().UTC().Format(time.RFC3339)
	_, err = fmt.Fprintf(f, "\n---\n\n**%s** · %s\n\n%s\n", sender, ts, quoteBody(body))
	return err
}

// quoteBody renders the body as a markdown blockquote. Only the separator and
// sender line the inbox writes are unquoted, so a body cannot imitate a note
// from another sender (the promoter, aic-inbox).
func quoteBody(body string) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("> "+l, " ")
	}
	return strings.Join(lines, "\n")
}

func (l *Local) file(iid int, ext string) string {
	return filepath.Join(l.Work, "mr", strconv.Itoa(iid)+ext)
}

package mr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitLab reads merge requests through the GitLab REST API (v4). The repo part
// of a context id is the project path, e.g. "parley-lab/orders-service".
// Identities are GitLab usernames: the token's identity claim must be the
// username (preferred_username in the testbed realm), because the API shows
// participants by username and hides emails from non-admin tokens.
//
// Token: a group or project access token. Reading needs read_api; PostNote
// writes a note and needs the api scope.
type GitLab struct {
	BaseURL string // https://gitlab.example.com, no trailing slash
	Token   string
	Client  *http.Client
	Now     func() time.Time
	TTL     time.Duration // cache lifetime per merge request, default 30 s

	mu    sync.Mutex
	cache map[string]*gitlabEntry
}

type gitlabEntry struct {
	mr    *MR
	files []string // nil until DiffFiles ran
	at    time.Time
}

// NewGitLab builds a provider. A nil client gets a 10 s timeout.
func NewGitLab(baseURL, token string, client *http.Client) *GitLab {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &GitLab{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		Client:  client,
		Now:     time.Now,
		TTL:     30 * time.Second,
		cache:   map[string]*gitlabEntry{},
	}
}

func cacheKey(repo string, iid int) string { return repo + "!" + strconv.Itoa(iid) }

func (g *GitLab) mrPath(repo string, iid int) string {
	return "/api/v4/projects/" + url.PathEscape(repo) + "/merge_requests/" + strconv.Itoa(iid)
}

// Get fetches the merge request, or serves it from the cache within TTL.
func (g *GitLab) Get(repo string, iid int) (*MR, error) {
	key := cacheKey(repo, iid)
	g.mu.Lock()
	if e, ok := g.cache[key]; ok && g.Now().Sub(e.at) < g.TTL {
		g.mu.Unlock()
		return e.mr, nil
	}
	g.mu.Unlock()

	var raw struct {
		IID          int    `json:"iid"`
		Title        string `json:"title"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		Author       struct {
			Username string `json:"username"`
		} `json:"author"`
		Reviewers []struct {
			Username string `json:"username"`
		} `json:"reviewers"`
		Assignees []struct {
			Username string `json:"username"`
		} `json:"assignees"`
	}
	if _, err := g.do(http.MethodGet, g.mrPath(repo, iid), nil, &raw); err != nil {
		return nil, err
	}
	if raw.IID != iid {
		return nil, ErrNotFound
	}
	m := &MR{
		IID:          raw.IID,
		Repo:         repo,
		Title:        raw.Title,
		SourceBranch: raw.SourceBranch,
		TargetBranch: raw.TargetBranch,
		Author:       strings.ToLower(raw.Author.Username),
	}
	for _, r := range raw.Reviewers {
		m.Reviewers = append(m.Reviewers, strings.ToLower(r.Username))
	}
	for _, a := range raw.Assignees {
		m.Assignees = append(m.Assignees, strings.ToLower(a.Username))
	}
	g.mu.Lock()
	g.cache[key] = &gitlabEntry{mr: m, at: g.Now()}
	g.mu.Unlock()
	return m, nil
}

// DiffFiles lists every old and new path of the merge request's diffs,
// following pagination. The list is cached with the merge request.
func (g *GitLab) DiffFiles(m *MR) ([]string, error) {
	key := cacheKey(m.Repo, m.IID)
	g.mu.Lock()
	if e, ok := g.cache[key]; ok && e.files != nil && g.Now().Sub(e.at) < g.TTL {
		g.mu.Unlock()
		return e.files, nil
	}
	g.mu.Unlock()

	files := []string{}
	seen := map[string]bool{}
	for page := 1; page > 0; {
		var items []struct {
			OldPath string `json:"old_path"`
			NewPath string `json:"new_path"`
		}
		p := g.mrPath(m.Repo, m.IID) + "/diffs?per_page=100&page=" + strconv.Itoa(page)
		h, err := g.do(http.MethodGet, p, nil, &items)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			for _, f := range []string{it.OldPath, it.NewPath} {
				if f != "" && !seen[f] {
					seen[f] = true
					files = append(files, f)
				}
			}
		}
		next, _ := strconv.Atoi(h.Get("x-next-page"))
		page = next
	}
	g.mu.Lock()
	if e, ok := g.cache[key]; ok {
		e.files = files
	} else {
		g.cache[key] = &gitlabEntry{mr: m, files: files, at: g.Now()}
	}
	g.mu.Unlock()
	return files, nil
}

// PostNote adds a merge request note. The token is a bot, so the note body
// names the sender; the body itself is quoted as in Local.
func (g *GitLab) PostNote(m *MR, sender, body string) error {
	ts := g.Now().UTC().Format(time.RFC3339)
	note := fmt.Sprintf("**%s** · %s\n\n%s\n", sender, ts, quoteBody(body))
	payload, _ := json.Marshal(map[string]string{"body": note})
	if _, err := g.do(http.MethodPost, g.mrPath(m.Repo, m.IID)+"/notes", payload, nil); err != nil {
		return err
	}
	g.mu.Lock()
	delete(g.cache, cacheKey(m.Repo, m.IID))
	g.mu.Unlock()
	return nil
}

// do runs one API call. 404 is ErrNotFound; 401 and 403 name the token.
func (g *GitLab) do(method, path string, body []byte, out any) (http.Header, error) {
	req, err := http.NewRequest(method, g.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", g.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := g.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitlab: %s %s: %w", method, path, err)
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("gitlab: %s %s: status %d, check AIC_GITLAB_TOKEN and its scope", method, path, res.StatusCode)
	case res.StatusCode < 200 || res.StatusCode > 299:
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return nil, fmt.Errorf("gitlab: %s %s: status %d: %s", method, path, res.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			return nil, fmt.Errorf("gitlab: %s %s: bad JSON: %w", method, path, err)
		}
	}
	return res.Header, nil
}

var _ Provider = (*GitLab)(nil)

// ErrGitLabConfig is returned by the inbox at startup when the gitlab provider
// is chosen without its settings.
var ErrGitLabConfig = errors.New("AIC_MR_PROVIDER=gitlab needs AIC_GITLAB_URL and AIC_GITLAB_TOKEN")

package mr

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGitLab answers the three endpoints the provider uses for project
// "parley-lab/orders-service", merge request 7.
func fakeGitLab(t *testing.T) (*httptest.Server, *int64, *[]string) {
	t.Helper()
	var hits int64
	notes := []string{}
	const base = "/api/v4/projects/parley-lab%2Forders-service/merge_requests/7"
	h := http.NewServeMux()
	h.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		if r.Header.Get("PRIVATE-TOKEN") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		p := r.URL.EscapedPath()
		switch {
		case p == base && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"iid":7,"title":"Discount rounding","source_branch":"fix/rounding","target_branch":"main",
			  "author":{"username":"Bob"},"reviewers":[{"username":"alice"},{"username":"carol"}],"assignees":[{"username":"bob"}],"state":"opened"}`))
		case p == base+"/diffs" && r.Method == http.MethodGet:
			switch r.URL.Query().Get("page") {
			case "", "1":
				w.Header().Set("x-next-page", "2")
				_, _ = w.Write([]byte(`[{"old_path":"internal/price.go","new_path":"internal/price.go"},{"old_path":"old.go","new_path":"new.go"}]`))
			case "2":
				w.Header().Set("x-next-page", "")
				_, _ = w.Write([]byte(`[{"old_path":"internal/price_test.go","new_path":"internal/price_test.go"},{"old_path":"new.go","new_path":"new.go"}]`))
			}
		case p == base+"/notes" && r.Method == http.MethodPost:
			var in struct{ Body string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			notes = append(notes, in.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1}`))
		case strings.Contains(p, "merge_requests/403"):
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"404 Not found"}`))
		}
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, &hits, &notes
}

func newTestGitLab(srv *httptest.Server, now *time.Time) *GitLab {
	g := NewGitLab(srv.URL+"/", "tok", srv.Client())
	g.Now = func() time.Time { return *now }
	return g
}

func TestGitLabGet(t *testing.T) {
	srv, _, _ := fakeGitLab(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	g := newTestGitLab(srv, &now)
	m, err := g.Get("parley-lab/orders-service", 7)
	if err != nil {
		t.Fatal(err)
	}
	want := MR{IID: 7, Repo: "parley-lab/orders-service", Title: "Discount rounding", SourceBranch: "fix/rounding", TargetBranch: "main",
		Author: "bob", Reviewers: []string{"alice", "carol"}, Assignees: []string{"bob"}}
	got, _ := json.Marshal(m)
	exp, _ := json.Marshal(want)
	if string(got) != string(exp) {
		t.Fatalf("got %s\nwant %s", got, exp)
	}
	for _, tc := range []struct {
		user          string
		finding, memb bool
	}{
		{"alice", true, true}, {"carol", true, true}, {"bob", true, true}, {"dave", false, false},
	} {
		if m.CanSendFinding(tc.user) != tc.finding || m.IsMember(tc.user) != tc.memb {
			t.Errorf("%s: CanSendFinding=%v IsMember=%v", tc.user, m.CanSendFinding(tc.user), m.IsMember(tc.user))
		}
	}
}

func TestGitLabDiffFilesPaginates(t *testing.T) {
	srv, hits, _ := fakeGitLab(t)
	now := time.Now()
	g := newTestGitLab(srv, &now)
	m, err := g.Get("parley-lab/orders-service", 7)
	if err != nil {
		t.Fatal(err)
	}
	files, err := g.DiffFiles(m)
	if err != nil {
		t.Fatal(err)
	}
	want := "internal/price.go,old.go,new.go,internal/price_test.go"
	if strings.Join(files, ",") != want {
		t.Fatalf("got %v want %s", files, want)
	}
	if *hits != 3 { // get + two diff pages
		t.Fatalf("hits=%d want 3", *hits)
	}
}

func TestGitLabErrors(t *testing.T) {
	srv, _, _ := fakeGitLab(t)
	now := time.Now()
	g := newTestGitLab(srv, &now)
	for _, tc := range []struct {
		name  string
		repo  string
		iid   int
		token string
		want  string
	}{
		{"missing mr", "parley-lab/orders-service", 9, "tok", "notfound"},
		{"missing project", "other/project", 7, "tok", "notfound"},
		{"forbidden", "parley-lab/orders-service", 403, "tok", "AIC_GITLAB_TOKEN"},
		{"bad token", "parley-lab/orders-service", 7, "nope", "AIC_GITLAB_TOKEN"},
	} {
		g.Token = tc.token
		_, err := g.Get(tc.repo, tc.iid)
		switch tc.want {
		case "notfound":
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("%s: err=%v want ErrNotFound", tc.name, err)
			}
		default:
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: err=%v want %q", tc.name, err, tc.want)
			}
		}
	}
}

func TestGitLabCacheAndNotes(t *testing.T) {
	srv, hits, notes := fakeGitLab(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	g := newTestGitLab(srv, &now)
	m, _ := g.Get("parley-lab/orders-service", 7)
	_, _ = g.Get("parley-lab/orders-service", 7)
	_, _ = g.DiffFiles(m)
	_, _ = g.DiffFiles(m)
	if *hits != 3 {
		t.Fatalf("cache miss: hits=%d want 3", *hits)
	}
	now = now.Add(31 * time.Second)
	_, _ = g.Get("parley-lab/orders-service", 7)
	if *hits != 4 {
		t.Fatalf("ttl: hits=%d want 4", *hits)
	}
	if err := g.PostNote(m, "alice", "done\nsee commit"); err != nil {
		t.Fatal(err)
	}
	if len(*notes) != 1 || !strings.HasPrefix((*notes)[0], "**alice** · 2026-11-01T12:00:31Z\n\n> done\n> see commit") {
		t.Fatalf("note=%q", *notes)
	}
	_, _ = g.Get("parley-lab/orders-service", 7)
	if *hits != 6 { // note + refetch after invalidation
		t.Fatalf("invalidation: hits=%d want 6", *hits)
	}
}

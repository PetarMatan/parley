package route

import (
	"testing"
	"time"
)

func TestPick(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	live := []Candidate{
		{SessionID: "old-main", User: "b", Repo: "local/shop", Branch: "main", LastActive: t0},
		{SessionID: "new-main", User: "b", Repo: "local/shop", Branch: "main", LastActive: t0.Add(time.Minute)},
		{SessionID: "quarantine", User: "b", Repo: "local/shop", Branch: "feature/discount", LastActive: t0.Add(-time.Hour)},
		{SessionID: "other-repo", User: "b", Repo: "local/other", Branch: "feature/discount", LastActive: t0.Add(time.Hour)},
		{SessionID: "a-session", User: "a", Repo: "local/shop", Branch: "feature/discount", LastActive: t0.Add(time.Hour)},
	}
	cases := []struct {
		name string
		live []Candidate
		t    Target
		want string
	}{
		{"branch wins over recency", live, Target{To: "b", Repo: "local/shop", SourceBranch: "feature/discount"}, "quarantine"},
		{"recency without branch match", live[:2], Target{To: "b", Repo: "local/shop", SourceBranch: "feature/discount"}, "new-main"},
		{"preferred wins", live, Target{To: "b", Repo: "local/shop", SourceBranch: "feature/discount", Preferred: "old-main"}, "old-main"},
		{"preferred in other repo ignored", live, Target{To: "b", Repo: "local/shop", SourceBranch: "x", Preferred: "other-repo"}, "new-main"},
		{"repo filter", live, Target{To: "b", Repo: "local/other", SourceBranch: "main"}, "other-repo"},
		{"no session", live, Target{To: "c", Repo: "local/shop"}, ""},
		{"quarantine wins over branch and preferred", append([]Candidate{{SessionID: "q", User: "b", Repo: "local/shop", Branch: "aic/q-1", Quarantine: true, LastActive: t0.Add(-2 * time.Hour)}}, live...),
			Target{To: "b", Repo: "local/shop", SourceBranch: "feature/discount", Preferred: "old-main"}, "q"},
		{"author message without a quarantine stays queued", live,
			Target{To: "b", Repo: "local/shop", SourceBranch: "feature/discount", Preferred: "old-main", QuarantineOnly: true}, ""},
		{"author message goes to the quarantine", append([]Candidate{{SessionID: "q", User: "b", Repo: "local/shop", Branch: "aic/q-1", Quarantine: true, LastActive: t0.Add(-2 * time.Hour)}}, live...),
			Target{To: "b", Repo: "local/shop", SourceBranch: "feature/discount", Preferred: "old-main", QuarantineOnly: true}, "q"},
	}
	for _, c := range cases {
		if got := Pick(c.live, c.t); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

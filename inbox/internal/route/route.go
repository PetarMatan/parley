// Package route picks the one session a message is pushed to (contract 6,
// delivery rule and reply routing).
package route

import (
	"sort"
	"time"
)

// Candidate is a live session (stream open).
type Candidate struct {
	SessionID  string
	User       string
	Repo       string
	Branch     string
	Quarantine bool // registered from a quarantine worktree (D15)
	LastActive time.Time
}

// Target describes where a message should go.
type Target struct {
	To           string // addressee email
	Repo         string // repo of the context
	SourceBranch string // merge request source branch
	Preferred    string // session that last sent or acked in this task, if any
	// QuarantineOnly is set for messages addressed to the merge request
	// author. They go only to the author's quarantine sessions; with none
	// live they stay queued, so a normal session never receives them.
	QuarantineOnly bool
}

// Pick returns the chosen session id, or "" when the message stays queued.
// A message for the MR author (QuarantineOnly) goes only to a live quarantine
// session of the author. For other messages a live quarantine session of the
// addressee outranks every other session (D15).
// Within that set the order is: the preferred session, then a session on the
// source branch, then the most recently active one. Ties break on session id
// so the choice is stable.
func Pick(live []Candidate, t Target) string {
	var cands, quarantined []Candidate
	for _, c := range live {
		if c.User == t.To && c.Repo == t.Repo {
			cands = append(cands, c)
			if c.Quarantine {
				quarantined = append(quarantined, c)
			}
		}
	}
	if len(quarantined) > 0 || t.QuarantineOnly {
		cands = quarantined
	}
	if len(cands) == 0 {
		return ""
	}
	for _, c := range cands {
		if t.Preferred != "" && c.SessionID == t.Preferred {
			return c.SessionID
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if ab, bb := a.Branch == t.SourceBranch, b.Branch == t.SourceBranch; ab != bb {
			return ab
		}
		if !a.LastActive.Equal(b.LastActive) {
			return a.LastActive.After(b.LastActive)
		}
		return a.SessionID < b.SessionID
	})
	return cands[0].SessionID
}

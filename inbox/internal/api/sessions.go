package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/PetarMatan/parley/inbox/internal/logx"
	"github.com/PetarMatan/parley/inbox/internal/route"
	"github.com/PetarMatan/parley/inbox/internal/store"
)

var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// stream is one open SSE connection. ch carries encoded envelopes.
type stream struct {
	sid  string
	ch   chan []byte
	done chan struct{} // closed when the inbox ends the stream (DELETE or replace)
}

func (s *Server) sessionFromPath(r *http.Request, rl *reqLog) (string, error) {
	sid := r.PathValue("session_id")
	rl.sessionID = sid
	if !sessionIDRe.MatchString(sid) {
		return "", apiErr(http.StatusBadRequest, "invalid_field", "session_id must match %s", sessionIDRe)
	}
	return sid, nil
}

func (s *Server) putSession(w http.ResponseWriter, r *http.Request, rl *reqLog) error {
	sid, err := s.sessionFromPath(r, rl)
	if err != nil {
		return err
	}
	b, err := readBody(r)
	if err != nil {
		return err
	}
	var in struct {
		Host, Repo, Branch, Cwd string
		Quarantine              bool
	}
	if he := decodeStrict(b, &in); he != nil {
		return he
	}
	if in.Repo == "" {
		return apiErr(http.StatusBadRequest, "missing_field", "field repo is required")
	}
	for name, v := range map[string]string{"host": in.Host, "repo": in.Repo, "branch": in.Branch, "cwd": in.Cwd} {
		if utf8.RuneCountInString(v) > 1024 {
			return apiErr(http.StatusBadRequest, "too_long", "%s exceeds 1024 characters", name)
		}
	}
	user := identity(r).Email
	var out store.Session
	err = s.st.Update(func(d *store.Data) error {
		now := s.Now().UTC()
		sess := d.Sessions[sid]
		if sess != nil && sess.User != user {
			s.security("session_refused", rl, "session belongs to another user")
			return apiErr(http.StatusForbidden, "session_not_owned", "session belongs to another user")
		}
		if sess == nil {
			sess = &store.Session{SessionID: sid, User: user, RegisteredAt: now}
			d.Sessions[sid] = sess
		}
		sess.Host, sess.Repo, sess.Branch, sess.Cwd, sess.Quarantine, sess.LastActive = in.Host, in.Repo, in.Branch, in.Cwd, in.Quarantine, now
		out = *sess
		// A changed repo or branch can make queued messages routable.
		s.dispatchQueued(d, user)
		return nil
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request, rl *reqLog) error {
	sid, err := s.sessionFromPath(r, rl)
	if err != nil {
		return err
	}
	user := identity(r).Email
	err = s.st.Update(func(d *store.Data) error {
		sess := d.Sessions[sid]
		if sess == nil {
			return nil
		}
		if sess.User != user {
			s.security("session_refused", rl, "session belongs to another user")
			return apiErr(http.StatusForbidden, "session_not_owned", "session belongs to another user")
		}
		s.endStream(d, sid)
		delete(d.Sessions, sid)
		return nil
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// streamEvents is the SSE stream. Opening it makes the session live and
// pushes everything queued that routes to it.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request, rl *reqLog) error {
	sid, err := s.sessionFromPath(r, rl)
	if err != nil {
		return err
	}
	user := identity(r).Email
	st := &stream{sid: sid, ch: make(chan []byte, 256), done: make(chan struct{})}
	// Register before writing headers: once the client sees ": open" the
	// session is live, and anything pushed meanwhile waits in st.ch.
	err = s.st.Update(func(d *store.Data) error {
		sess := d.Sessions[sid]
		if sess == nil {
			return apiErr(http.StatusNotFound, "session_not_found", "register the session first")
		}
		if sess.User != user {
			s.security("session_refused", rl, "session belongs to another user")
			return apiErr(http.StatusForbidden, "session_not_owned", "session belongs to another user")
		}
		// A second stream for the same session replaces the first. Its
		// unacked pushes go back to the queue and come out on the new one.
		s.endStream(d, sid)
		s.hubMu.Lock()
		s.live[sid] = st
		s.hubMu.Unlock()
		sess.LastActive = s.Now().UTC()
		s.dispatchQueued(d, user)
		return nil
	})
	if err != nil {
		return err
	}
	defer s.closeStream(st)
	s.log.Info(logx.Fields{"event": "stream_open", "sender": user, "session_id": sid, "outcome": "ok"})

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, ": open\n\n"); err != nil {
		return nil
	}
	if err := rc.Flush(); err != nil {
		return nil
	}

	ping := time.NewTicker(s.cfg.PingInterval)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-st.done:
			return nil
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return nil
			}
		case env := <-st.ch:
			var id struct {
				MessageID string `json:"message_id"`
			}
			_ = json.Unmarshal(env, &id)
			if _, err := fmt.Fprintf(w, "event: message\nid: %s\ndata: %s\n\n", id.MessageID, env); err != nil {
				return nil
			}
		}
		if err := rc.Flush(); err != nil {
			return nil
		}
	}
}

// closeStream runs when a stream goroutine exits. It requeues only if this
// stream is still the live one, so a replaced stream cannot requeue messages
// pushed to its successor.
func (s *Server) closeStream(st *stream) {
	_ = s.st.Update(func(d *store.Data) error {
		s.hubMu.Lock()
		current := s.live[st.sid] == st
		s.hubMu.Unlock()
		if current {
			s.endStream(d, st.sid)
		}
		return nil
	})
	s.log.Info(logx.Fields{"event": "stream_close", "session_id": st.sid, "outcome": "ok"})
}

// endStream removes the live stream of sid, signals it to stop and returns
// its unacked pushed messages to queued. Caller holds the store lock.
func (s *Server) endStream(d *store.Data, sid string) {
	s.hubMu.Lock()
	if st := s.live[sid]; st != nil {
		delete(s.live, sid)
		close(st.done)
	}
	s.hubMu.Unlock()
	for _, m := range d.Messages {
		if m.Envelope.State == store.Pushed && m.PushedTo == sid {
			m.Envelope.State, m.PushedTo = store.Queued, ""
		}
	}
}

// dispatchQueued tries to push every queued message addressed to user.
// Caller holds the store lock.
func (s *Server) dispatchQueued(d *store.Data, user string) {
	for _, m := range d.Messages {
		if m.Envelope.State == store.Queued && m.Envelope.To == user {
			s.dispatch(d, m)
		}
	}
}

// forAuthor reports whether env is addressed to the merge request author of
// task t: the finding and every later message of the task sent to the author.
// Only the author's quarantine session receives and acks those.
func forAuthor(t *store.Task, env *store.Envelope) bool {
	return t != nil && env.To == t.Addressee
}

// dispatch pushes m to the one session the routing rule picks. The send never
// blocks: a full stream buffer leaves the message queued. Caller holds the
// store lock.
func (s *Server) dispatch(d *store.Data, m *store.Message) {
	env := &m.Envelope
	ctx := d.Contexts[env.ContextID]
	if ctx == nil {
		return
	}
	target := route.Target{To: env.To, Repo: ctx.Repo, SourceBranch: ctx.SourceBranch}
	if t := ctx.Tasks[env.TaskID]; t != nil {
		target.Preferred = t.Sessions[env.To]
		target.QuarantineOnly = forAuthor(t, env)
	}
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	var cands []route.Candidate
	for sid := range s.live {
		if sess := d.Sessions[sid]; sess != nil {
			cands = append(cands, route.Candidate{SessionID: sid, User: sess.User, Repo: sess.Repo, Branch: sess.Branch, Quarantine: sess.Quarantine, LastActive: sess.LastActive})
		}
	}
	sid := route.Pick(cands, target)
	if sid == "" {
		return
	}
	out := *env
	out.State = store.Pushed
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	select {
	case s.live[sid].ch <- b:
		env.State, m.PushedTo = store.Pushed, sid
	default:
	}
}

// Package api is the inbox HTTP API of contract section 6.
package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentic-conversation/aic/internal/auth"
	"agentic-conversation/aic/internal/logx"
	"agentic-conversation/aic/internal/mr"
	"agentic-conversation/aic/internal/rules"
	"agentic-conversation/aic/internal/store"
)

// MaxBody is the request body cap (contract 6).
const MaxBody = 16 << 10

// SystemSender is the from of envelopes the inbox writes itself.
const SystemSender = "aic-inbox"

// SessionHeader optionally names the caller's session on POST /v1/messages,
// so reply routing can prefer the session that sent in a task.
const SessionHeader = "Aic-Session-Id"

// Verifier checks a bearer token.
type Verifier interface {
	Verify(token string) (auth.Identity, error)
}

// Config holds server settings.
type Config struct {
	MaxRounds    int
	PingInterval time.Duration
	// ServiceSenders may post notes and read contexts of any merge request.
	ServiceSenders []string
}

// Server is the inbox.
type Server struct {
	cfg   Config
	st    *store.Store
	mrs   mr.Provider
	ver   Verifier
	log   *logx.Logger
	Now   func() time.Time
	hubMu sync.Mutex
	live  map[string]*stream // session id -> open stream
}

// New builds a server. Messages left pushed by a previous run go back to
// queued, because no stream survives a restart.
func New(cfg Config, st *store.Store, mrs mr.Provider, ver Verifier, log *logx.Logger) (*Server, error) {
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = 5
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = 15 * time.Second
	}
	s := &Server{cfg: cfg, st: st, mrs: mrs, ver: ver, log: log, Now: time.Now, live: map[string]*stream{}}
	err := st.Update(func(d *store.Data) error {
		for _, m := range d.Messages {
			if m.Envelope.State == store.Pushed {
				m.Envelope.State, m.PushedTo = store.Queued, ""
			}
		}
		return nil
	})
	return s, err
}

// Handler returns the routed HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	route := func(pattern, event string, h func(http.ResponseWriter, *http.Request, *reqLog) error) {
		mux.Handle(pattern, s.wrap(event, h))
	}
	route("PUT /v1/sessions/{session_id}", "session_put", s.putSession)
	route("DELETE /v1/sessions/{session_id}", "session_delete", s.deleteSession)
	route("GET /v1/sessions/{session_id}/events", "session_stream", s.streamEvents)
	route("GET /v1/inbox", "inbox_list", s.listInbox)
	route("POST /v1/messages", "message_post", s.postMessage)
	route("POST /v1/messages/{message_id}/ack", "message_ack", s.ackMessage)
	route("GET /v1/contexts/{context_id}", "context_get", s.getContext)
	route("POST /v1/contexts/{context_id}/notes", "note_post", s.postNote)
	mux.Handle("/", s.wrap("not_found", func(http.ResponseWriter, *http.Request, *reqLog) error {
		return apiErr(http.StatusNotFound, "not_found", "no such route")
	}))
	return mux
}

// reqLog is the per-request log line; handlers fill in what they know.
type reqLog struct {
	sender, sessionID, contextID, taskID, messageID string
}

// httpError is a 4xx/5xx with the contract error body.
type httpError struct {
	status        int
	code, message string
}

func (e *httpError) Error() string { return e.code + ": " + e.message }

func apiErr(status int, code, format string, a ...any) *httpError {
	return &httpError{status: status, code: code, message: fmt.Sprintf(format, a...)}
}

func ruleErr(e *rules.RuleError) *httpError {
	return &httpError{status: http.StatusBadRequest, code: e.Code, message: e.Message}
}

// wrap authenticates, caps the body, runs h, writes errors and logs one line.
func (s *Server) wrap(event string, h func(http.ResponseWriter, *http.Request, *reqLog) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rl := &reqLog{}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		var err error
		defer func() {
			if p := recover(); p != nil {
				err = apiErr(http.StatusInternalServerError, "internal", "internal error")
				s.log.Error(logx.Fields{"event": "panic", "detail": fmt.Sprint(p)})
			}
			var he *httpError
			if err != nil && !errors.As(err, &he) {
				s.log.Error(logx.Fields{"event": event, "detail": err.Error()})
				he = apiErr(http.StatusInternalServerError, "internal", "internal error")
			}
			if he != nil && !rec.wrote {
				writeJSON(rec, he.status, map[string]any{"error": map[string]string{"code": he.code, "message": he.message}})
			}
			s.logRequest(event, r, rl, rec.status, he)
		}()

		id, aerr := s.authenticate(r)
		if aerr != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="aic-inbox"`)
			err = aerr
			return
		}
		rl.sender = id.Email
		if r.ContentLength > MaxBody {
			err = apiErr(http.StatusRequestEntityTooLarge, "too_large", "request body exceeds %d bytes", MaxBody)
			return
		}
		r.Body = http.MaxBytesReader(rec, r.Body, MaxBody)
		r = r.WithContext(context.WithValue(r.Context(), identityKey{}, id))
		err = h(rec, r, rl)
	})
}

func (s *Server) logRequest(event string, r *http.Request, rl *reqLog, status int, he *httpError) {
	level, outcome, code := logx.Info, "ok", ""
	if he != nil {
		level, outcome, code = logx.Warn, "refused", he.code
		if he.status >= 500 {
			level, outcome = logx.Error, "error"
		}
	}
	s.log.Log(level, logx.Fields{
		"event": event, "method": r.Method, "status": status, "sender": rl.sender,
		"session_id": rl.sessionID, "context_id": rl.contextID, "task_id": rl.taskID,
		"message_id": rl.messageID, "outcome": outcome, "error_code": code,
	})
}

// security logs a refusal as its own WARN line (contract 6, sender gate).
func (s *Server) security(event string, rl *reqLog, reason string) {
	s.log.Warn(logx.Fields{
		"event": event, "security": true, "sender": rl.sender, "session_id": rl.sessionID,
		"context_id": rl.contextID, "task_id": rl.taskID, "message_id": rl.messageID, "reason": reason,
	})
}

type identityKey struct{}

func identity(r *http.Request) auth.Identity {
	id, _ := r.Context().Value(identityKey{}).(auth.Identity)
	return id
}

func (s *Server) authenticate(r *http.Request) (auth.Identity, *httpError) {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || strings.TrimSpace(tok) == "" {
		return auth.Identity{}, apiErr(http.StatusUnauthorized, "unauthorized", "bearer token required")
	}
	id, err := s.ver.Verify(strings.TrimSpace(tok))
	if err != nil {
		s.log.Warn(logx.Fields{"event": "auth_failed", "security": true, "path": r.URL.Path, "reason": err.Error()})
		return auth.Identity{}, apiErr(http.StatusUnauthorized, "unauthorized", "invalid token")
	}
	return id, nil
}

// readBody reads the capped body; an over-cap body is 413 too_large.
func readBody(r *http.Request) ([]byte, error) {
	b, err := io.ReadAll(r.Body)
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return nil, apiErr(http.StatusRequestEntityTooLarge, "too_large", "request body exceeds %d bytes", MaxBody)
	}
	return b, err
}

// decodeStrict decodes a small JSON object with no unknown fields.
func decodeStrict(b []byte, dst any) *httpError {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			return apiErr(http.StatusBadRequest, rules.CodeUnknownField, "%s", strings.TrimPrefix(err.Error(), "json: "))
		}
		return apiErr(http.StatusBadRequest, rules.CodeBadJSON, "%v", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status, r.wrote = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach Flush on the real writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// parseContextID splits <repo>!<iid>.
func parseContextID(id string) (string, int, *httpError) {
	if !rules.ContextIDRe.MatchString(id) {
		return "", 0, apiErr(http.StatusBadRequest, rules.CodeInvalidField, "context_id must be <repo>!<iid>")
	}
	i := strings.LastIndexByte(id, '!')
	iid, err := strconv.Atoi(id[i+1:])
	if err != nil {
		return "", 0, apiErr(http.StatusBadRequest, rules.CodeInvalidField, "context_id iid is not an integer")
	}
	return id[:i], iid, nil
}

// loadMR returns the merge request of a context id, or 404 mr_not_found.
func (s *Server) loadMR(contextID string) (*mr.MR, error) {
	repo, iid, he := parseContextID(contextID)
	if he != nil {
		return nil, he
	}
	m, err := s.mrs.Get(repo, iid)
	if errors.Is(err, mr.ErrNotFound) {
		return nil, apiErr(http.StatusNotFound, "mr_not_found", "no merge request %s", contextID)
	}
	return m, err
}

func (s *Server) isService(user string) bool { return slices.Contains(s.cfg.ServiceSenders, user) }

func (s *Server) nowISO() string { return s.Now().UTC().Format(time.RFC3339) }

// newMessageID is m-<10 char time><16 char random>, Crockford base32, so ids
// sort by creation time like a ULID.
func newMessageID(now time.Time) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var out [26]byte
	ms := uint64(now.UnixMilli())
	for i := 9; i >= 0; i-- {
		out[i] = alphabet[ms&31]
		ms >>= 5
	}
	var rnd [16]byte
	_, _ = rand.Read(rnd[:])
	for i, b := range rnd {
		out[10+i] = alphabet[b&31]
	}
	return "m-" + string(out[:])
}

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"

	"github.com/PetarMatan/parley/inbox/internal/logx"
	"github.com/PetarMatan/parley/inbox/internal/mr"
	"github.com/PetarMatan/parley/inbox/internal/rules"
	"github.com/PetarMatan/parley/inbox/internal/store"
)

type postResult struct {
	MessageID string `json:"message_id"`
	Round     int    `json:"round"`
	Delivered string `json:"delivered"`
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request, rl *reqLog) error {
	b, err := readBody(r)
	if err != nil {
		return err
	}
	var head struct {
		Schema string `json:"schema"`
	}
	_ = json.Unmarshal(b, &head) // the validators report bad JSON properly
	var res postResult
	switch head.Schema {
	case "reply.v1":
		res, err = s.postReply(r, rl, b)
	case "finding.v1":
		res, err = s.postFinding(r, rl, b)
	default:
		if _, _, re := rules.ValidateFinding(b); re != nil && re.Code == rules.CodeBadJSON {
			return ruleErr(re)
		}
		return apiErr(http.StatusBadRequest, rules.CodeInvalidField, `schema must be "finding.v1" or "reply.v1"`)
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, res)
	return nil
}

func (s *Server) postFinding(r *http.Request, rl *reqLog, b []byte) (postResult, error) {
	f, tags, re := rules.ValidateFinding(b)
	if re != nil {
		return postResult{}, ruleErr(re)
	}
	rl.contextID, rl.taskID = f.ContextID, f.TaskID
	sender := identity(r).Email
	m, err := s.loadMR(f.ContextID)
	if err != nil {
		return postResult{}, err
	}
	if !m.CanSendFinding(sender) {
		s.security("sender_refused", rl, "finding sender is not a reviewer or assignee")
		return postResult{}, apiErr(http.StatusForbidden, "sender_not_allowed", "only a reviewer or assignee of the merge request may send a finding")
	}
	if rules.IsBlockedFile(f.File) {
		return postResult{}, apiErr(http.StatusUnprocessableEntity, "blocked_file", "%s is a blocked file class", f.File)
	}
	files, err := s.mrs.DiffFiles(m)
	if err != nil {
		return postResult{}, err
	}
	if !slices.Contains(files, f.File) {
		return postResult{}, apiErr(http.StatusUnprocessableEntity, "file_not_in_diff", "%s is not changed by the merge request", f.File)
	}
	payload, err := json.Marshal(f)
	if err != nil {
		return postResult{}, err
	}

	var res postResult
	err = s.st.Update(func(d *store.Data) error {
		ctx := ensureContext(d, f.ContextID, m)
		if ctx.Tasks[f.TaskID] != nil {
			return apiErr(http.StatusConflict, "duplicate_task", "task %s already exists in %s", f.TaskID, f.ContextID)
		}
		now := s.Now().UTC()
		t := &store.Task{ID: f.TaskID, State: store.TaskSubmitted, Round: 1, Class: f.Class,
			Sender: sender, Addressee: m.Author, Sessions: map[string]string{}, CreatedAt: now, UpdatedAt: now}
		s.noteSession(d, r, rl, sender, t)
		ctx.Tasks[t.ID] = t
		addParticipants(ctx, sender, m.Author)
		msg := s.appendMessage(d, ctx, t.ID, 1, sender, m.Author, payload, tags)
		s.dispatch(d, msg)
		rl.messageID = msg.Envelope.MessageID
		res = postResult{MessageID: msg.Envelope.MessageID, Round: 1, Delivered: msg.Envelope.State}
		return nil
	})
	return res, err
}

func (s *Server) postReply(r *http.Request, rl *reqLog, b []byte) (postResult, error) {
	rp, tags, re := rules.ValidateReply(b)
	if re != nil {
		return postResult{}, ruleErr(re)
	}
	rl.contextID, rl.taskID = rp.ContextID, rp.TaskID
	sender := identity(r).Email
	m, err := s.loadMR(rp.ContextID)
	if err != nil {
		return postResult{}, err
	}
	payload, err := json.Marshal(rp)
	if err != nil {
		return postResult{}, err
	}

	var res postResult
	var capped *store.Task
	err = s.st.Update(func(d *store.Data) error {
		ctx := d.Contexts[rp.ContextID]
		var t *store.Task
		if ctx != nil {
			t = ctx.Tasks[rp.TaskID]
		}
		if t == nil {
			return apiErr(http.StatusNotFound, "task_not_found", "no task %s in %s", rp.TaskID, rp.ContextID)
		}
		if sender != t.Sender && sender != m.Author {
			s.security("sender_refused", rl, "reply sender is not a participant of the task")
			return apiErr(http.StatusForbidden, "sender_not_allowed", "only the finding's sender or the merge request author may reply")
		}
		if t.State == store.TaskFailed {
			return apiErr(http.StatusConflict, "round_limit", "task %s stopped at the round limit", t.ID)
		}
		round := t.Round + 1
		now := s.Now().UTC()
		if round > s.cfg.MaxRounds {
			// Refuse, but save: the task fails and both sides are told.
			t.State, t.UpdatedAt = store.TaskFailed, now
			for _, to := range uniq(t.Sender, t.Addressee) {
				sys := s.systemEscalation(rp.ContextID, t.ID, sender, round)
				s.dispatch(d, s.appendMessage(d, ctx, t.ID, round, SystemSender, to, sys, rules.Tags{Stripped: []string{}}))
			}
			c := *t
			capped = &c
			return nil
		}
		to := t.Sender
		if sender == t.Sender {
			to = t.Addressee
		}
		t.Round, t.UpdatedAt = round, now
		t.State = rules.ReplyKindState[rp.Kind]
		s.noteSession(d, r, rl, sender, t)
		msg := s.appendMessage(d, ctx, t.ID, round, sender, to, payload, tags)
		s.dispatch(d, msg)
		rl.messageID = msg.Envelope.MessageID
		res = postResult{MessageID: msg.Envelope.MessageID, Round: round, Delivered: msg.Envelope.State}
		return nil
	})
	if err != nil {
		return postResult{}, err
	}
	if capped != nil {
		note := fmt.Sprintf("**aic-inbox:** task `%s` reached the round limit of %d. Round %d from %s was refused. "+
			"The task is failed and needs a human decision.", capped.ID, s.cfg.MaxRounds, capped.Round+1, sender)
		if err := s.mrs.PostNote(m, SystemSender, note); err != nil {
			s.log.Error(logx.Fields{"event": "mr_note_failed", "context_id": rp.ContextID, "task_id": capped.ID, "detail": err.Error()})
		}
		s.log.Warn(logx.Fields{"event": "round_limit", "sender": sender, "context_id": rp.ContextID, "task_id": capped.ID, "outcome": "refused"})
		return postResult{}, apiErr(http.StatusConflict, "round_limit", "round %d exceeds the limit of %d; the task is failed", capped.Round+1, s.cfg.MaxRounds)
	}
	return res, nil
}

func (s *Server) systemEscalation(contextID, taskID, refusedSender string, round int) json.RawMessage {
	b, _ := json.Marshal(rules.Reply{
		Schema: "reply.v1", ContextID: contextID, TaskID: taskID, Kind: "escalated",
		Note: fmt.Sprintf("Round limit of %d reached. The inbox refused round %d from %s. The task is failed; a human decides on the merge request.",
			s.cfg.MaxRounds, round, refusedSender),
	})
	return b
}

// noteSession records the caller's session (from SessionHeader) for reply
// routing, if it is a session the caller owns.
func (s *Server) noteSession(d *store.Data, r *http.Request, rl *reqLog, user string, t *store.Task) {
	sid := r.Header.Get(SessionHeader)
	if sess := d.Sessions[sid]; sid != "" && sess != nil && sess.User == user {
		rl.sessionID = sid
		t.Sessions[user] = sid
		sess.LastActive = s.Now().UTC()
	}
}

func (s *Server) appendMessage(d *store.Data, ctx *store.Context, taskID string, round int, from, to string, payload json.RawMessage, tags rules.Tags) *store.Message {
	if tags.Stripped == nil {
		tags.Stripped = []string{}
	}
	msg := &store.Message{
		Repo: ctx.Repo,
		Envelope: store.Envelope{
			MessageID: newMessageID(s.Now()), ContextID: ctx.ContextID, TaskID: taskID, Round: round,
			From: from, To: to, SentAt: s.nowISO(), Payload: payload, Tags: tags, State: store.Queued,
		},
	}
	d.Messages = append(d.Messages, msg)
	return msg
}

func ensureContext(d *store.Data, id string, m *mr.MR) *store.Context {
	ctx := d.Contexts[id]
	if ctx == nil {
		ctx = &store.Context{ContextID: id, Repo: m.Repo, Tasks: map[string]*store.Task{}, Participants: []string{}}
		d.Contexts[id] = ctx
	}
	ctx.SourceBranch = m.SourceBranch
	return ctx
}

func addParticipants(ctx *store.Context, users ...string) {
	for _, u := range users {
		if !slices.Contains(ctx.Participants, u) {
			ctx.Participants = append(ctx.Participants, u)
		}
	}
}

func uniq(a, b string) []string {
	if a == b {
		return []string{a}
	}
	return []string{a, b}
}

func (s *Server) listInbox(w http.ResponseWriter, r *http.Request, rl *reqLog) error {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		return apiErr(http.StatusBadRequest, rules.CodeMissingField, "query parameter repo is required")
	}
	user := identity(r).Email
	out := []store.Envelope{}
	s.st.View(func(d *store.Data) {
		for _, m := range d.Messages {
			st := m.Envelope.State
			if m.Envelope.To == user && m.Repo == repo && (st == store.Queued || st == store.Pushed) {
				out = append(out, m.Envelope)
			}
		}
	})
	writeJSON(w, http.StatusOK, map[string]any{"messages": out})
	return nil
}

func (s *Server) ackMessage(w http.ResponseWriter, r *http.Request, rl *reqLog) error {
	mid := r.PathValue("message_id")
	rl.messageID = mid
	b, err := readBody(r)
	if err != nil {
		return err
	}
	var in struct {
		SessionID string `json:"session_id"`
	}
	if he := decodeStrict(b, &in); he != nil {
		return he
	}
	if in.SessionID == "" {
		return apiErr(http.StatusBadRequest, rules.CodeMissingField, "field session_id is required")
	}
	rl.sessionID = in.SessionID
	user := identity(r).Email
	err = s.st.Update(func(d *store.Data) error {
		m := d.Message(mid)
		if m == nil {
			return apiErr(http.StatusNotFound, "message_not_found", "no message %s", mid)
		}
		rl.contextID, rl.taskID = m.Envelope.ContextID, m.Envelope.TaskID
		if m.Envelope.To != user {
			s.security("ack_refused", rl, "caller is not the addressee")
			return apiErr(http.StatusForbidden, "not_addressee", "only the addressee may ack")
		}
		sess := d.Sessions[in.SessionID]
		if sess == nil {
			return apiErr(http.StatusNotFound, "session_not_found", "no session %s", in.SessionID)
		}
		if sess.User != user {
			s.security("ack_refused", rl, "session belongs to another user")
			return apiErr(http.StatusForbidden, "session_not_owned", "session belongs to another user")
		}
		var task *store.Task
		if ctx := d.Contexts[m.Envelope.ContextID]; ctx != nil {
			task = ctx.Tasks[m.Envelope.TaskID]
		}
		if forAuthor(task, &m.Envelope) && !sess.Quarantine {
			// A normal session of the author may list it (read-only brief),
			// but only the quarantine handles it. The request log records the code.
			return apiErr(http.StatusConflict, "quarantine_required", "messages for the merge request author are acked only by a quarantine session")
		}
		sess.LastActive = s.Now().UTC()
		if m.Envelope.State == store.Acked {
			return nil // idempotent
		}
		// queued is accepted too: the SessionStart hook acks what it fetched.
		m.Envelope.State, m.PushedTo, m.AckedBy = store.Acked, "", in.SessionID
		if t := task; t != nil {
			t.Sessions[user] = in.SessionID
			if t.State == store.TaskSubmitted && m.Envelope.Round == 1 {
				t.State, t.UpdatedAt = store.TaskWorking, s.Now().UTC()
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type taskView struct {
	TaskID    string `json:"task_id"`
	State     string `json:"state"`
	Round     int    `json:"round"`
	Class     string `json:"class"`
	Sender    string `json:"sender"`
	Addressee string `json:"addressee"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (s *Server) getContext(w http.ResponseWriter, r *http.Request, rl *reqLog) error {
	cid := r.PathValue("context_id")
	rl.contextID = cid
	m, err := s.loadMR(cid)
	if err != nil {
		return err
	}
	user := identity(r).Email
	if !m.IsMember(user) && !s.isService(user) {
		s.security("read_refused", rl, "caller is not a member of the merge request")
		return apiErr(http.StatusForbidden, "not_member", "only members of the merge request may read its conversation")
	}
	out := struct {
		ContextID    string           `json:"context_id"`
		Participants []string         `json:"participants"`
		Tasks        []taskView       `json:"tasks"`
		Messages     []store.Envelope `json:"messages"`
	}{ContextID: cid, Participants: []string{}, Tasks: []taskView{}, Messages: []store.Envelope{}}
	s.st.View(func(d *store.Data) {
		ctx := d.Contexts[cid]
		if ctx == nil {
			return
		}
		out.Participants = append(out.Participants, ctx.Participants...)
		for _, t := range ctx.Tasks {
			out.Tasks = append(out.Tasks, taskView{t.ID, t.State, t.Round, t.Class, t.Sender, t.Addressee,
				t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"), t.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")})
		}
		for _, msg := range d.Messages {
			if msg.Envelope.ContextID == cid {
				out.Messages = append(out.Messages, msg.Envelope)
			}
		}
	})
	sort.Slice(out.Tasks, func(i, j int) bool {
		if out.Tasks[i].CreatedAt != out.Tasks[j].CreatedAt {
			return out.Tasks[i].CreatedAt < out.Tasks[j].CreatedAt
		}
		return out.Tasks[i].TaskID < out.Tasks[j].TaskID
	})
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) postNote(w http.ResponseWriter, r *http.Request, rl *reqLog) error {
	cid := r.PathValue("context_id")
	rl.contextID = cid
	b, err := readBody(r)
	if err != nil {
		return err
	}
	n, _, re := rules.ValidateNote(b)
	if re != nil {
		return ruleErr(re)
	}
	if n.ContextID != cid {
		return apiErr(http.StatusBadRequest, rules.CodeInvalidField, "context_id in the body does not match the path")
	}
	m, err := s.loadMR(cid)
	if err != nil {
		return err
	}
	user := identity(r).Email
	if !m.IsMember(user) && !s.isService(user) {
		s.security("sender_refused", rl, "note sender is not a member of the merge request")
		return apiErr(http.StatusForbidden, "sender_not_allowed", "only members of the merge request may post notes")
	}
	if err := s.mrs.PostNote(m, user, n.Body); err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, map[string]string{"context_id": cid, "posted_at": s.nowISO()})
	return nil
}

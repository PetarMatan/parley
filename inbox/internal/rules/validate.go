package rules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Error codes. Every 4xx body uses one of these.
const (
	CodeBadJSON      = "bad_json"
	CodeUnknownField = "unknown_field"
	CodeInvalidType  = "invalid_type"
	CodeMissingField = "missing_field"
	CodeBadEnum      = "bad_enum"
	CodeTooLong      = "too_long"
	CodeInvalidField = "invalid_field"
	CodeEmptyField   = "empty_field"
)

// RuleError is a validation failure with a stable snake_case code.
type RuleError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RuleError) Error() string { return e.Code + ": " + e.Message }

func fail(code, format string, a ...any) *RuleError {
	return &RuleError{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Tags travel with the stored envelope.
type Tags struct {
	InjectionScore float64  `json:"injection_score"`
	Stripped       []string `json:"stripped"`
}

type Finding struct {
	Schema         string `json:"schema"`
	ContextID      string `json:"context_id"`
	TaskID         string `json:"task_id"`
	File           string `json:"file"`
	Line           int    `json:"line"`
	Category       string `json:"category"`
	Severity       string `json:"severity"`
	Class          string `json:"class"`
	Title          string `json:"title"`
	Note           string `json:"note"`
	SuggestedPatch string `json:"suggested_patch,omitempty"`
}

type Reply struct {
	Schema    string `json:"schema"`
	ContextID string `json:"context_id"`
	TaskID    string `json:"task_id"`
	Kind      string `json:"kind"`
	Note      string `json:"note"`
	Commit    string `json:"commit,omitempty"`
}

type Note struct {
	Schema    string `json:"schema"`
	ContextID string `json:"context_id"`
	Body      string `json:"body"`
}

var (
	Categories = []string{"bug", "security", "performance", "maintainability", "test", "docs", "style"}
	Severities = []string{"blocker", "major", "minor", "nit"}
	Classes    = []string{"fact", "taste", "product"}
	ReplyKinds = []string{"fixed", "disputed", "answer", "accepted", "withdrawn", "escalated", "acknowledged"}

	// ReplyKindState maps a reply kind to the task state it causes.
	ReplyKindState = map[string]string{
		"fixed": "completed", "accepted": "completed", "withdrawn": "canceled",
		"escalated": "input-required", "disputed": "working", "answer": "working", "acknowledged": "working",
	}
)

// Length limits in Unicode code points, checked on the raw value.
const (
	MaxTitle     = 120
	MaxNote      = 2000
	MaxPatch     = 4000
	MaxBody      = 8000
	MaxFile      = 512
	MaxContextID = 256
)

var (
	ContextIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)*![1-9][0-9]{0,9}$`)
	TaskIDRe    = regexp.MustCompile(`^f-[0-9a-f]{8}$`)
	CommitRe    = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// ValidateFinding parses, checks and cleans a finding.v1 body.
func ValidateFinding(raw []byte) (Finding, Tags, *RuleError) {
	var f Finding
	if e := parse(raw, &f, "schema", "context_id", "task_id", "file", "line", "category", "severity", "class", "title", "note"); e != nil {
		return f, Tags{}, e
	}
	if e := firstErr(
		checkSchema(f.Schema, "finding.v1"),
		checkContextID(f.ContextID),
		checkTaskID(f.TaskID),
		checkFile(f.File),
		checkLine(f.Line),
		checkEnum("category", f.Category, Categories),
		checkEnum("severity", f.Severity, Severities),
		checkEnum("class", f.Class, Classes),
		checkLen("title", f.Title, MaxTitle, true),
		checkLen("note", f.Note, MaxNote, true),
		checkLen("suggested_patch", f.SuggestedPatch, MaxPatch, false),
	); e != nil {
		return f, Tags{}, e
	}
	rawText := []string{f.Title, f.Note, f.SuggestedPatch}
	var s1, s2, s3 []string
	f.Title, s1 = Clean(f.Title, false)
	f.Note, s2 = Clean(f.Note, false)
	f.SuggestedPatch, s3 = Clean(f.SuggestedPatch, true)
	if strings.TrimSpace(f.Title) == "" {
		return f, Tags{}, fail(CodeEmptyField, "title is empty after cleaning")
	}
	cleaned := []string{f.Title, f.Note, f.SuggestedPatch}
	if e := scan(rawText, cleaned); e != nil {
		return f, Tags{}, e
	}
	return f, Tags{InjectionScore: InjectionScore(strings.Join(cleaned, "\n")), Stripped: mergeStripped(s1, s2, s3)}, nil
}

// ValidateReply parses, checks and cleans a reply.v1 body.
func ValidateReply(raw []byte) (Reply, Tags, *RuleError) {
	var r Reply
	if e := parse(raw, &r, "schema", "context_id", "task_id", "kind", "note"); e != nil {
		return r, Tags{}, e
	}
	if e := firstErr(
		checkSchema(r.Schema, "reply.v1"),
		checkContextID(r.ContextID),
		checkTaskID(r.TaskID),
		checkEnum("kind", r.Kind, ReplyKinds),
		checkLen("note", r.Note, MaxNote, true),
		checkCommit(r.Commit),
	); e != nil {
		return r, Tags{}, e
	}
	rawNote := r.Note
	var s []string
	r.Note, s = Clean(r.Note, false)
	if e := scan([]string{rawNote}, []string{r.Note}); e != nil {
		return r, Tags{}, e
	}
	return r, Tags{InjectionScore: InjectionScore(r.Note), Stripped: s}, nil
}

// ValidateNote parses, checks and cleans a note.v1 body.
func ValidateNote(raw []byte) (Note, Tags, *RuleError) {
	var n Note
	if e := parse(raw, &n, "schema", "context_id", "body"); e != nil {
		return n, Tags{}, e
	}
	if e := firstErr(
		checkSchema(n.Schema, "note.v1"),
		checkContextID(n.ContextID),
		checkLen("body", n.Body, MaxBody, true),
	); e != nil {
		return n, Tags{}, e
	}
	rawBody := n.Body
	var s []string
	n.Body, s = Clean(n.Body, false)
	if e := scan([]string{rawBody}, []string{n.Body}); e != nil {
		return n, Tags{}, e
	}
	return n, Tags{InjectionScore: InjectionScore(n.Body), Stripped: s}, nil
}

// parse decodes one JSON object strictly and checks required keys are present
// and not null.
func parse(raw []byte, dst any, required ...string) *RuleError {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) {
			return fail(CodeBadJSON, "body must be a JSON object")
		}
		return fail(CodeBadJSON, "%v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var te *json.UnmarshalTypeError
		switch {
		case errors.As(err, &te):
			return fail(CodeInvalidType, "field %s has the wrong type", te.Field)
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			return fail(CodeUnknownField, "%s", strings.TrimPrefix(err.Error(), "json: "))
		default:
			return fail(CodeBadJSON, "%v", err)
		}
	}
	for _, k := range required {
		if v, ok := keys[k]; !ok || string(bytes.TrimSpace(v)) == "null" {
			return fail(CodeMissingField, "field %s is required", k)
		}
	}
	return nil
}

// scan rejects a secret in either the raw or the cleaned text: cleaning can
// join a secret split by hidden characters, and a secret hidden in a comment
// was still sent.
func scan(raw, cleaned []string) *RuleError {
	for _, t := range append(slices.Clone(raw), cleaned...) {
		if name := MatchSecret(t); name != "" {
			return fail(CodeSecretDetected, "text matches secret pattern %s", name)
		}
	}
	return nil
}

func firstErr(errs ...*RuleError) *RuleError {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func checkSchema(got, want string) *RuleError {
	if got != want {
		return fail(CodeInvalidField, "schema must be %q", want)
	}
	return nil
}

func checkContextID(v string) *RuleError {
	if utf8.RuneCountInString(v) > MaxContextID || !ContextIDRe.MatchString(v) {
		return fail(CodeInvalidField, "context_id must be <repo>!<iid>")
	}
	return nil
}

func checkTaskID(v string) *RuleError {
	if !TaskIDRe.MatchString(v) {
		return fail(CodeInvalidField, "task_id must be f-<8 hex>")
	}
	return nil
}

func checkCommit(v string) *RuleError {
	if v != "" && !CommitRe.MatchString(v) {
		return fail(CodeInvalidField, "commit must be a 40-hex sha")
	}
	return nil
}

func checkLine(v int) *RuleError {
	if v < 1 {
		return fail(CodeInvalidField, "line must be >= 1")
	}
	return nil
}

// checkFile accepts a clean repository-relative path only.
func checkFile(v string) *RuleError {
	if v == "" || utf8.RuneCountInString(v) > MaxFile || strings.HasPrefix(v, "/") || strings.Contains(v, `\`) {
		return fail(CodeInvalidField, "file must be a repository-relative path")
	}
	for _, seg := range strings.Split(v, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fail(CodeInvalidField, "file must be a repository-relative path")
		}
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7F || hiddenClass(r) != "" {
			return fail(CodeInvalidField, "file contains a control or hidden character")
		}
	}
	return nil
}

func checkEnum(field, v string, allowed []string) *RuleError {
	if !slices.Contains(allowed, v) {
		return fail(CodeBadEnum, "%s must be one of %s", field, strings.Join(allowed, ", "))
	}
	return nil
}

func checkLen(field, v string, max int, required bool) *RuleError {
	if required && strings.TrimSpace(v) == "" {
		return fail(CodeEmptyField, "%s must not be empty", field)
	}
	if utf8.RuneCountInString(v) > max {
		return fail(CodeTooLong, "%s exceeds %d characters", field, max)
	}
	return nil
}

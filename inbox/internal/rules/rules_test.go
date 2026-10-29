package rules

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

type vector struct {
	Name   string          `json:"name"`
	Kind   string          `json:"kind"`
	Input  json.RawMessage `json:"input"`
	Expect struct {
		OK                bool           `json:"ok"`
		ErrorCode         string         `json:"error_code"`
		Cleaned           map[string]any `json:"cleaned"`
		Stripped          []string       `json:"stripped"`
		InjectionScoreMin *float64       `json:"injection_score_min"`
	} `json:"expect"`
}

// rawBody returns the request body: a JSON string input is the literal body
// (used for malformed JSON), anything else is the body as written.
func rawBody(in json.RawMessage) []byte {
	var s string
	if json.Unmarshal(in, &s) == nil {
		return []byte(s)
	}
	return in
}

func run(kind string, body []byte) (any, Tags, *RuleError) {
	switch kind {
	case "finding":
		return ValidateFinding(body)
	case "reply":
		return ValidateReply(body)
	case "note":
		return ValidateNote(body)
	}
	panic("unknown kind " + kind)
}

func TestVectors(t *testing.T) {
	data, err := os.ReadFile("../../../schema/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vs []vector
	if err := json.Unmarshal(data, &vs); err != nil {
		t.Fatal(err)
	}
	if len(vs) < 40 {
		t.Fatalf("want at least 40 vectors, got %d", len(vs))
	}
	seen := map[string]bool{}
	for _, v := range vs {
		if seen[v.Name] {
			t.Fatalf("duplicate vector name %q", v.Name)
		}
		seen[v.Name] = true
		t.Run(v.Name, func(t *testing.T) {
			msg, tags, rerr := run(v.Kind, rawBody(v.Input))
			if !v.Expect.OK {
				if rerr == nil {
					t.Fatalf("want error %s, got ok", v.Expect.ErrorCode)
				}
				if rerr.Code != v.Expect.ErrorCode {
					t.Fatalf("want error %s, got %s (%s)", v.Expect.ErrorCode, rerr.Code, rerr.Message)
				}
				return
			}
			if rerr != nil {
				t.Fatalf("want ok, got %v", rerr)
			}
			if len(v.Expect.Cleaned) > 0 {
				b, _ := json.Marshal(msg)
				var got map[string]any
				_ = json.Unmarshal(b, &got)
				for k, want := range v.Expect.Cleaned {
					if got[k] != want {
						t.Errorf("cleaned %s:\n got  %q\n want %q", k, got[k], want)
					}
				}
			}
			if v.Expect.Stripped != nil && !slices.Equal(tags.Stripped, v.Expect.Stripped) {
				t.Errorf("stripped: got %v, want %v", tags.Stripped, v.Expect.Stripped)
			}
			if m := v.Expect.InjectionScoreMin; m != nil && tags.InjectionScore < *m {
				t.Errorf("injection_score %.2f < %.2f", tags.InjectionScore, *m)
			}
		})
	}
}

func TestIsBlockedFile(t *testing.T) {
	cases := map[string]bool{
		".gitlab-ci.yml":            true,
		".github/workflows/ci.yml":  true,
		".claude/settings.json":     true,
		".mcp.json":                 true,
		"CLAUDE.md":                 true,
		"docs/claude.md":            true,
		".husky/pre-commit":         true,
		"package.json":              true,
		"web/package.json":          true,
		"package-lock.json":         true,
		"go.mod":                    true,
		"svc/go.sum":                true,
		"yarn.lock":                 true,
		"a/b/Cargo.lock":            true,
		"requirements-dev.txt":      true,
		"py/pyproject.toml":         true,
		"Dockerfile":                true,
		"Dockerfile.prod":           true,
		".aic/policy.json":          true,
		"./.claude/x":               true,
		"pnpm-lock.yaml":            true,
		"web/npm-shrinkwrap.json":   true,
		".npmrc":                    true,
		"src/.claude/settings.json": true,
		"src/discount.js":           false,
		"test/discount.test.js":     false,
		"README.md":                 false,
		"src/.github.js":            false,
		"docs/github/notes.md":      false,
		"package.json.md":           false,
	}
	for p, want := range cases {
		if got := IsBlockedFile(p); got != want {
			t.Errorf("IsBlockedFile(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestInjectionScoreCap(t *testing.T) {
	if s := InjectionScore("hello"); s != 0 {
		t.Fatalf("want 0, got %v", s)
	}
	if s := InjectionScore("ignore previous, system prompt, delete, push --force, curl"); s != 1 {
		t.Fatalf("want 1, got %v", s)
	}
}

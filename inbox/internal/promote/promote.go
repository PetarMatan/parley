// Package promote is the only exit from the quarantine (design 8.1, layer 4).
// It pushes the quarantine worktree's HEAD to the merge request branch after
// fixed checks (contract section 10). The model proposes; this program decides.
package promote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PetarMatan/parley/inbox/internal/rules"
)

// Check names, in contract order.
const (
	CheckCeiling   = "ceiling"
	CheckAncestry  = "ancestry"
	CheckDiffScope = "diff_scope"
	CheckBlocked   = "blocked_files"
	CheckSize      = "patch_size"
	CheckSecrets   = "secrets"
	CheckTests     = "tests"
	CheckPush      = "push"
	CheckNote      = "note"
)

// MinLevel is the authority level the promoter acts at (D14).
const MinLevel = 2

type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Result is the contract section 10 output.
type Result struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
	Pushed string  `json:"pushed,omitempty"`
}

type Options struct {
	Worktree string
	Work     string // $AIC_WORK: mr/, origin.git, logs/, quarantine/<iid>/
	// PolicyFile defaults to PolicyPath(Work, MR). It is never read from the
	// worktree: the session can write there.
	PolicyFile string
	MR         int
	TaskID     string
	Inbox      Inbox

	TestCommand []string      // default: npm test
	TestTimeout time.Duration // default: 5 minutes
	// Sandbox wraps the test command in macOS sandbox-exec with outbound IP
	// traffic denied, when sandbox-exec exists. Tests switch it off.
	Sandbox bool
	Now     func() time.Time
}

type mergeRequest struct {
	IID          int    `json:"iid"`
	Repo         string `json:"repo"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
}

type policy struct {
	Ceiling       map[string]string `json:"ceiling"`
	MaxPatchLines int               `json:"max_patch_lines"`
	TestCommand   []string          `json:"test_command,omitempty"` // overrides the detected test command
}

// Run evaluates every static check, then runs the tests and pushes only when
// all of them pass. Any error fails the check it belongs to (fail closed).
func Run(ctx context.Context, o Options) Result {
	o = withDefaults(o)
	res := run(ctx, o)
	logRun(o, res)
	return res
}

func withDefaults(o Options) Options {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.TestTimeout == 0 {
		o.TestTimeout = 5 * time.Minute
	}
	if o.PolicyFile == "" {
		o.PolicyFile = PolicyPath(o.Work, o.MR)
	}
	return o
}

// PolicyPath is the policy copy make-quarantine.sh writes outside the
// worktree: $AIC_WORK/quarantine/<iid>/policy.json.
func PolicyPath(work string, iid int) string {
	return filepath.Join(work, "quarantine", strconv.Itoa(iid), "policy.json")
}

func run(ctx context.Context, o Options) Result {
	var res Result
	add := func(name string, err error, okDetail string) bool {
		c := Check{Name: name, OK: err == nil, Detail: okDetail}
		if err != nil {
			c.Detail = err.Error()
		}
		res.Checks = append(res.Checks, c)
		return c.OK
	}
	fail := func(name string, err error) Result {
		add(name, err, "")
		return res
	}

	if !rules.TaskIDRe.MatchString(o.TaskID) {
		return fail("input", fmt.Errorf("task id %q is not f-<8 hex>", o.TaskID))
	}
	mr, err := loadMR(o.Work, o.MR)
	if err != nil {
		return fail("input", err)
	}
	contextID := mr.Repo + "!" + strconv.Itoa(mr.IID)
	pol, polErr := loadPolicy(o.PolicyFile)
	if len(o.TestCommand) == 0 {
		o.TestCommand = testCommandFor(o.Worktree, pol)
	}

	// 1. Authority ceiling for the task's class.
	ok := add(CheckCeiling, func() error {
		if polErr != nil {
			return polErr
		}
		if o.Inbox == nil {
			return errors.New("no inbox client")
		}
		class, err := o.Inbox.TaskClass(ctx, contextID, o.TaskID)
		if err != nil {
			return fmt.Errorf("cannot read the task class, refusing: %w", err)
		}
		lvl, err := level(pol.Ceiling[class])
		if err != nil {
			return fmt.Errorf("class %q: %w", class, err)
		}
		if lvl < MinLevel {
			return fmt.Errorf("class %q has ceiling L%d, promotion needs L%d", class, lvl, MinLevel)
		}
		return nil
	}(), "")

	// Fresh branch heads straight from the origin. The sandbox keeps a bare
	// origin under the work dir; elsewhere the worktree's "origin" remote is
	// used, with the checkout's git credential. The refs fetched here are
	// private to the promoter; the worktree's own refs are under the session's
	// control and never trusted.
	origin := filepath.Join(o.Work, "origin.git")
	if _, err := os.Stat(origin); err != nil {
		origin = "origin"
	}
	srcRef, tgtRef := "refs/aic/promote/source", "refs/aic/promote/target"
	_, fetchErr := git(ctx, o.Worktree, "fetch", "-q", "--no-tags", origin,
		"+refs/heads/"+mr.SourceBranch+":"+srcRef, "+refs/heads/"+mr.TargetBranch+":"+tgtRef)
	var head, src, tgt string
	if fetchErr == nil {
		head, fetchErr = git(ctx, o.Worktree, "rev-parse", "--verify", "HEAD^{commit}")
	}
	if fetchErr == nil {
		src, fetchErr = git(ctx, o.Worktree, "rev-parse", "--verify", srcRef+"^{commit}")
	}
	if fetchErr == nil {
		tgt, fetchErr = git(ctx, o.Worktree, "rev-parse", "--verify", tgtRef+"^{commit}")
	}

	// 2. HEAD descends from the source branch head on origin.
	ok = add(CheckAncestry, func() error {
		if fetchErr != nil {
			return fetchErr
		}
		if head == src {
			return errors.New("HEAD equals the source branch head, nothing to promote")
		}
		if _, err := git(ctx, o.Worktree, "merge-base", "--is-ancestor", src, head); err != nil {
			return fmt.Errorf("HEAD %s does not descend from origin/%s %s", short(head), mr.SourceBranch, short(src))
		}
		return nil
	}(), fmt.Sprintf("%s descends from origin/%s %s", short(head), mr.SourceBranch, short(src))) && ok

	var changed []string
	var diffErr error
	if fetchErr != nil {
		diffErr = fetchErr
	} else {
		var out string
		out, diffErr = git(ctx, o.Worktree, "diff", "--name-only", "--no-renames", src, head)
		changed = lines(out)
	}

	// 3. Every changed file is in the MR diff.
	ok = add(CheckDiffScope, func() error {
		if diffErr != nil {
			return diffErr
		}
		out, err := git(ctx, o.Worktree, "diff", "--name-only", "--no-renames", tgt+"..."+src)
		if err != nil {
			return err
		}
		inMR := map[string]bool{}
		for _, f := range lines(out) {
			inMR[f] = true
		}
		var outside []string
		for _, f := range changed {
			if !inMR[f] {
				outside = append(outside, f)
			}
		}
		if len(outside) > 0 {
			return fmt.Errorf("files outside the merge request diff: %s", strings.Join(outside, ", "))
		}
		if len(changed) == 0 {
			return errors.New("no changed files")
		}
		return nil
	}(), fmt.Sprintf("%d changed file(s), all in the merge request diff", len(changed))) && ok

	// 4. No blocked file class.
	ok = add(CheckBlocked, func() error {
		if diffErr != nil {
			return diffErr
		}
		var hit []string
		for _, f := range changed {
			if rules.IsBlockedFile(f) {
				hit = append(hit, f)
			}
		}
		if len(hit) > 0 {
			return fmt.Errorf("blocked files changed: %s", strings.Join(hit, ", "))
		}
		return nil
	}(), "no blocked files") && ok

	// 5. Changed lines within max_patch_lines.
	var nLines int
	ok = add(CheckSize, func() error {
		if polErr != nil {
			return polErr
		}
		if diffErr != nil {
			return diffErr
		}
		out, err := git(ctx, o.Worktree, "diff", "--numstat", "--no-renames", src, head)
		if err != nil {
			return err
		}
		for _, l := range lines(out) {
			f := strings.SplitN(l, "\t", 3)
			if len(f) < 3 || f[0] == "-" {
				return fmt.Errorf("binary change in %s", f[len(f)-1])
			}
			a, _ := strconv.Atoi(f[0])
			d, _ := strconv.Atoi(f[1])
			nLines += a + d
		}
		if nLines > pol.MaxPatchLines {
			return fmt.Errorf("%d changed lines, limit %d", nLines, pol.MaxPatchLines)
		}
		return nil
	}(), fmt.Sprintf("%d changed lines, limit %d", nLines, pol.MaxPatchLines)) && ok // calls run left to right, so nLines is set here

	// 6. No secret pattern in added lines.
	ok = add(CheckSecrets, func() error {
		if diffErr != nil {
			return diffErr
		}
		out, err := git(ctx, o.Worktree, "diff", "-U0", "--no-renames", "--no-color", src, head)
		if err != nil {
			return err
		}
		// "+++ " is a file header only between "diff --git" and the first
		// "@@"; inside a hunk it is an added line whose content starts "++ ".
		file, inHeader := "", false
		for _, l := range lines(out) {
			switch {
			case strings.HasPrefix(l, "diff --git "):
				inHeader = true
			case strings.HasPrefix(l, "@@"):
				inHeader = false
			case inHeader && strings.HasPrefix(l, "+++ "):
				file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
			case !inHeader && strings.HasPrefix(l, "+"):
				// Name the pattern and file only; the secret itself never reaches a log.
				if name := rules.MatchSecret(l[1:]); name != "" {
					return fmt.Errorf("secret pattern %s in an added line of %s", name, file)
				}
			}
		}
		return nil
	}(), "no secret pattern in added lines") && ok

	if !ok {
		add(CheckTests, errors.New("skipped: an earlier check failed"), "")
		add(CheckPush, errors.New("skipped: an earlier check failed"), "")
		return res
	}

	// 7. Tests pass in a clean temporary copy of HEAD.
	if !add(CheckTests, runTests(ctx, o, head), strings.Join(o.TestCommand, " ")+" passed") {
		add(CheckPush, errors.New("skipped: tests failed"), "")
		return res
	}

	// 8. Fast-forward push. No "+" and no --force: origin refuses anything
	// else, also when the branch moved after the ancestry check.
	if _, err := git(ctx, o.Worktree, "push", "-q", "--no-verify", origin, head+":refs/heads/"+mr.SourceBranch); err != nil {
		add(CheckPush, err, "")
		return res
	}
	add(CheckPush, nil, fmt.Sprintf("%s pushed to %s", head, mr.SourceBranch))
	res.OK, res.Pushed = true, head

	// The push stands even when the note fails; the check reports it.
	note := fmt.Sprintf("Promoter pushed %s to %s for task %s after these checks: ceiling, ancestry, diff scope, blocked files, patch size (%d lines), secrets, tests.",
		head, mr.SourceBranch, o.TaskID, nLines)
	add(CheckNote, o.Inbox.PostNote(ctx, contextID, note), "note posted to "+contextID)
	return res
}

func runTests(ctx context.Context, o Options, head string) error {
	dir, err := os.MkdirTemp("", "aic-promote-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	repo, home, tmp := filepath.Join(dir, "repo"), filepath.Join(dir, "home"), filepath.Join(dir, "tmp")
	for _, d := range []string{home, tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	// Push the exact commit into an empty repo: works for a detached HEAD
	// too, and copies nothing untracked from the worktree.
	if _, err := git(ctx, dir, "init", "-q", repo); err != nil {
		return err
	}
	if _, err := git(ctx, o.Worktree, "push", "-q", "--no-verify", repo, head+":refs/heads/aic-test"); err != nil {
		return err
	}
	if _, err := git(ctx, repo, "checkout", "-q", "aic-test"); err != nil {
		return err
	}

	tctx, cancel := context.WithTimeout(ctx, o.TestTimeout)
	defer cancel()
	argv := o.TestCommand
	if o.Sandbox {
		if sb, err := exec.LookPath("sandbox-exec"); err == nil {
			argv = append([]string{sb, "-p", sandboxProfile(dir, o.Work)}, argv...)
		}
	}
	cmd := exec.CommandContext(tctx, argv[0], argv[1:]...)
	cmd.Dir = repo
	cmd.Env = scrubbedEnv(home, tmp)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		// The detail goes back to the model; test code can print anything.
		detail := tail(out.String(), 600)
		if name := rules.MatchSecret(out.String()); name != "" {
			detail = "output withheld: it matches secret pattern " + name
		}
		return fmt.Errorf("%s failed: %v: %s", strings.Join(o.TestCommand, " "), err, detail)
	}
	return nil
}

// sandboxProfile confines the test run, which executes session-written code
// as this user. Rules later in the profile win. Writes are allowed only in
// the run's temp dir and /dev; reads of $HOME and $AIC_WORK are denied, except
// the install trees of PATH entries under $HOME (nvm and similar), which the
// test command needs.
func sandboxProfile(dir, work string) string {
	q := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return strconv.Quote(p)
	}
	var b strings.Builder
	b.WriteString("(version 1)(allow default)(deny network-outbound (remote ip))")
	b.WriteString("(deny file-write*)")
	b.WriteString(`(allow file-write* (subpath ` + q(dir) + `) (literal "/dev/null") (literal "/dev/tty") (regex #"^/dev/fd/"))`)
	home, _ := os.UserHomeDir()
	for _, p := range []string{home, work} {
		if p != "" {
			b.WriteString("(deny file-read* (subpath " + q(p) + "))")
		}
	}
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		parent := filepath.Dir(p)
		inside := func(root string) bool { return root != "" && strings.HasPrefix(parent+"/", root+"/") }
		if filepath.IsAbs(p) && inside(home) && parent != home && !inside(work) {
			b.WriteString("(allow file-read* (subpath " + q(parent) + "))")
		}
	}
	b.WriteString("(allow file-read* (subpath " + q(dir) + "))")
	return b.String()
}

// scrubbedEnv keeps PATH and LANG only. HOME and TMPDIR point at empty temp
// dirs, so no ~/.npmrc, tokens or keys are visible to the test run.
func scrubbedEnv(home, tmp string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TMPDIR=" + tmp, "CI=1",
		"npm_config_offline=true", "npm_config_audit=false", "npm_config_fund=false", "npm_config_update_notifier=false"}
	if l := os.Getenv("LANG"); l != "" {
		env = append(env, "LANG="+l)
	}
	// Go test runs offline too: caches under the temporary home, no toolchain
	// or module download.
	env = append(env, "GOCACHE="+filepath.Join(home, "go-build"), "GOPATH="+filepath.Join(home, "go"),
		"GOMODCACHE="+filepath.Join(home, "go", "pkg", "mod"), "GOTOOLCHAIN=local", "GOPROXY=off", "GOFLAGS=-mod=mod")
	return env
}

// testCommandFor is the policy's test_command, else a command chosen by the
// repository's build files: go.mod → go test ./..., package.json → npm test.
func testCommandFor(worktree string, pol policy) []string {
	if len(pol.TestCommand) > 0 {
		return pol.TestCommand
	}
	if _, err := os.Stat(filepath.Join(worktree, "go.mod")); err == nil {
		return []string{"go", "test", "./..."}
	}
	return []string{"npm", "test"}
}

var levelRe = regexp.MustCompile(`^L([0-9])$`)

func level(s string) (int, error) {
	m := levelRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("no valid ceiling (got %q)", s)
	}
	return int(m[1][0] - '0'), nil
}

func loadMR(work string, iid int) (mergeRequest, error) {
	var mr mergeRequest
	b, err := os.ReadFile(filepath.Join(work, "mr", strconv.Itoa(iid)+".json"))
	if err != nil {
		return mr, fmt.Errorf("merge request: %w", err)
	}
	if err := json.Unmarshal(b, &mr); err != nil {
		return mr, fmt.Errorf("merge request %d: %w", iid, err)
	}
	if mr.IID != iid || mr.Repo == "" || mr.SourceBranch == "" || mr.TargetBranch == "" {
		return mr, fmt.Errorf("merge request %d: missing iid, repo or branch", iid)
	}
	for _, b := range []string{mr.SourceBranch, mr.TargetBranch} {
		if strings.HasPrefix(b, "-") || strings.ContainsAny(b, " :^~?*[\\") || strings.Contains(b, "..") {
			return mr, fmt.Errorf("merge request %d: bad branch name %q", iid, b)
		}
	}
	return mr, nil
}

func loadPolicy(file string) (policy, error) {
	var p policy
	b, err := os.ReadFile(file)
	if err != nil {
		return p, fmt.Errorf("policy: %w", err)
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("policy: %w", err)
	}
	if p.MaxPatchLines <= 0 {
		return p, errors.New("policy: max_patch_lines missing or not positive")
	}
	return p, nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

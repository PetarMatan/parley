package rules

import (
	"path"
	"regexp"
	"strings"
)

// SecretPattern is one secret shape. The promoter reuses these for added lines.
type SecretPattern struct {
	Name string
	Re   *regexp.Regexp
}

// SecretPatterns is the contract section 5 list.
var SecretPatterns = []SecretPattern{
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"aws-access-key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"gitlab-pat", regexp.MustCompile(`glpat-[0-9A-Za-z_-]{20,}`)},
	{"github-pat", regexp.MustCompile(`ghp_[0-9A-Za-z]{36}`)},
	{"anthropic-key", regexp.MustCompile(`sk-ant-[0-9A-Za-z_-]{20,}`)},
	{"jwt", regexp.MustCompile(`eyJ[\w-]+\.eyJ[\w-]+\.[\w-]+`)},
	// The suffix and quote catch SECRET_KEY=…, AWS_SECRET_ACCESS_KEY=… and
	// "password": "…" as well as the bare keyword.
	{"assignment", regexp.MustCompile(`(?i)(password|secret|api[_-]?key|token)[A-Za-z0-9_]*["']?\s*[:=]\s*\S{8,}`)},
	{"url-credential", regexp.MustCompile(`://[^/\s:@]+:[^@\s/]{6,}@`)},
}

// CodeSecretDetected is the error code for a secret match.
const CodeSecretDetected = "secret_detected"

// ScanSecrets returns CodeSecretDetected when text matches any secret pattern,
// otherwise "".
func ScanSecrets(text string) string {
	if MatchSecret(text) != "" {
		return CodeSecretDetected
	}
	return ""
}

// MatchSecret returns the name of the first matching pattern, or "".
func MatchSecret(text string) string {
	for _, p := range SecretPatterns {
		if p.Re.MatchString(text) {
			return p.Name
		}
	}
	return ""
}

// InjectionKeywords are matched case-insensitively as substrings.
var InjectionKeywords = []string{
	"ignore previous", "system prompt", "delete", "push", "force", "curl",
	"wget", ".env", "credentials", "rm -rf", "base64",
}

// InjectionScore is 0.25 per distinct keyword found, capped at 1. It tags a
// message and never blocks it.
func InjectionScore(text string) float64 {
	lower := strings.ToLower(text)
	hits := 0
	for _, k := range InjectionKeywords {
		if strings.Contains(lower, k) {
			hits++
		}
	}
	return min(1.0, float64(hits)*0.25)
}

// BlockedFiles are file classes a finding may not target and a promoted
// change may not touch. A pattern without "/" matches the base name at any
// depth; a pattern with "/" is anchored at the repository root; "**" spans
// directories. Matching ignores case (macOS file systems do).
var BlockedFiles = []string{
	".gitlab-ci.yml", ".github/**", ".claude/**", ".mcp.json", "CLAUDE.md",
	".husky/**", "**/package.json", "**/package-lock.json", "**/go.mod",
	"**/go.sum", "**/*.lock", "**/requirements*.txt", "**/pyproject.toml",
	"Dockerfile*", ".aic/**",
	"**/pnpm-lock.yaml", "**/npm-shrinkwrap.json", "**/.npmrc", "**/.claude/**",
}

var blockedRes = compileGlobs(BlockedFiles)

// IsBlockedFile reports whether a repository-relative path is a blocked file.
func IsBlockedFile(p string) bool {
	p = strings.TrimPrefix(path.Clean(strings.ReplaceAll(p, `\`, "/")), "./")
	p = strings.TrimPrefix(p, "/")
	for _, re := range blockedRes {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

func compileGlobs(globs []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(globs))
	for _, g := range globs {
		out = append(out, regexp.MustCompile(globToRegexp(g)))
	}
	return out
}

// globToRegexp translates a glob to an anchored, case-insensitive regexp.
func globToRegexp(g string) string {
	var b strings.Builder
	b.WriteString("(?i)^")
	if !strings.Contains(g, "/") {
		b.WriteString("(?:.*/)?")
	}
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch {
		case strings.HasPrefix(g[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(g[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return b.String()
}

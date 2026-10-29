// Package rules is the canonical validation and cleaning rule set for aic
// messages (contract section 5). plugin/lib mirrors it; both run the cases in
// schema/vectors.json.
package rules

import (
	"regexp"
	"strings"
)

// Names recorded in tags.stripped, in the order the rules run.
const (
	StripZeroWidth   = "zero-width"
	StripBidi        = "bidi"
	StripUnicodeTag  = "unicode-tag"
	StripBOM         = "bom"
	StripHTMLComment = "html-comment"
	StripImage       = "image"
	StripLink        = "link"
	StripTag         = "tag"
	StripBlankLines  = "blank-lines"
)

// StripOrder is the canonical order of tags.stripped.
var StripOrder = []string{
	StripZeroWidth, StripBidi, StripUnicodeTag, StripBOM,
	StripHTMLComment, StripImage, StripLink, StripTag, StripBlankLines,
}

// hiddenClass returns the stripped name for a rune that must be removed, or "".
func hiddenClass(r rune) string {
	switch {
	case r >= 0x200B && r <= 0x200D, r >= 0x2060 && r <= 0x2064,
		r == 0x00AD, r == 0x180E, r >= 0xFFF9 && r <= 0xFFFB: // soft hyphen, Mongolian vowel separator, interlinear annotation
		return StripZeroWidth
	case r == 0x061C, r == 0x200E, r == 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return StripBidi
	case r >= 0xE0000 && r <= 0xE007F:
		return StripUnicodeTag
	case r == 0xFEFF:
		return StripBOM
	}
	return ""
}

var (
	// An unterminated comment is removed to the end of the text.
	htmlCommentRe = regexp.MustCompile(`(?s)<!--.*?(?:-->|$)`)
	imageRe       = regexp.MustCompile(`!\[[^\]\n]*\]\([^)\n]*\)`)
	linkRe        = regexp.MustCompile(`\[([^\]\n]*)\]\([^)\n]*\)`)
	blankLinesRe  = regexp.MustCompile(`\n(?:[ \t]*\n){3,}`)
)

// FrameNames are tag names that are always removed from a patch, even where
// the patch exemption below would keep them.
var FrameNames = []string{"channel", "system", "system-reminder", "user", "assistant", "human", "instructions"}

// Clean applies the cleaning rules to one text field. It returns the cleaned
// text and the rules that fired, in StripOrder.
//
// For a patch (isPatch) the markdown and blank-line rules are skipped, because
// they would corrupt code and diff text, and a tag-shaped match directly after
// an identifier character (Array<string>, a<b && c>d) is kept unless its name
// is a frame name or contains a hyphen. Closing tags are always removed.
func Clean(text string, isPatch bool) (string, []string) {
	fired := map[string]bool{}

	var b strings.Builder
	for _, r := range text {
		if c := hiddenClass(r); c != "" {
			fired[c] = true
			continue
		}
		b.WriteRune(foldRune(r))
	}
	s := b.String()

	// Repeat until stable: removing one tag can join the halves of another,
	// e.g. "<<b>system-reminder>".
	for i := 0; i < 8; i++ {
		prev := s
		s = replaceFired(htmlCommentRe, s, "", StripHTMLComment, fired)
		if !isPatch {
			s = replaceFired(imageRe, s, "[image removed]", StripImage, fired)
			s = replaceFired(linkRe, s, "$1", StripLink, fired)
		}
		s = stripTags(s, isPatch, fired)
		if s == prev {
			break
		}
	}
	if !isPatch {
		s = replaceFired(blankLinesRe, s, "\n\n\n", StripBlankLines, fired)
	}
	return s, orderStripped(fired)
}

func replaceFired(re *regexp.Regexp, s, repl, name string, fired map[string]bool) string {
	if !re.MatchString(s) {
		return s
	}
	fired[name] = true
	return re.ReplaceAllString(s, repl)
}

// foldRune is the stdlib-only stand-in for NFKC (Go has no NFKC without
// golang.org/x/text). It maps compatibility and look-alike angle brackets to
// ASCII < and >, fullwidth ASCII (U+FF01–U+FF5E) to ASCII, and every Unicode
// space separator (Zs) to ASCII space. plugin/lib/sanitize.mjs applies the
// same table, not String.prototype.normalize, so both stay identical.
func foldRune(r rune) rune {
	switch r {
	case 0xFF1C, 0xFE64, 0x2039, 0x3008, 0x27E8, 0x2329:
		return '<'
	case 0xFF1E, 0xFE65, 0x203A, 0x3009, 0x27E9, 0x232A:
		return '>'
	case 0x00A0, 0x1680, 0x202F, 0x205F, 0x3000:
		return ' '
	}
	switch {
	case r >= 0x2000 && r <= 0x200A:
		return ' '
	case r >= 0xFF01 && r <= 0xFF5E:
		return r - 0xFEE0
	}
	return r
}

// isTagSpace is the separator inside a tag shape: ASCII whitespace including
// newline, plus NEL and the Unicode line and paragraph separators. Zs is
// already folded to ASCII space.
func isTagSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}

func isNameStart(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }

func isNameRune(r rune) bool {
	return isNameStart(r) || (r >= '0' && r <= '9') || r == '_' || r == ':' || r == '.' || r == '-'
}

// tagAt matches a tag shape starting at rs[i] == '<'. Shapes:
//
//	<name>  <name …>  <name/>  </name>          strict
//	< name>  </ name>  < / name …>               tolerant: space after < or /
//	<name …  </name …  (no > before the next <) unterminated: removed to end of line
//
// An unterminated shape needs the name (or /name) right after <: "x < limit"
// in prose is a comparison, not a tag.
//
// Separators are isTagSpace runes, so a newline separates too. It returns the
// end (exclusive), the lower-case name, whether it closes, whether the shape
// is tolerant or unterminated, and ok.
func tagAt(rs []rune, i int) (end int, name string, closing, loose, ok bool) {
	n := len(rs)
	j := i + 1
	for j < n && isTagSpace(rs[j]) {
		j++
		loose = true
	}
	if j < n && rs[j] == '/' {
		closing = true
		j++
		for j < n && isTagSpace(rs[j]) {
			j++
			loose = true
		}
	}
	if j >= n || !isNameStart(rs[j]) {
		return 0, "", false, false, false
	}
	k := j
	for k < n && isNameRune(rs[k]) {
		k++
	}
	name = strings.ToLower(string(rs[j:k]))
	switch {
	case k < n && rs[k] == '>':
		return k + 1, name, closing, loose, true
	case k+1 < n && rs[k] == '/' && rs[k+1] == '>':
		return k + 2, name, closing, loose, true
	case k < n && !isTagSpace(rs[k]):
		return 0, "", false, false, false // e.g. Array<string, f<T>(x), <https://…>
	}
	q := k
	for q < n && rs[q] != '<' && rs[q] != '>' {
		q++
	}
	if q < n && rs[q] == '>' {
		return q + 1, name, closing, loose, true
	}
	if loose {
		return 0, "", false, false, false // spaced and unterminated: a comparison
	}
	// Unterminated: remove to the end of the line the name ends on.
	for end = k; end < n && rs[end] != '\n'; end++ {
	}
	return end, name, closing, true, true
}

// stripTags removes tag shapes. In a patch an opening shape is kept when it
// directly follows an identifier character (Array<string>) or is tolerant or
// unterminated (a < b), unless its name is a frame name or has a hyphen.
// Closing shapes are always removed. After a kept shape the scan resumes at
// the next rune, so a kept shape never hides a tag inside it.
func stripTags(s string, isPatch bool, fired map[string]bool) string {
	if !strings.ContainsRune(s, '<') {
		return s
	}
	rs := []rune(s)
	out := make([]rune, 0, len(rs))
	for i := 0; i < len(rs); {
		if rs[i] != '<' {
			out = append(out, rs[i])
			i++
			continue
		}
		end, name, closing, loose, ok := tagAt(rs, i)
		if !ok {
			out = append(out, rs[i])
			i++
			continue
		}
		afterIdent := i > 0 && rs[i-1] < 0x80 && isIdentByte(byte(rs[i-1]))
		if isPatch && !closing && (afterIdent || loose) && !strings.Contains(name, "-") && !isFrameName(name) {
			out = append(out, rs[i])
			i++
			continue
		}
		fired[StripTag] = true
		i = end
	}
	return string(out)
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c == ')' || c == ']' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isFrameName(name string) bool {
	for _, f := range FrameNames {
		if name == f {
			return true
		}
	}
	return false
}

func orderStripped(fired map[string]bool) []string {
	out := []string{}
	for _, n := range StripOrder {
		if fired[n] {
			out = append(out, n)
		}
	}
	return out
}

// mergeStripped unions stripped lists and returns them in StripOrder.
func mergeStripped(lists ...[]string) []string {
	fired := map[string]bool{}
	for _, l := range lists {
		for _, n := range l {
			fired[n] = true
		}
	}
	return orderStripped(fired)
}

// Package trailer parses the PR-body conventions agents write when they open
// a PR:
//
//	Purpose: one plain-language sentence saying why this PR exists
//	Merge-after: #318            (same repo)
//	Merge-after: owner/repo#318  (another repo)
//	Topic: prod PDB              (groups sibling PRs of one initiative)
//	Hold-until: 2026-10-29 19:00 Europe/Paris
//	Hold: waiting for the vendor  (held with no date)
//
// Topic and Hold lines may also sit in a list bullet or in bold.
package trailer

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	purposeRe    = regexp.MustCompile(`(?mi)^\s*purpose:\s*(.+?)\s*$`)
	mergeAfterRe = regexp.MustCompile(`(?mi)^\s*merge-after:\s*([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)?#(\d+)\s*$`)
	topicRe      = lineRe("topic")
	holdUntilRe  = lineRe("hold-until")
	holdRe       = lineRe("hold")
)

// lineRe matches "Key: value", also as "- Key: value" or "**Key:** value".
func lineRe(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?mi)^\s*(?:[-*+]\s+)?(?:\*\*|__)?` + key + `(?:\*\*|__)?:(?:\*\*|__)?\s*(.+?)\s*$`)
}

func value(re *regexp.Regexp, body string) string {
	if m := re.FindStringSubmatch(body); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// Topic returns the body's Topic: line, or "".
func Topic(body string) string { return value(topicRe, body) }

// Hold returns when the PR may merge (zero: no date, held until the line
// goes) and the hold text, from a Hold-until: or Hold: line. A Hold-until
// value pr-deck cannot read holds with no date, so it fails safe.
func Hold(body string) (until time.Time, reason string, ok bool) {
	if v := value(holdUntilRe, body); v != "" {
		if t, ok := parseWhen(v); ok {
			return t, v, true
		}
		return time.Time{}, v, true
	}
	if v := value(holdRe, body); v != "" {
		return time.Time{}, v, true
	}
	return time.Time{}, "", false
}

// parseWhen reads "2006-01-02[ T15:04][ Zone/Name]", local time by default.
func parseWhen(s string) (time.Time, bool) {
	if len(s) > 10 && s[10] == 'T' {
		s = s[:10] + " " + s[11:]
	}
	f := strings.Fields(s)
	if len(f) == 0 {
		return time.Time{}, false
	}
	date := f[0]
	layout := "2006-01-02"
	f = f[1:]
	if len(f) > 0 && strings.Contains(f[0], ":") {
		date, layout, f = date+" "+f[0], layout+" 15:04", f[1:]
	}
	loc := time.Local
	if len(f) > 0 {
		l, err := time.LoadLocation(f[0])
		if err != nil {
			return time.Time{}, false
		}
		loc = l
	}
	t, err := time.ParseInLocation(layout, date, loc)
	return t, err == nil
}

// Purpose returns the body's Purpose: line, or "".
func Purpose(body string) string {
	m := purposeRe.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// MergeAfter returns the repo (empty for "same repo") and number named by a
// Merge-after: line, and whether one was found.
func MergeAfter(body string) (repo string, number int, ok bool) {
	m := mergeAfterRe.FindStringSubmatch(body)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return m[1], n, true
}

// Package trailer parses the PR-body conventions agents write when they open
// a PR:
//
//	Purpose: one plain-language sentence saying why this PR exists
//	Merge-after: #318            (same repo)
//	Merge-after: owner/repo#318  (another repo)
package trailer

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	purposeRe    = regexp.MustCompile(`(?mi)^\s*purpose:\s*(.+?)\s*$`)
	mergeAfterRe = regexp.MustCompile(`(?mi)^\s*merge-after:\s*([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)?#(\d+)\s*$`)
)

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

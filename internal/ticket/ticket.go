// Package ticket finds Jira keys on PRs and turns ticket summaries into the
// plain-language topics pr-deck leads with. Keys are secondary: people
// remember "Bigger uploads", not "ABC-123".
package ticket

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var keyRe = regexp.MustCompile(`(?i)\b([A-Z][A-Z0-9]{1,9})-(\d{1,6})\b`)

// Extract returns the first ticket key found in branch, then title, then
// body. When projects is non-empty only those project keys count. Keys are
// upper-cased: branch "abc-412-body-size" yields "ABC-412".
func Extract(projects []string, branch, title, body string) string {
	allowed := map[string]bool{}
	for _, p := range projects {
		allowed[strings.ToUpper(p)] = true
	}
	for _, s := range []string{branch, title, body} {
		for _, m := range keyRe.FindAllStringSubmatch(s, -1) {
			proj := strings.ToUpper(m[1])
			if len(allowed) > 0 && !allowed[proj] {
				continue
			}
			if len(allowed) == 0 && !looksLikeKey(m[0]) {
				continue
			}
			if strings.Trim(m[2], "0") == "" { // ABC-0000 is the "no ticket" placeholder
				continue
			}
			return proj + "-" + m[2]
		}
	}
	return ""
}

// looksLikeKey rejects common false positives when no project allow-list is
// configured, such as "utf-8", "sha-256" or "v1-2".
func looksLikeKey(s string) bool {
	low := strings.ToLower(s)
	for _, bad := range []string{"utf-", "sha-", "iso-", "rfc-", "md-", "x-"} {
		if strings.HasPrefix(low, bad) {
			return false
		}
	}
	return true
}

// Short makes the compact topic used in tight spots: an alias when the user
// set one, else the summary's first words cut at maxRunes on a word
// boundary.
func Short(summary, alias string, maxRunes int) string {
	if alias != "" {
		return alias
	}
	s := strings.TrimSpace(summary)
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	words := strings.Fields(s)
	out := ""
	for _, w := range words {
		next := w
		if out != "" {
			next = out + " " + w
		}
		if utf8.RuneCountInString(next) > maxRunes {
			break
		}
		out = next
	}
	if out == "" {
		r := []rune(s)
		return string(r[:maxRunes])
	}
	return strings.TrimRight(out, " ,;:-(")
}

// StripKey removes the ticket key and the separators around it from a PR
// title: "ABC-412 ingress: body size 25m" → "ingress: body size 25m".
func StripKey(title, key string) string {
	t := strings.TrimSpace(title)
	if key != "" {
		re := regexp.MustCompile(`(?i)[\[(]?\b` + regexp.QuoteMeta(key) + `\b[\])]?[:\s-]*`)
		t = strings.TrimSpace(re.ReplaceAllString(t, ""))
	}
	return strings.TrimLeft(t, ":- ")
}

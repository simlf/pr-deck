// Package strip renders pr-deck's two-line status strip for tmux (or any
// status line): global counts, the most relevant eng → prod chain and the
// merge window on line 1; the current session's PR on line 2.
package strip

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/simlf/pr-deck/internal/deck"
	"github.com/simlf/pr-deck/internal/model"
)

// Format of the output.
const (
	Plain = "plain"
	Tmux  = "tmux"
	ANSI  = "ansi"
)

type painter func(color, s string) string

func paint(format string) painter {
	switch format {
	case Tmux:
		return func(c, s string) string {
			if c == "" {
				return s
			}
			return "#[fg=" + c + "]" + strings.ReplaceAll(s, "#", "##") + "#[default]"
		}
	case ANSI:
		codes := map[string]string{"green": "32", "red": "31", "yellow": "33", "magenta": "35", "cyan": "36", "brightblack": "90"}
		return func(c, s string) string {
			if code, ok := codes[c]; ok {
				return "\x1b[" + code + "m" + s + "\x1b[0m"
			}
			return s
		}
	}
	return func(_, s string) string { return s }
}

// Line1 is the global line.
func Line1(v deck.View, format string) string {
	p := paint(format)
	c := v.Count()
	parts := []string{
		p("cyan", "pr-deck"),
		fmt.Sprintf("%d open", c.Open),
		p("green", fmt.Sprintf("●%d", c.Ready)) + " ready",
		p("red", fmt.Sprintf("✗%d", c.Fail)) + " CI",
		p("magenta", fmt.Sprintf("◇%d", c.Waiting)) + " waiting",
	}
	if c.Review > 0 {
		parts = append(parts, p("yellow", fmt.Sprintf("◔%d", c.Review))+" awaiting review")
	}
	if c.Scheduled > 0 {
		parts = append(parts, p("cyan", fmt.Sprintf("◷%d", c.Scheduled))+" scheduled")
	}
	parts = append(parts, p("yellow", fmt.Sprintf("⚑%d", c.Flagged))+" untouched today")
	out := strings.Join(parts, " ")
	if ch := chainSegment(v, p); ch != "" {
		out += " ▏ " + ch
	}
	if w := v.Window(); w.Enabled {
		color := "yellow"
		if w.Open {
			color = "green"
		}
		out += " ▏ " + p(color, w.Describe(v.Now))
	}
	return out
}

// chainSegment shows the most actionable eng → prod chain: one with a
// member that is waiting or ready.
func chainSegment(v deck.View, p painter) string {
	chains := map[string][]model.PR{}
	for _, pr := range v.Snap.PRs {
		if pr.Pair != nil {
			chains[pr.Pair.ID] = append(chains[pr.Pair.ID], pr)
		}
	}
	var ids []string
	for id, prs := range chains {
		live := false
		for _, pr := range prs {
			if !pr.Merged() {
				live = true
			}
		}
		if live {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	sort.Strings(ids)
	prs := chains[ids[0]]
	sort.Slice(prs, func(i, j int) bool { return prs[i].Pair.Order < prs[j].Pair.Order })
	var marks []string
	for _, pr := range prs {
		st := v.StateOf(pr)
		marks = append(marks, p("magenta", deck.OrderMark(pr))+p(colorOf(st), deck.Glyph(st)))
	}
	topic := prs[len(prs)-1]
	return v.TopicShort(topic) + " " + strings.Join(marks, "→")
}

func colorOf(state string) string {
	switch state {
	case deck.StReady, deck.StMerged:
		return "green"
	case deck.StFail, deck.StConflict:
		return "red"
	case deck.StRunning:
		return "yellow"
	case deck.StWaiting:
		return "magenta"
	case deck.StScheduled:
		return "cyan"
	}
	return "brightblack"
}

// Line2 is the "this session" line for the PR whose branch is checked out
// in cwd. It falls back to a scheduled-merge or recall hint.
func Line2(v deck.View, cwd, format string) string {
	p := paint(format)
	if pr, ok := SessionPR(v.Snap, cwd); ok {
		st := v.StateOf(pr)
		return fmt.Sprintf("this session ▸ %s %s %s %s", v.TopicShort(pr), p("brightblack", pr.Ref()),
			p(colorOf(st), deck.Glyph(st)), p("brightblack", v.Next(pr)))
	}
	c := v.Count()
	if c.Scheduled > 0 {
		for _, pr := range v.Snap.PRs {
			if v.StateOf(pr) == deck.StScheduled {
				return p("cyan", "◷ ") + pr.Ref() + " " + p("brightblack", v.Next(pr))
			}
		}
	}
	if c.Flagged > 0 {
		return p("yellow", fmt.Sprintf("⚑ %d PRs untouched today", c.Flagged)) + p("brightblack", " · prd recall")
	}
	return p("green", "✓ nothing needs you")
}

// SessionPR finds the PR for the branch checked out in cwd, falling back to
// the PR an agent created from that directory.
func SessionPR(snap model.Snapshot, cwd string) (model.PR, bool) {
	if cwd == "" {
		return model.PR{}, false
	}
	branch := git(cwd, "symbolic-ref", "--short", "-q", "HEAD")
	repo := repoFromRemote(git(cwd, "config", "--get", "remote.origin.url"))
	if branch != "" && repo != "" {
		for _, pr := range snap.PRs {
			if pr.Branch == branch && strings.EqualFold(pr.Repo, repo) && !pr.Merged() {
				return pr, true
			}
		}
	}
	abs, _ := filepath.Abs(cwd)
	var best model.PR
	found := false
	for _, pr := range snap.PRs {
		if pr.Why != nil && pr.Why.Cwd == abs && !pr.Merged() && (!found || pr.Why.At.After(best.Why.At)) {
			best, found = pr, true
		}
	}
	return best, found
}

func git(dir string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// repoFromRemote turns git@host:owner/repo.git or https://host/owner/repo
// into owner/repo.
func repoFromRemote(u string) string {
	u = strings.TrimSuffix(strings.TrimSpace(u), ".git")
	if u == "" {
		return ""
	}
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		if j := strings.IndexByte(u, '/'); j >= 0 {
			u = u[j+1:]
		}
	} else if i := strings.IndexByte(u, ':'); i >= 0 {
		u = u[i+1:]
	}
	parts := strings.Split(u, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}

package deck

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/simlf/pr-deck/internal/config"
	"github.com/simlf/pr-deck/internal/model"
	"github.com/simlf/pr-deck/internal/review"
	"github.com/simlf/pr-deck/internal/state"
	"github.com/simlf/pr-deck/internal/ticket"
	"github.com/simlf/pr-deck/internal/window"
)

// PR states pr-deck shows, most blocking first.
const (
	StMerged    = "merged"
	StScheduled = "scheduled"
	StHeld      = "held"    // a Hold-until: date not reached yet, or a Hold: line
	StWaiting   = "waiting" // its earlier sibling is not merged
	StDraft     = "draft"
	StFail      = "fail"
	StRunning   = "running"
	StConflict  = "conflict"
	StReview    = "review"  // not approved yet
	StBlocked   = "blocked" // approved and green, but branch protection still blocks
	StReady     = "ready"
)

// View is everything a renderer needs about the current moment.
type View struct {
	Snap  model.Snapshot
	State state.State
	Rules window.Rules
	Cfg   config.Config
	Now   time.Time
}

// Sibling returns the PR that must merge before p, if any.
func (v View) Sibling(p model.PR) (model.PR, bool) {
	if p.Pair == nil || p.Pair.After == "" {
		return model.PR{}, false
	}
	return v.Snap.Find(p.Pair.After)
}

// waitingOn returns the first PR p must merge after that is not merged yet:
// the chain sibling, then any further Merge-after: PR. key is "" when none
// waits; ok is false when pr-deck cannot see that PR.
func (v View) waitingOn(p model.PR) (key string, dep model.PR, ok bool) {
	if p.Pair == nil || p.Pair.After == "" {
		return "", model.PR{}, false
	}
	for _, k := range append([]string{p.Pair.After}, p.Pair.Also...) {
		if dep, ok := v.Snap.Find(k); !ok || !dep.Merged() {
			return k, dep, ok
		}
	}
	return "", model.PR{}, false
}

// StateOf derives p's state.
func (v View) StateOf(p model.PR) string {
	switch {
	case p.Merged():
		return StMerged
	case v.scheduled(p):
		return StScheduled
	case v.Held(p):
		return StHeld
	}
	if key, _, _ := v.waitingOn(p); key != "" {
		return StWaiting
	}
	switch {
	case p.Draft:
		return StDraft
	case p.CI == model.CIFail:
		return StFail
	case p.CI == model.CIRunning:
		return StRunning
	case p.Mergeable == "CONFLICTING":
		return StConflict
	case p.ReviewDecision != model.ReviewApproved:
		return StReview
	case p.MergeState == "BLOCKED" && p.CI == model.CIPass:
		return StBlocked
	}
	return StReady
}

// Held reports whether p's Hold-until: date is still ahead, or p has a
// Hold: line with no date.
func (v View) Held(p model.PR) bool {
	return p.Held && (p.HoldUntil.IsZero() || v.Now.Before(p.HoldUntil))
}

func (v View) holdText(p model.PR) string {
	if p.HoldUntil.IsZero() {
		return "held: " + p.HoldReason
	}
	return "held until " + window.When(v.Now, p.HoldUntil)
}

func (v View) scheduled(p model.PR) bool {
	_, ok := v.State.Schedules[p.Key()]
	return ok
}

// Review is p's review state.
func (v View) Review(p model.PR) review.Info {
	return review.Of(p, v.Cfg.Review.Channel, v.Cfg.Review.RemindAfter.Duration, v.Now)
}

// Critical reports whether p must respect the merge windows.
func (v View) Critical(p model.PR) bool { return Critical(v.Cfg, p) }

// Critical reports whether p matches any merge-window selector.
func Critical(cfg config.Config, p model.PR) bool {
	for _, sel := range cfg.CriticalSelectors() {
		kind, val, _ := strings.Cut(sel, ":")
		switch kind {
		case "env":
			if strings.EqualFold(p.Env, val) {
				return true
			}
		case "label":
			if p.HasLabel(val) {
				return true
			}
		}
	}
	return false
}

// Production reports whether p carries the production label.
func (v View) Production(p model.PR) bool {
	return v.Cfg.ProductionLabel != "" && p.HasLabel(v.Cfg.ProductionLabel)
}

// Touched is the last time the user touched p: a new commit, their own
// comment or review on GitHub, or an action in pr-deck (open, copy, merge, reviewed).
func (v View) Touched(p model.PR) time.Time {
	t := p.LastCommitAt
	if p.AuthorActiveAt.After(t) {
		t = p.AuthorActiveAt
	}
	if x, ok := v.State.Touches[p.Key()]; ok && x.After(t) {
		t = x
	}
	return t
}

// NewComments are p's human comments since the last push or pr-deck action, newest first.
func (v View) NewComments(p model.PR) []model.Comment {
	var out []model.Comment
	since := v.Touched(p)
	for _, c := range p.Comments {
		if c.At.After(since) && !v.bot(c.Author) {
			out = append(out, c)
		}
	}
	return out
}

func (v View) bot(login string) bool {
	if strings.HasSuffix(login, "[bot]") {
		return true
	}
	for _, x := range v.Cfg.GitHub.IgnoreCommenters {
		if strings.EqualFold(x, login) {
			return true
		}
	}
	return false
}

// Flagged is the "needs me" ⚑: p needs the user, can move, and was not
// touched since local midnight. PRs waiting on a sibling, on running CI, on a
// scheduled merge or on reviewers it was posted to (not overdue yet) are skipped.
func (v View) Flagged(p model.PR) bool {
	if p.Sibling {
		return false
	}
	switch st := v.StateOf(p); {
	case st == StMerged, st == StHeld, st == StWaiting, st == StRunning, st == StScheduled:
		return false
	case st == StReview && v.Review(p).Kind == review.Posted:
		return false
	}
	if until, ok := v.State.ReviewedUntil[p.Key()]; ok && v.Now.Before(until) {
		return false
	}
	now := v.Now.Local()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return v.Touched(p).Before(midnight)
}

// Stale reports whether p has been open longer than deck.stale_after.
func (v View) Stale(p model.PR) bool {
	after := v.Cfg.Deck.StaleAfter.Duration
	return after > 0 && !p.CreatedAt.IsZero() && v.Now.Sub(p.CreatedAt) > after
}

// Flaky reports whether every failed check of p is in ci.known_flaky.
func (v View) Flaky(p model.PR) bool {
	if p.CI != model.CIFail || len(p.FailedJobs) == 0 {
		return false
	}
	for _, j := range p.FailedJobs {
		known := false
		for _, k := range v.Cfg.CI.KnownFlaky {
			if strings.EqualFold(j, k) {
				known = true
			}
		}
		if !known {
			return false
		}
	}
	return true
}

// Topic is the plain-language topic, never just a key: the Jira summary,
// else the PR's Purpose: line, else its title without the ticket key. The
// bare key is the very last resort.
func (v View) Topic(p model.PR) string {
	if p.Topic != "" {
		return p.Topic
	}
	if p.Purpose != "" {
		return p.Purpose
	}
	if t := ticket.StripKey(p.Title, p.Ticket); t != "" {
		return t
	}
	if p.Ticket != "" {
		return p.Ticket
	}
	return p.Ref()
}

// Purpose is what p is for: its Purpose: line, else its title without the
// conventional-commit prefix and the ticket key.
func (v View) Purpose(p model.PR) string {
	if p.Purpose != "" {
		return p.Purpose
	}
	return ticket.StripKey(p.Title, p.Ticket)
}

// Ref is p.Ref() with the user's repo alias (deck.repo_aliases), for display.
func (v View) Ref(p model.PR) string {
	name := p.RepoName()
	best := ""
	for alias, full := range v.Cfg.Deck.RepoAliases {
		if (full == name || full == p.Repo) && (best == "" || alias < best) {
			best = alias
		}
	}
	if best != "" {
		name = best
	}
	return fmt.Sprintf("%s#%d", name, p.Number)
}

// Icon is the deck.repo_icons glyph for p's alias, repo name or full repo,
// padded to one cell plus a space; blank when icons are set but not for p.
func (v View) Icon(p model.PR) string {
	icons := v.Cfg.Deck.RepoIcons
	if len(icons) == 0 {
		return ""
	}
	ref := v.Ref(p)
	for _, k := range []string{ref[:strings.LastIndex(ref, "#")], p.RepoName(), p.Repo} {
		if g, ok := icons[k]; ok {
			return runewidth.FillRight(runewidth.Truncate(g, 2, ""), 2) + " "
		}
	}
	return "   "
}

// Snoozed reports whether the user hid p for now (z).
func (v View) Snoozed(p model.PR) bool {
	until, ok := v.State.SnoozedUntil[p.Key()]
	return ok && v.Now.Before(until)
}

// TopicShort is the compact topic: the user's alias (t key) for the ticket
// or PR, else the first words of Topic.
func (v View) TopicShort(p model.PR) string {
	return ticket.Short(v.Topic(p), v.State.Aliases[v.AliasKey(p)], v.Cfg.Jira.ShortLen)
}

// NoGroup is the group key of PRs with neither a ticket nor a Purpose.
const NoGroup = "~none"

// GroupPurpose is the Purpose that groups a ticketless PR: the one of the
// first PR of its Merge-after chain, else its own.
func (v View) GroupPurpose(p model.PR) string {
	if p.Pair != nil {
		best, purpose := 0, ""
		for _, x := range v.Snap.PRs {
			if x.Pair != nil && x.Pair.ID == p.Pair.ID && x.Purpose != "" && (purpose == "" || x.Pair.Order < best) {
				best, purpose = x.Pair.Order, x.Purpose
			}
		}
		if purpose != "" {
			return purpose
		}
	}
	return p.Purpose
}

// GroupKey is the board group of p: its ticket, else its Topic: line, else
// its group Purpose (both trimmed, case-insensitive), else NoGroup.
func (v View) GroupKey(p model.PR) string {
	if p.Ticket != "" {
		return p.Ticket
	}
	if t := strings.TrimSpace(p.TopicLine); t != "" {
		return "~t:" + strings.ToLower(t)
	}
	if pur := strings.TrimSpace(v.GroupPurpose(p)); pur != "" {
		return "~p:" + strings.ToLower(pur)
	}
	return NoGroup
}

// AliasKey is where a topic alias for p is stored: its group, or the PR
// itself when it has no group.
func (v View) AliasKey(p model.PR) string {
	if k := v.GroupKey(p); k != NoGroup {
		return k
	}
	return p.Key()
}

// Window is the merge-window status now.
func (v View) Window() window.Status { return v.Rules.At(v.Now) }

// Next is the one-line next action for p.
func (v View) Next(p model.PR) string {
	s := v.StateOf(p)
	switch s {
	case StMerged:
		return "merged"
	case StScheduled:
		if sch, ok := v.State.Schedules[p.Key()]; ok {
			return "◷ merges " + window.When(v.Now, sch.At) + " (re-checks first) · m cancel"
		}
		return "◷ scheduled · m cancel"
	case StHeld:
		if p.HoldUntil.IsZero() {
			return "‖ " + v.holdText(p) + " · delete the Hold: line to lift"
		}
		return "‖ " + v.holdText(p) + " · m schedule"
	case StWaiting:
		key, sib, ok := v.waitingOn(p)
		switch {
		case !ok:
			return "waits on " + key
		case sib.State == "CLOSED":
			return "waits on " + sib.Ref() + ", closed without merging · fix Merge-after or p to re-pair"
		}
		return "waits on " + orderMark(sib) + " " + sib.Ref()
	case StDraft:
		return "finish draft"
	case StFail:
		if v.Flaky(p) {
			return "known flaky CI · r rerun"
		}
		return "CI failing · r rerun"
	case StRunning:
		return "CI running"
	case StConflict:
		return "merge conflict · rebase"
	case StReview:
		ri := v.Review(p)
		switch ri.Kind {
		case review.Changes:
			return "address review: " + ri.Short
		case review.NotPosted:
			return "post for review · P request"
		case review.Overdue:
			return "nudge reviewers · P copy nudge"
		}
		return "awaiting review"
	case StBlocked:
		return "blocked by branch protection · check required reviews/checks"
	}
	if p.MergeState == "BEHIND" {
		return "behind base · update the branch on GitHub"
	}
	if v.Critical(p) {
		if w := v.Window(); w.Enabled && !w.Open {
			return "merge · ⊘ window " + window.When(v.Now, w.Next)
		}
	}
	if p.Pair != nil && p.Pair.Order > 1 {
		return "merge " + orderMark(p) + " (sibling merged)"
	}
	return "merge"
}

var circled = []string{"", "①", "②", "③", "④", "⑤", "⑥", "⑦", "⑧", "⑨"}

func orderMark(p model.PR) string {
	if p.Pair == nil || p.Pair.Order <= 0 {
		return ""
	}
	if p.Pair.Order < len(circled) {
		return circled[p.Pair.Order]
	}
	return "#"
}

// OrderMark is the circled merge-order digit for p ("" when unpaired).
func OrderMark(p model.PR) string { return orderMark(p) }

// Counts summarises the snapshot for the strip and workspace tokens.
type Counts struct {
	Open, Ready, Fail, Waiting, Review, Flagged, Scheduled, Commented int
}

// Count computes Counts over non-sibling PRs.
func (v View) Count() Counts {
	var c Counts
	for _, p := range v.Snap.PRs {
		if p.Sibling || p.Merged() {
			continue
		}
		c.Open++
		switch v.StateOf(p) {
		case StReady:
			c.Ready++
		case StFail:
			c.Fail++
		case StWaiting:
			c.Waiting++
		case StScheduled:
			c.Scheduled++
		}
		if v.Review(p).Needed() {
			c.Review++
		}
		if v.Flagged(p) {
			c.Flagged++
		}
		if len(v.NewComments(p)) > 0 {
			c.Commented++
		}
	}
	return c
}

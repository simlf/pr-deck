package deck

import (
	"strings"
	"time"

	"prdeck/internal/config"
	"prdeck/internal/model"
	"prdeck/internal/review"
	"prdeck/internal/state"
	"prdeck/internal/ticket"
	"prdeck/internal/window"
)

// PR states pr-deck shows, most blocking first.
const (
	StMerged    = "merged"
	StScheduled = "scheduled"
	StWaiting   = "waiting" // its earlier sibling is not merged
	StDraft     = "draft"
	StFail      = "fail"
	StRunning   = "running"
	StConflict  = "conflict"
	StReview    = "review" // not approved yet
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

// StateOf derives p's state.
func (v View) StateOf(p model.PR) string {
	switch {
	case p.Merged():
		return StMerged
	case v.scheduled(p):
		return StScheduled
	}
	if p.Pair != nil && p.Pair.After != "" {
		if sib, ok := v.Sibling(p); !ok || !sib.Merged() {
			return StWaiting
		}
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
	}
	return StReady
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

// Touched is the last time the user touched p: a new commit, or an action
// in pr-deck (open, copy, merge, reviewed).
func (v View) Touched(p model.PR) time.Time {
	t := p.LastCommitAt
	if x, ok := v.State.Touches[p.Key()]; ok && x.After(t) {
		t = x
	}
	return t
}

// Flagged is the "untouched today" ⚑: p needs the user, can move, and was
// not touched since local midnight. PRs waiting on a sibling, on running CI
// or on a scheduled merge cannot move, so they are skipped.
func (v View) Flagged(p model.PR) bool {
	if p.Sibling {
		return false
	}
	switch v.StateOf(p) {
	case StMerged, StWaiting, StRunning, StScheduled:
		return false
	}
	if until, ok := v.State.ReviewedUntil[p.Key()]; ok && v.Now.Before(until) {
		return false
	}
	now := v.Now.Local()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return v.Touched(p).Before(midnight)
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

// TopicShort is the compact topic: the user's alias (t key) for the ticket
// or PR, else the first words of Topic.
func (v View) TopicShort(p model.PR) string {
	alias := v.State.Aliases[p.Ticket]
	if p.Ticket == "" {
		alias = v.State.Aliases[p.Key()]
	}
	return ticket.Short(v.Topic(p), alias, v.Cfg.Jira.ShortLen)
}

// AliasKey is where a topic alias for p is stored.
func AliasKey(p model.PR) string {
	if p.Ticket != "" {
		return p.Ticket
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
		sch := v.State.Schedules[p.Key()]
		return "◷ merges " + window.When(v.Now, sch.At) + " (re-checks first) · m cancel"
	case StWaiting:
		if sib, ok := v.Sibling(p); ok {
			return "waits on " + orderMark(sib) + " " + sib.Ref()
		}
		return "waits on " + p.Pair.After
	case StDraft:
		return "finish draft"
	case StFail:
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
			return "post for review · P copy request"
		case review.Overdue:
			return "nudge reviewers · P copy nudge"
		}
		return "awaiting review"
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
	Open, Ready, Fail, Waiting, Review, Flagged, Scheduled int
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
	}
	return c
}

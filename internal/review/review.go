// Package review turns GitHub's review decision and the review channel's
// "was it posted?" into the single review state pr-deck shows per PR.
package review

import (
	"fmt"
	"time"

	"prdeck/internal/model"
)

// Kind of review state.
const (
	Approved  = "approved"
	Changes   = "changes"
	Draft     = "draft"
	NotPosted = "not_posted"
	Overdue   = "overdue"
	Posted    = "posted"
	Waiting   = "waiting" // no channel tracking configured
)

// Info is the display form.
type Info struct {
	Kind  string
	Glyph string
	Short string // fits a column: "approved", "6d, no review"
	Long  string // sentence for the why drawer and refusals
}

// Needed reports whether the PR still needs a review push (not posted, or
// posted and overdue).
func (i Info) Needed() bool { return i.Kind == NotPosted || i.Kind == Overdue }

// Of computes the review state at now. channel is the configured review
// channel ("" when tracking is off).
func Of(p model.PR, channel string, remindAfter time.Duration, now time.Time) Info {
	switch {
	case p.Merged() || p.ReviewDecision == model.ReviewApproved:
		who := p.Reviewer
		if who == "" {
			who = "a reviewer"
		}
		return Info{Approved, "✓", "approved", "approved by " + who}
	case p.ReviewDecision == model.ReviewChanges:
		long := "changes requested by " + nonEmpty(p.Reviewer, "a reviewer")
		if p.ReviewNote != "" {
			long += fmt.Sprintf(": %q", p.ReviewNote)
		}
		return Info{Changes, "✎", "changes", long}
	case p.Draft:
		return Info{Draft, "·", "draft", "draft: not up for review yet"}
	case channel == "" || !p.PostedKnown:
		return Info{Waiting, "◌", "no review", "not reviewed yet"}
	case p.Posted == nil:
		return Info{NotPosted, "○", "not posted", "not posted to " + channel + " yet · P copies a review request"}
	}
	age := now.Sub(*p.Posted)
	if age >= remindAfter {
		return Info{Overdue, "◔", Age(age) + ", no review",
			fmt.Sprintf("posted in %s %s ago, still no review (reminder after %s) · P copies a nudge", channel, Age(age), Age(remindAfter))}
	}
	return Info{Posted, "◌", "posted " + Age(age),
		fmt.Sprintf("posted in %s %s ago · reminder at %s", channel, Age(age), Age(remindAfter))}
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// Age renders a duration compactly: 45m, 5h, 6d.
func Age(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

package deck

import (
	"context"
	"fmt"
	"time"

	"github.com/simlf/pr-deck/internal/cache"
	"github.com/simlf/pr-deck/internal/model"
	"github.com/simlf/pr-deck/internal/review"
	"github.com/simlf/pr-deck/internal/state"
	"github.com/simlf/pr-deck/internal/window"
)

// Plan kinds: what pressing m leads to.
const (
	PlanRefuse   = "refuse"
	PlanMerge    = "merge"    // confirm, then merge Target
	PlanSibling  = "sibling"  // confirm, then merge the earlier sibling (Target)
	PlanSchedule = "schedule" // confirm, then schedule Target at At
	PlanCancel   = "cancel"   // confirm, then cancel Target's schedule
	PlanPost     = "post"     // confirm, then post Target's review request
)

// Plan is the outcome of the merge guards for one PR.
type Plan struct {
	Kind    string
	Message string
	Target  model.PR
	At      time.Time
}

// PlanMerge runs the guards in order: already merged → scheduled (offer
// cancel) → pair order (offer the earlier sibling) → CI → draft → conflict →
// review → merge window (offer a schedule) → confirm.
func (v View) PlanMerge(p model.PR) Plan {
	if p.Merged() {
		return Plan{Kind: PlanRefuse, Message: p.Ref() + " is already merged"}
	}
	if sch, ok := v.State.Schedules[p.Key()]; ok {
		return Plan{Kind: PlanCancel, Target: p, Message: fmt.Sprintf("◷ %s is scheduled for %s. Cancel the schedule?", p.Ref(), window.When(v.Now, sch.At))}
	}
	if p.Pair != nil && p.Pair.After != "" {
		sib, ok := v.Sibling(p)
		if !ok {
			return Plan{Kind: PlanRefuse, Message: fmt.Sprintf("%s waits on %s, which pr-deck cannot see", p.Ref(), p.Pair.After)}
		}
		if !sib.Merged() {
			sp := v.PlanMerge(sib)
			head := fmt.Sprintf("◇ %s is %s: %s %s %s is not merged.", p.Ref(), orderMark(p), envOr(sib), orderMark(sib), sib.Ref())
			if sp.Kind == PlanMerge {
				return Plan{Kind: PlanSibling, Target: sib, Message: head + " Merge " + orderMark(sib) + " first?"}
			}
			return Plan{Kind: PlanRefuse, Message: head + " " + sp.Message}
		}
	}
	if r := v.blocker(p); r != "" {
		return Plan{Kind: PlanRefuse, Message: r}
	}
	if v.Critical(p) {
		w := v.Window()
		if w.Enabled && !w.Open {
			if v.Cfg.MergeWindows.OutsideWindow == "schedule" && !w.Next.IsZero() {
				return Plan{Kind: PlanSchedule, Target: p, At: w.Next,
					Message: fmt.Sprintf("⊘ prod merge window closed (%s). Schedule merge of %s for %s? It re-checks CI, approval and pair order first.", w.Reason, p.Ref(), window.When(v.Now, w.Next))}
			}
			return Plan{Kind: PlanRefuse, Message: fmt.Sprintf("⊘ prod merge window closed (%s) · opens %s", w.Reason, window.When(v.Now, w.Next))}
		}
		if w.Enabled {
			return Plan{Kind: PlanMerge, Target: p, Message: fmt.Sprintf("merge %s (%s) with %s? ◉ window open, %s left", p.Ref(), envOr(p), v.Cfg.GitHub.MergeMethod, w.Until.Sub(v.Now).Round(time.Minute))}
		}
	}
	return Plan{Kind: PlanMerge, Target: p, Message: fmt.Sprintf("merge %s%s with %s?", p.Ref(), envSuffix(p), v.Cfg.GitHub.MergeMethod)}
}

// blocker returns why p cannot merge right now, ignoring pairs and windows.
func (v View) blocker(p model.PR) string {
	switch {
	case p.Draft:
		return p.Ref() + " is a draft: mark it ready on GitHub first"
	case p.CI == model.CIFail:
		return "✗ CI failing on " + p.Ref() + ": r to rerun"
	case p.CI == model.CIRunning:
		return "◌ CI still running on " + p.Ref()
	case p.Mergeable == "CONFLICTING":
		return p.Ref() + " has merge conflicts"
	case p.MergeState == "BEHIND":
		return p.Ref() + " is behind its base and GitHub requires an up-to-date branch: update it on GitHub, then merge"
	case p.ReviewDecision != model.ReviewApproved:
		r := v.Review(p)
		return r.Glyph + " can't merge " + p.Ref() + ": " + r.Long
	}
	return ""
}

func envOr(p model.PR) string {
	if p.Env == "" {
		return "sibling"
	}
	return p.Env
}

func envSuffix(p model.PR) string {
	if p.Env == "" {
		return ""
	}
	return " (" + p.Env + ")"
}

// Recheck is the fire-time guard for a scheduled merge, on fresh data:
// CI, approval, pair order, window and mergeability.
func (v View) Recheck(p model.PR) (bool, string) {
	if p.Merged() {
		return false, "already merged"
	}
	if p.Pair != nil && p.Pair.After != "" {
		if sib, ok := v.Sibling(p); !ok || !sib.Merged() {
			return false, "earlier sibling " + p.Pair.After + " is not merged"
		}
	}
	switch {
	case p.CI != model.CIPass:
		return false, "CI is " + p.CI
	case p.ReviewDecision != model.ReviewApproved:
		return false, "no approval"
	case p.Mergeable == "CONFLICTING":
		return false, "merge conflicts"
	case p.MergeState == "BEHIND":
		return false, "branch behind base and GitHub requires it up to date"
	case p.Draft:
		return false, "draft"
	}
	if v.Critical(p) {
		if w := v.Window(); w.Enabled && !w.Open {
			return false, "window closed (" + w.Reason + ")"
		}
	}
	return true, ""
}

// Merge merges p now and records the touch.
func (d *Deck) Merge(ctx context.Context, p model.PR) error {
	if err := d.GH.Merge(ctx, p, d.Cfg.GitHub.MergeMethod); err != nil {
		return err
	}
	_, err := d.Store.Update(func(s *state.State) error {
		delete(s.Schedules, p.Key())
		s.Touches[p.Key()] = d.Now()
		return nil
	})
	return err
}

// Schedule queues p to merge at the next window opening.
func (d *Deck) Schedule(p model.PR, at time.Time) error {
	_, err := d.Store.Update(func(s *state.State) error {
		s.Schedules[p.Key()] = state.Schedule{At: at, Created: d.Now()}
		s.Touches[p.Key()] = d.Now()
		return nil
	})
	return err
}

// Cancel drops p's schedule.
func (d *Deck) Cancel(p model.PR) error {
	_, err := d.Store.Update(func(s *state.State) error {
		delete(s.Schedules, p.Key())
		return nil
	})
	return err
}

// Touch records that the user acted on p (clears the untouched-today ⚑).
func (d *Deck) Touch(key string) error {
	_, err := d.Store.Update(func(s *state.State) error {
		s.Touches[key] = d.Now()
		return nil
	})
	return err
}

// MarkReviewed hides p's ⚑ until tomorrow's midnight.
func (d *Deck) MarkReviewed(key string) error {
	now := d.Now().Local()
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	_, err := d.Store.Update(func(s *state.State) error {
		s.ReviewedUntil[key] = tomorrow
		return nil
	})
	return err
}

// Snooze hides key from the deck until the same time tomorrow; on false it
// shows key again.
func (d *Deck) Snooze(key string, on bool) error {
	_, err := d.Store.Update(func(s *state.State) error {
		if on {
			s.SnoozedUntil[key] = d.Now().Add(24 * time.Hour)
		} else {
			delete(s.SnoozedUntil, key)
		}
		return nil
	})
	return err
}

// SetAlias stores the short topic for an alias key ("" clears it).
func (d *Deck) SetAlias(key, alias string) error {
	_, err := d.Store.Update(func(s *state.State) error {
		if alias == "" {
			delete(s.Aliases, key)
		} else {
			s.Aliases[key] = alias
		}
		return nil
	})
	return err
}

// SetPair records that key must merge after afterKey ("" unpairs).
func (d *Deck) SetPair(key, afterKey string) error {
	_, err := d.Store.Update(func(s *state.State) error {
		s.Pairs[key] = afterKey
		return nil
	})
	return err
}

// PostReview posts text to the review channel unless a fresh search finds
// p already posted there, then records the post in the cached snapshot.
func (d *Deck) PostReview(ctx context.Context, p model.PR, text string) error {
	ch := d.Cfg.Review.Channel
	if d.Slack == nil {
		return fmt.Errorf("posting needs review.channel and a Slack token")
	}
	at, err := d.Slack.FirstPost(ctx, ch, p.URL)
	if err != nil {
		return err
	}
	if at != nil {
		return fmt.Errorf("%s is already posted in %s (%s ago)", p.Ref(), ch, review.Age(d.Now().Sub(*at)))
	}
	if err := d.Slack.Post(ctx, ch, text); err != nil {
		return err
	}
	now := d.Now()
	if snap, ok := d.Cached(); ok {
		for i := range snap.PRs {
			if snap.PRs[i].Key() == p.Key() {
				snap.PRs[i].Posted, snap.PRs[i].PostedKnown, snap.PRs[i].PostedChecked = &now, true, now
			}
		}
		_ = cache.Save(d.SnapshotPath(), snap)
	}
	return d.Touch(p.Key())
}

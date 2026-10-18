package deck

import (
	"context"
	"fmt"
	"strings"
	"time"

	"prdeck/internal/model"
	"prdeck/internal/pairs"
	"prdeck/internal/review"
	"prdeck/internal/state"
	"prdeck/internal/window"
)

// Event is something worth a toast. ID dedupes it across daemon ticks.
type Event struct {
	ID    string
	Title string
	Body  string
}

// Glyph is the one-cell state mark shared by every view.
func Glyph(state string) string {
	switch state {
	case StMerged:
		return "✓"
	case StScheduled:
		return "◷"
	case StWaiting:
		return "◇"
	case StDraft:
		return "◦"
	case StFail, StConflict:
		return "✗"
	case StRunning:
		return "◌"
	case StReview:
		return "○"
	}
	return "●"
}

// Notices lists the toast-worthy facts at v.Now. The daemon sends each ID
// once (state.Once).
func (v View) Notices() []Event {
	var out []Event
	w := v.Window()
	for _, p := range v.Snap.PRs {
		if p.Sibling || p.Merged() {
			continue
		}
		if p.Pair != nil && p.Pair.Order > 1 && v.StateOf(p) == StReady {
			if sib, ok := v.Sibling(p); ok && sib.Merged() {
				body := fmt.Sprintf("%s %s merged → %s %s is unblocked", orderMark(sib), sib.Ref(), orderMark(p), p.Ref())
				if v.Critical(p) && w.Enabled {
					body += " · " + w.Describe(v.Now)
				}
				out = append(out, Event{ID: "unblocked:" + p.Key(), Title: "◆ " + v.TopicShort(p), Body: body})
			}
		}
		if ri := v.Review(p); ri.Kind == review.Overdue && p.Posted != nil {
			out = append(out, Event{
				ID:    fmt.Sprintf("overdue:%s:%d", p.Key(), p.Posted.Unix()),
				Title: "◔ " + p.Ref() + " still has no review",
				Body:  v.TopicShort(p) + " · " + ri.Long,
			})
		}
	}
	if w.Enabled && w.Open {
		out = append(out, Event{ID: "window-open:" + w.Until.Format(time.RFC3339), Title: "◉ prod merge window open", Body: "until " + w.Until.Format("15:04")})
	}
	if at := v.Cfg.Herdr.RecallAt; at != "" {
		if hm, err := time.Parse("15:04", at); err == nil {
			now := v.Now.Local()
			due := time.Date(now.Year(), now.Month(), now.Day(), hm.Hour(), hm.Minute(), 0, 0, now.Location())
			if !now.Before(due) {
				if n := v.Count().Flagged; n > 0 {
					out = append(out, Event{ID: "recall:" + now.Format("2006-01-02"), Title: fmt.Sprintf("⚑ %d PRs untouched today", n), Body: "end-of-day recall: prd recall (R in the board)"})
				}
			}
		}
	}
	return out
}

// FireDue handles scheduled merges at now, always on fresh GitHub data:
// a reminder before the time, then at the time either merge (after
// re-checking CI, approval, pair order, window and mergeability), report a
// skip, or roll a missed window over to the next opening.
func (d *Deck) FireDue(ctx context.Context, rules window.Rules, now time.Time) ([]Event, error) {
	st, err := d.Store.Load()
	if err != nil {
		return nil, err
	}
	var events []Event
	remind := d.Cfg.MergeWindows.RemindBefore.Duration
	for key, sch := range st.Schedules {
		repo, n, ok := splitKey(key)
		if !ok {
			continue
		}
		fresh, err := d.GH.Get(ctx, repo, n)
		if err != nil {
			continue // try again next tick
		}
		d.enrichBasic(&fresh)
		if fresh.Merged() {
			d.updateSchedule(key, nil)
			events = append(events, Event{ID: "sched-gone:" + key, Title: "◷ " + fresh.Ref() + " was already merged", Body: "schedule dropped"})
			continue
		}
		mini := []model.PR{fresh}
		for _, k := range pairs.ReferencedMissing(mini, st.Pairs) {
			if r, num, ok := splitKey(k); ok {
				if sib, err := d.GH.Get(ctx, r, num); err == nil {
					d.enrichBasic(&sib)
					sib.Sibling = true
					mini = append(mini, sib)
				}
			}
		}
		pairs.Link(mini, d.Cfg.Envs, st.Pairs)
		fresh = mini[0]
		v := View{Snap: model.Snapshot{At: now, PRs: mini}, State: st, Rules: rules, Cfg: d.Cfg, Now: now}

		if now.Before(sch.At) {
			if !sch.Reminded && remind > 0 && !now.Before(sch.At.Add(-remind)) {
				sch.Reminded = true
				d.updateSchedule(key, &sch)
				events = append(events, Event{ID: fmt.Sprintf("sched-remind:%s:%d", key, sch.At.Unix()),
					Title: "◷ " + fresh.Ref() + " merges at " + sch.At.Local().Format("15:04"),
					Body:  "scheduled merge in " + sch.At.Sub(now).Round(time.Minute).String() + " · m in pr-deck to cancel"})
			}
			continue
		}
		if w := rules.At(now); v.Critical(fresh) && w.Enabled && !w.Open {
			if d.Cfg.MergeWindows.MissedWindow == "next_window" && !w.Next.IsZero() {
				sch.At, sch.Reminded, sch.LastSkip = w.Next, false, ""
				d.updateSchedule(key, &sch)
				events = append(events, Event{ID: fmt.Sprintf("sched-roll:%s:%d", key, w.Next.Unix()),
					Title: "◷ " + fresh.Ref() + " missed its window",
					Body:  "rolled to " + window.When(now, w.Next)})
			} else {
				d.updateSchedule(key, nil)
				events = append(events, Event{ID: "sched-missed:" + key, Title: "◷ " + fresh.Ref() + " missed its window", Body: "schedule dropped"})
			}
			continue
		}
		if ok, reason := v.Recheck(fresh); !ok {
			if reason != sch.LastSkip {
				sch.LastSkip = reason
				d.updateSchedule(key, &sch)
				events = append(events, Event{ID: fmt.Sprintf("sched-skip:%s:%s", key, reason),
					Title: "◷ scheduled merge skipped · " + fresh.Ref(),
					Body:  reason + " · still scheduled, retried while the window is open"})
			}
			continue
		}
		if err := d.Merge(ctx, fresh); err != nil {
			events = append(events, Event{ID: fmt.Sprintf("sched-fail:%s:%d", key, now.Unix()/600),
				Title: "◷ scheduled merge failed · " + fresh.Ref(), Body: firstLine(err.Error())})
			continue
		}
		events = append(events, Event{ID: "sched-ran:" + key,
			Title: "◷ scheduled merge ran · ✓ merged " + fresh.Ref(),
			Body:  "re-checked CI ✓ approval ✓ pair order ✓ window ✓"})
	}
	return events, nil
}

func (d *Deck) updateSchedule(key string, sch *state.Schedule) {
	_, _ = d.Store.Update(func(s *state.State) error {
		if sch == nil {
			delete(s.Schedules, key)
		} else if _, still := s.Schedules[key]; still {
			s.Schedules[key] = *sch
		}
		return nil
	})
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// AgentPRs returns the PRs an agent created, matched by its session id
// (from herdr) or its working directory (from the transcript).
func (v View) AgentPRs(sessionID, cwd string) []model.PR {
	var out []model.PR
	for _, p := range v.Snap.PRs {
		if p.Why == nil || p.Sibling {
			continue
		}
		if (sessionID != "" && p.Why.SessionID == sessionID) || (sessionID == "" && cwd != "" && p.Why.Cwd == cwd) {
			out = append(out, p)
		}
	}
	return out
}

// Tokens renders a compact PR list for a herdr sidebar token:
// "①#318● ②#319◇ r#88○⚑".
func (v View) Tokens(prs []model.PR, homeRepo string) string {
	var parts []string
	for _, p := range prs {
		s := orderMark(p)
		if p.Repo != homeRepo && homeRepo != "" {
			s += abbrev(p.RepoName())
		}
		s += fmt.Sprintf("#%d%s", p.Number, Glyph(v.StateOf(p)))
		if v.Flagged(p) {
			s += "⚑"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

func abbrev(repo string) string {
	var b strings.Builder
	for _, w := range strings.FieldsFunc(repo, func(r rune) bool { return r == '-' || r == '_' || r == '.' }) {
		b.WriteString(w[:1])
	}
	return b.String()
}

// Package window decides when critical PRs may merge: configured weekdays,
// time slots, holidays, and the day before a holiday.
package window

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"prdeck/internal/config"
)

// Rules is a compiled merge-window configuration.
type Rules struct {
	Days        map[time.Weekday]bool
	Slots       []Slot
	Loc         *time.Location
	Holidays    map[string]bool // YYYY-MM-DD
	BlockHol    bool
	BlockHolEve bool
}

// Slot is a half-open [Start, End) range in minutes after midnight.
type Slot struct{ Start, End int }

// Status describes the window at one instant.
type Status struct {
	Enabled bool
	Open    bool
	Until   time.Time // end of the open slot
	Reason  string    // why closed: "Fri–Sun closed", "lunch break", ...
	Next    time.Time // next opening when closed
}

var dayNames = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// Compile builds Rules from config plus holiday dates (from an ICS feed).
func Compile(c config.MergeWindows, holidays []string) (Rules, error) {
	r := Rules{Days: map[time.Weekday]bool{}, Holidays: map[string]bool{},
		BlockHol: c.BlockHoliday, BlockHolEve: c.BlockDayBeforeHoliday}
	for _, d := range c.Days {
		wd, ok := dayNames[strings.ToLower(strings.TrimSpace(d))[:min(3, len(strings.TrimSpace(d)))]]
		if !ok {
			return r, fmt.Errorf("merge_windows.days: unknown day %q", d)
		}
		r.Days[wd] = true
	}
	for _, s := range c.Slots {
		sl, err := parseSlot(s)
		if err != nil {
			return r, err
		}
		r.Slots = append(r.Slots, sl)
	}
	sort.Slice(r.Slots, func(i, j int) bool { return r.Slots[i].Start < r.Slots[j].Start })
	switch c.Timezone {
	case "", "Local", "local":
		r.Loc = time.Local
	default:
		loc, err := time.LoadLocation(c.Timezone)
		if err != nil {
			return r, fmt.Errorf("merge_windows.timezone: %w", err)
		}
		r.Loc = loc
	}
	for _, h := range append(append([]string(nil), c.Holidays...), holidays...) {
		h = strings.TrimSpace(h)
		if _, err := time.Parse("2006-01-02", h); err != nil {
			return r, fmt.Errorf("holiday %q: want YYYY-MM-DD", h)
		}
		r.Holidays[h] = true
	}
	if len(r.Days) > 0 && len(r.Slots) == 0 {
		return r, fmt.Errorf("merge_windows: days set but no slots")
	}
	return r, nil
}

func parseSlot(s string) (Slot, error) {
	a, b, ok := strings.Cut(strings.TrimSpace(s), "-")
	if !ok {
		return Slot{}, fmt.Errorf("merge_windows.slots: %q is not HH:MM-HH:MM", s)
	}
	st, err := parseHM(a)
	if err != nil {
		return Slot{}, fmt.Errorf("merge_windows.slots: %q: %w", s, err)
	}
	en, err := parseHM(b)
	if err != nil {
		return Slot{}, fmt.Errorf("merge_windows.slots: %q: %w", s, err)
	}
	if en <= st {
		return Slot{}, fmt.Errorf("merge_windows.slots: %q ends before it starts", s)
	}
	return Slot{st, en}, nil
}

func parseHM(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Enabled reports whether any window is configured. Without windows,
// critical PRs merge any time.
func (r Rules) Enabled() bool { return len(r.Days) > 0 }

func (r Rules) dayBlock(d time.Time) string {
	key := d.Format("2006-01-02")
	if r.BlockHol && r.Holidays[key] {
		return "holiday"
	}
	next := d.AddDate(0, 0, 1)
	if r.BlockHolEve && r.Holidays[next.Format("2006-01-02")] {
		return "eve of holiday (" + next.Format("Mon 2 Jan") + ")"
	}
	if !r.Days[d.Weekday()] {
		return closedDays(r.Days)
	}
	return ""
}

// closedDays names the closed weekdays, e.g. "Fri–Sun closed".
func closedDays(open map[time.Weekday]bool) string {
	var closed []time.Weekday
	for d := time.Sunday; d <= time.Saturday; d++ {
		if !open[d] {
			closed = append(closed, d)
		}
	}
	// Rotate so a run starting late in the week reads naturally (Fri–Sun).
	order := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}
	var names []string
	var pos []int
	for i, d := range order {
		for _, c := range closed {
			if c == d {
				names = append(names, d.String()[:3])
				pos = append(pos, i)
			}
		}
	}
	if len(names) == 0 {
		return "closed"
	}
	if len(names) == 1 {
		return names[0] + " closed"
	}
	if pos[len(pos)-1]-pos[0] == len(pos)-1 {
		return names[0] + "–" + names[len(names)-1] + " closed"
	}
	return strings.Join(names, ", ") + " closed"
}

// At evaluates the window at t.
func (r Rules) At(t time.Time) Status {
	if !r.Enabled() {
		return Status{Enabled: false, Open: true}
	}
	t = t.In(r.Loc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, r.Loc)
	mins := t.Hour()*60 + t.Minute()
	block := r.dayBlock(day)
	if block == "" {
		for _, s := range r.Slots {
			if mins >= s.Start && mins < s.End {
				return Status{Enabled: true, Open: true, Until: day.Add(time.Duration(s.End) * time.Minute)}
			}
		}
	}
	reason := block
	if reason == "" {
		switch {
		case mins < r.Slots[0].Start:
			reason = "before hours"
		case mins >= r.Slots[len(r.Slots)-1].End:
			reason = "after hours"
		default:
			reason = "break"
		}
	}
	st := Status{Enabled: true, Reason: reason}
	for i := 0; i < 60; i++ {
		d := day.AddDate(0, 0, i)
		if r.dayBlock(d) != "" {
			continue
		}
		for _, s := range r.Slots {
			if i > 0 || s.Start > mins {
				st.Next = d.Add(time.Duration(s.Start) * time.Minute)
				return st
			}
		}
	}
	return st
}

// Describe is the one-line window state shown in the strip and board.
func (s Status) Describe(now time.Time) string {
	if !s.Enabled {
		return ""
	}
	if s.Open {
		left := s.Until.Sub(now).Round(time.Minute)
		return fmt.Sprintf("◉ prod window until %s (%s left)", s.Until.Format("15:04"), short(left))
	}
	return fmt.Sprintf("⊘ prod window %s · opens %s", s.Reason, When(now, s.Next))
}

// When formats t relative to now: "14:00" today, else "Mon 26 Oct 09:30".
func When(now, t time.Time) string {
	if t.IsZero() {
		return "not soon"
	}
	t = t.In(now.Location())
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("Mon 2 Jan 15:04")
}

func short(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

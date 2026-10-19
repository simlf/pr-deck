package window

import (
	"strings"
	"testing"
	"time"

	"github.com/simlf/pr-deck/internal/config"
)

func rules(t *testing.T) Rules {
	t.Helper()
	c := config.Default().MergeWindows
	c.Days = []string{"mon", "tue", "wed", "thu"}
	c.Slots = []string{"09:30-11:40", "14:00-16:00"}
	c.Timezone = "UTC"
	r, err := Compile(c, []string{"2026-11-11"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func at(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, time.UTC)
}

func TestOpenSlot(t *testing.T) {
	s := rules(t).At(at(2026, 10, 20, 11, 2)) // Tue
	if !s.Open || s.Until != at(2026, 10, 20, 11, 40) {
		t.Fatalf("want open until 11:40, got %+v", s)
	}
	if got := s.Describe(at(2026, 10, 20, 11, 2)); !strings.Contains(got, "38m left") {
		t.Fatalf("Describe = %q", got)
	}
}

func TestLunchBreak(t *testing.T) {
	s := rules(t).At(at(2026, 10, 20, 12, 15))
	if s.Open || s.Reason != "break" || s.Next != at(2026, 10, 20, 14, 0) {
		t.Fatalf("got %+v", s)
	}
}

func TestFridayRollsToMonday(t *testing.T) {
	s := rules(t).At(at(2026, 10, 23, 10, 0)) // Fri
	if s.Open || s.Reason != "Fri–Sun closed" || s.Next != at(2026, 10, 26, 9, 30) {
		t.Fatalf("got %+v", s)
	}
}

func TestEveOfHolidayAndHoliday(t *testing.T) {
	s := rules(t).At(at(2026, 11, 10, 10, 15)) // Tue before Wed 11 Nov holiday
	if s.Open || !strings.HasPrefix(s.Reason, "eve of holiday") {
		t.Fatalf("eve: %+v", s)
	}
	if s.Next != at(2026, 11, 12, 9, 30) {
		t.Fatalf("next after holiday = %v", s.Next)
	}
	h := rules(t).At(at(2026, 11, 11, 10, 0))
	if h.Open || h.Reason != "holiday" {
		t.Fatalf("holiday: %+v", h)
	}
}

func TestDisabled(t *testing.T) {
	r, err := Compile(config.Default().MergeWindows, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s := r.At(time.Now()); !s.Open || s.Enabled {
		t.Fatalf("no days configured must mean always open, got %+v", s)
	}
}

func TestCompileErrors(t *testing.T) {
	c := config.Default().MergeWindows
	c.Days = []string{"mon"}
	if _, err := Compile(c, nil); err == nil {
		t.Fatal("days without slots must fail")
	}
	c.Slots = []string{"11:00-10:00"}
	if _, err := Compile(c, nil); err == nil {
		t.Fatal("inverted slot must fail")
	}
	c.Slots = []string{"09:00-10:00"}
	c.Days = []string{"funday"}
	if _, err := Compile(c, nil); err == nil {
		t.Fatal("unknown day must fail")
	}
}

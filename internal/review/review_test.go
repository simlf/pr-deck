package review

import (
	"strings"
	"testing"
	"time"

	"github.com/simlf/pr-deck/internal/model"
)

func TestOf(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	posted := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	cases := []struct {
		name string
		pr   model.PR
		ch   string
		kind string
	}{
		{"approved", model.PR{ReviewDecision: model.ReviewApproved, Reviewer: "@dana"}, "#team-reviews", Approved},
		{"merged counts as approved", model.PR{State: "MERGED"}, "#team-reviews", Approved},
		{"changes", model.PR{ReviewDecision: model.ReviewChanges, Reviewer: "@lee", ReviewNote: "lower burst"}, "#team-reviews", Changes},
		{"draft", model.PR{Draft: true}, "#team-reviews", Draft},
		{"no tracking", model.PR{}, "", Waiting},
		{"unknown posted", model.PR{PostedKnown: false}, "#team-reviews", Waiting},
		{"not posted", model.PR{PostedKnown: true}, "#team-reviews", NotPosted},
		{"posted recently", model.PR{PostedKnown: true, Posted: posted(2 * time.Hour)}, "#team-reviews", Posted},
		{"overdue", model.PR{PostedKnown: true, Posted: posted(6 * 24 * time.Hour)}, "#team-reviews", Overdue},
	}
	for _, c := range cases {
		got := Of(c.pr, c.ch, 5*time.Hour, now)
		if got.Kind != c.kind {
			t.Errorf("%s: kind = %s, want %s", c.name, got.Kind, c.kind)
		}
	}
	o := Of(model.PR{PostedKnown: true, Posted: posted(6 * 24 * time.Hour)}, "#team-reviews", 5*time.Hour, now)
	if o.Short != "6d, no review" || !strings.Contains(o.Long, "#team-reviews 6d ago") || !o.Needed() {
		t.Fatalf("overdue display = %+v", o)
	}
	c := Of(model.PR{ReviewDecision: model.ReviewChanges, Reviewer: "@lee", ReviewNote: "lower burst"}, "", 5*time.Hour, now)
	if c.Long != `changes requested by @lee: "lower burst"` {
		t.Fatalf("changes long = %q", c.Long)
	}
}

package state

import (
	"sync"
	"testing"
	"time"
)

func TestUpdateRoundTrip(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	_, err := st.Update(func(s *State) error {
		s.Schedules["a/r#1"] = Schedule{At: now.Add(time.Hour), Created: now}
		s.Aliases["ABC-1"] = "Bigger uploads"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Schedules["a/r#1"].At.Equal(now.Add(time.Hour)) || s.Aliases["ABC-1"] != "Bigger uploads" {
		t.Fatalf("round trip lost data: %+v", s)
	}
}

func TestConcurrentUpdates(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st.Update(func(s *State) error {
				s.Touches[string(rune('a'+i))] = time.Now()
				return nil
			})
		}(i)
	}
	wg.Wait()
	s, _ := st.Load()
	if len(s.Touches) != 20 {
		t.Fatalf("lost updates: %d of 20", len(s.Touches))
	}
}

func TestOnceAndPrune(t *testing.T) {
	var s State
	s.init()
	now := time.Now()
	if !s.Once("x", now) || s.Once("x", now) {
		t.Fatal("Once must be true only the first time")
	}
	s.Prune(now.Add(48*time.Hour), 24*time.Hour, nil)
	if !s.Once("x", now) {
		t.Fatal("pruned mark should allow the event again")
	}
}

func TestPruneTouchesAndPairs(t *testing.T) {
	var s State
	s.init()
	now := time.Now()
	s.Touches["old"], s.Touches["new"] = now.Add(-48*time.Hour), now
	s.Pairs["a/r#2"], s.Pairs["a/r#9"] = "a/r#1", ""
	s.Prune(now, 24*time.Hour, nil)
	if _, ok := s.Touches["old"]; ok || len(s.Touches) != 1 || len(s.Pairs) != 2 {
		t.Fatalf("touches=%v pairs=%v", s.Touches, s.Pairs)
	}
	s.Prune(now, 24*time.Hour, map[string]bool{"a/r#2": true})
	if len(s.Pairs) != 1 || s.Pairs["a/r#2"] != "a/r#1" {
		t.Fatalf("pairs of PRs gone from the snapshot must drop: %v", s.Pairs)
	}
}

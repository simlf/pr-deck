// Package state keeps pr-deck's small local state: scheduled merges,
// "reviewed" marks, snoozes, topic aliases, manual pairs, touches and which
// reminders were already sent. The TUI and the daemon share it, so every
// write goes through Update, which holds an exclusive file lock.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Schedule is a merge queued for the next window opening.
type Schedule struct {
	At       time.Time `json:"at"`
	Created  time.Time `json:"created"`
	Reminded bool      `json:"reminded,omitempty"`
	LastSkip string    `json:"last_skip,omitempty"`
}

// State is the persisted document.
type State struct {
	Schedules     map[string]Schedule  `json:"schedules"`
	ReviewedUntil map[string]time.Time `json:"reviewed_until"`
	Aliases       map[string]string    `json:"aliases"`
	Pairs         map[string]string    `json:"pairs"`
	Touches       map[string]time.Time `json:"touches"`
	Notified      map[string]time.Time `json:"notified"`
	SnoozedUntil  map[string]time.Time `json:"snoozed_until"`
}

func (s *State) init() {
	if s.Schedules == nil {
		s.Schedules = map[string]Schedule{}
	}
	if s.ReviewedUntil == nil {
		s.ReviewedUntil = map[string]time.Time{}
	}
	if s.Aliases == nil {
		s.Aliases = map[string]string{}
	}
	if s.Pairs == nil {
		s.Pairs = map[string]string{}
	}
	if s.Touches == nil {
		s.Touches = map[string]time.Time{}
	}
	if s.Notified == nil {
		s.Notified = map[string]time.Time{}
	}
	if s.SnoozedUntil == nil {
		s.SnoozedUntil = map[string]time.Time{}
	}
}

// Store is a state file in a directory.
type Store struct{ Dir string }

func (st Store) path() string { return filepath.Join(st.Dir, "state.json") }

// Load reads the state; a missing file is an empty state.
func (st Store) Load() (State, error) {
	var s State
	b, err := os.ReadFile(st.path())
	if errors.Is(err, os.ErrNotExist) {
		s.init()
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	s.init()
	return s, nil
}

// Update applies fn under an exclusive lock and writes the result
// atomically. fn's error aborts the write.
func (st Store) Update(fn func(*State) error) (State, error) {
	if err := os.MkdirAll(st.Dir, 0o700); err != nil {
		return State{}, err
	}
	lock, err := os.OpenFile(filepath.Join(st.Dir, "state.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return State{}, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return State{}, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	s, err := st.Load()
	if err != nil {
		return s, err
	}
	if err := fn(&s); err != nil {
		return s, err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return s, err
	}
	tmp, err := os.CreateTemp(st.Dir, "state-*.json")
	if err != nil {
		return s, err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return s, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return s, err
	}
	return s, os.Rename(tmp.Name(), st.path())
}

// Once records event id and reports whether this is its first occurrence.
// Used so a reminder toast fires once, not on every daemon tick.
func (s *State) Once(id string, now time.Time) bool {
	if _, ok := s.Notified[id]; ok {
		return false
	}
	s.Notified[id] = now
	return true
}

// Prune drops notification marks older than keep, so the file stays small.
func (s *State) Prune(now time.Time, keep time.Duration) {
	for k, t := range s.Notified {
		if now.Sub(t) > keep {
			delete(s.Notified, k)
		}
	}
	for k, t := range s.ReviewedUntil {
		if now.After(t) {
			delete(s.ReviewedUntil, k)
		}
	}
	for k, t := range s.SnoozedUntil {
		if now.After(t) {
			delete(s.SnoozedUntil, k)
		}
	}
}

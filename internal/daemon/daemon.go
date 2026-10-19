// Package daemon is pr-deck's background loop: refresh the snapshot, fire
// scheduled merges, send each reminder once, and publish herdr sidebar
// tokens so every agent row shows the PRs it opened.
package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/simlf/pr-deck/internal/deck"
	"github.com/simlf/pr-deck/internal/herdr"
	"github.com/simlf/pr-deck/internal/model"
	"github.com/simlf/pr-deck/internal/state"
)

// Host is the herdr surface the daemon publishes to.
type Host interface {
	Agents() ([]herdr.Agent, error)
	PaneTokens(paneID, source string, tokens map[string]*string, ttl time.Duration) error
	WorkspaceTokens(workspaceID, source string, tokens map[string]*string, ttl time.Duration) error
}

// Notifier shows a toast.
type Notifier interface {
	Notify(title, body string) error
}

// Daemon runs ticks.
type Daemon struct {
	Deck   *deck.Deck
	Host   Host     // nil outside herdr
	Notify Notifier // nil: events are only logged
	Log    io.Writer

	panes map[string]bool // panes we set tokens on, to clear them later
}

func (d *Daemon) logf(format string, a ...any) {
	if d.Log != nil {
		fmt.Fprintf(d.Log, time.Now().Format("15:04:05 ")+format+"\n", a...)
	}
}

// PokePath is the file `prd poke` touches to ask the daemon for an early
// tick (herdr's agent-status event hook uses it after an agent goes idle).
func PokePath(cacheDir string) string { return filepath.Join(cacheDir, "poke") }

// Poke asks a running daemon to tick soon.
func Poke(cacheDir string) error {
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return err
	}
	now := time.Now()
	f, err := os.OpenFile(PokePath(cacheDir), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	return os.Chtimes(PokePath(cacheDir), now, now)
}

// Lock takes the single-instance daemon lock; ok is false when another
// daemon already holds it. Keep the file open for the daemon's lifetime.
func Lock(cacheDir string) (*os.File, bool, error) {
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(filepath.Join(cacheDir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false, nil
	}
	return f, true, nil
}

// Running reports whether a daemon currently holds the lock in cacheDir.
func Running(cacheDir string) bool {
	f, ok, err := Lock(cacheDir)
	if err != nil {
		return false
	}
	if ok {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return false
	}
	return true
}

// Run ticks every refresh interval, or sooner after a poke (at most every
// 10 seconds), until ctx ends.
func (d *Daemon) Run(ctx context.Context) error {
	every := d.Deck.Cfg.Deck.Refresh.Duration
	if every < 15*time.Second {
		every = 15 * time.Second
	}
	poke := PokePath(d.Deck.Cfg.Deck.CacheDir)
	var lastPoke time.Time
	if st, err := os.Stat(poke); err == nil {
		lastPoke = st.ModTime()
	}
	check := time.NewTicker(2 * time.Second)
	defer check.Stop()
	next := time.Now()
	last := time.Time{}
	for {
		if !time.Now().Before(next) {
			if err := d.Tick(ctx); err != nil {
				d.logf("tick: %v", err)
			}
			last = time.Now()
			next = last.Add(every)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-check.C:
			if st, err := os.Stat(poke); err == nil && st.ModTime().After(lastPoke) {
				lastPoke = st.ModTime()
				if soon := last.Add(10 * time.Second); soon.Before(next) {
					next = soon
				}
			}
		}
	}
}

// Tick does one full pass.
func (d *Daemon) Tick(ctx context.Context) error {
	now := d.Deck.Now()
	snap, err := d.Deck.Refresh(ctx)
	if err != nil {
		cached, ok := d.Deck.Cached()
		if !ok {
			return err
		}
		d.logf("refresh failed, using cache: %v", err)
		snap = cached
	}
	for _, e := range snap.Errors {
		d.logf("source: %s", e)
	}
	rules, err := d.Deck.Rules(ctx)
	if err != nil {
		return err
	}
	fired, err := d.Deck.FireDue(ctx, rules, now)
	if err != nil {
		d.logf("scheduled merges: %v", err)
	}
	for _, e := range fired {
		if strings.HasPrefix(e.ID, "sched-ran:") {
			if s, err := d.Deck.Refresh(ctx); err == nil {
				snap = s
			}
			break
		}
	}
	st, err := d.Deck.Store.Load()
	if err != nil {
		return err
	}
	v := deck.View{Snap: snap, State: st, Rules: rules, Cfg: d.Deck.Cfg, Now: now}
	d.send(append(fired, v.Notices()...), now)
	if d.Host != nil {
		if err := d.publish(v); err != nil {
			d.logf("herdr tokens: %v", err)
		}
	}
	return nil
}

// send delivers each event once, recording it in state first so a crash
// never repeats a toast.
func (d *Daemon) send(events []deck.Event, now time.Time) {
	var fresh []deck.Event
	_, _ = d.Deck.Store.Update(func(s *state.State) error {
		for _, e := range events {
			if s.Once(e.ID, now) {
				fresh = append(fresh, e)
			}
		}
		s.Prune(now, 14*24*time.Hour)
		return nil
	})
	for _, e := range fresh {
		d.logf("event: %s · %s", e.Title, e.Body)
		if d.Notify != nil {
			if err := d.Notify.Notify(e.Title, e.Body); err != nil {
				d.logf("notify: %v", err)
			}
		}
	}
}

func (d *Daemon) publish(v deck.View) error {
	agents, err := d.Host.Agents()
	if err != nil {
		return err
	}
	src := d.Deck.Cfg.Herdr.Source
	ttl := 3 * d.Deck.Cfg.Deck.Refresh.Duration
	if d.panes == nil {
		d.panes = map[string]bool{}
	}
	seen := map[string]bool{}
	workspaces := map[string]bool{}
	for _, a := range agents {
		workspaces[a.WorkspaceID] = true
		prs := v.AgentPRs(a.SessionID(), a.Dir())
		if len(prs) == 0 {
			continue
		}
		seen[a.PaneID] = true
		d.panes[a.PaneID] = true
		tok := v.Tokens(prs, homeRepo(prs))
		if err := d.Host.PaneTokens(a.PaneID, src, map[string]*string{"pr": herdr.Str(tok)}, ttl); err != nil {
			d.logf("pane %s: %v", a.PaneID, err)
		}
	}
	for pane := range d.panes {
		if !seen[pane] {
			_ = d.Host.PaneTokens(pane, src, map[string]*string{"pr": nil}, ttl)
			delete(d.panes, pane)
		}
	}
	c := v.Count()
	counts := fmt.Sprintf("%d open ●%d ✗%d ◇%d ◔%d ⚑%d", c.Open, c.Ready, c.Fail, c.Waiting, c.Review, c.Flagged)
	toks := map[string]*string{"prdeck": herdr.Str(counts), "prwin": nil}
	if w := v.Window(); w.Enabled {
		toks["prwin"] = herdr.Str(w.Describe(v.Now))
	}
	if c.Scheduled > 0 {
		toks["prsched"] = herdr.Str(fmt.Sprintf("◷%d scheduled", c.Scheduled))
	} else {
		toks["prsched"] = nil
	}
	for ws := range workspaces {
		if err := d.Host.WorkspaceTokens(ws, src, toks, ttl); err != nil {
			d.logf("workspace %s: %v", ws, err)
		}
	}
	return nil
}

func homeRepo(prs []model.PR) string {
	n := map[string]int{}
	best := ""
	for _, p := range prs {
		n[p.Repo]++
		if n[p.Repo] > n[best] {
			best = p.Repo
		}
	}
	return best
}

// HerdrNotifier shows toasts through herdr.
type HerdrNotifier struct {
	Client   *herdr.Client
	Position string
}

// Notify implements Notifier.
func (h HerdrNotifier) Notify(title, body string) error {
	return h.Client.Notify(title, body, h.Position)
}

// CmdNotifier runs a user command with {title} and {body} substituted, for
// hosts other than herdr (e.g. terminal-notifier, notify-send).
type CmdNotifier struct{ Cmd []string }

// Notify implements Notifier.
func (c CmdNotifier) Notify(title, body string) error {
	if len(c.Cmd) == 0 {
		return nil
	}
	args := make([]string, len(c.Cmd))
	for i, a := range c.Cmd {
		args[i] = strings.NewReplacer("{title}", title, "{body}", body).Replace(a)
	}
	return exec.Command(args[0], args[1:]...).Run()
}

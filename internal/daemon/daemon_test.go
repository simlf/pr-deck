package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/simlf/pr-deck/internal/config"
	"github.com/simlf/pr-deck/internal/deck"
	"github.com/simlf/pr-deck/internal/herdr"
	"github.com/simlf/pr-deck/internal/model"
	"github.com/simlf/pr-deck/internal/state"
)

type fakeGH struct{ prs map[string]model.PR }

func (f *fakeGH) SearchOpen(context.Context) ([]model.PR, error) {
	var out []model.PR
	for _, p := range f.prs {
		if p.State == "OPEN" {
			out = append(out, p)
		}
	}
	return out, nil
}
func (f *fakeGH) Get(_ context.Context, r string, n int) (model.PR, error) {
	p, ok := f.prs[model.Key(r, n)]
	if !ok {
		return p, fmt.Errorf("nf")
	}
	return p, nil
}
func (f *fakeGH) Merge(_ context.Context, p model.PR, _ string) error {
	x := f.prs[p.Key()]
	x.State = "MERGED"
	f.prs[p.Key()] = x
	return nil
}
func (f *fakeGH) RerunFailed(context.Context, model.PR) (int, error) { return 0, nil }

type fakeHost struct {
	mu   sync.Mutex
	pane map[string]*string
	ws   map[string]*string
}

func (h *fakeHost) Agents() ([]herdr.Agent, error) {
	return []herdr.Agent{{PaneID: "w1:p1", WorkspaceID: "w1", Cwd: "/w/infra"}}, nil
}
func (h *fakeHost) PaneTokens(_ string, _ string, t map[string]*string, _ time.Duration) error {
	h.mu.Lock()
	h.pane = t
	h.mu.Unlock()
	return nil
}
func (h *fakeHost) WorkspaceTokens(_ string, _ string, t map[string]*string, _ time.Duration) error {
	h.mu.Lock()
	h.ws = t
	h.mu.Unlock()
	return nil
}

type recorder struct{ titles []string }

func (r *recorder) Notify(title, _ string) error { r.titles = append(r.titles, title); return nil }

func TestTickPublishesAndNotifiesOnce(t *testing.T) {
	now := time.Date(2026, 10, 20, 11, 2, 0, 0, time.Local)
	cfg := config.Default()
	cfg.Deck.StateDir, cfg.Deck.CacheDir = t.TempDir(), t.TempDir()
	cfg.GitHub.WebHost = "github.example"
	cfg.MergeWindows.AppliesTo = []string{"env:prod"}
	cfg.MergeWindows.Days = []string{"tue"}
	cfg.MergeWindows.Slots = []string{"09:30-11:40"}
	tr := t.TempDir()
	os.MkdirAll(filepath.Join(tr, "p"), 0o700)
	os.WriteFile(filepath.Join(tr, "p", "s.jsonl"), []byte(strings.Join([]string{
		`{"type":"user","message":{"content":"do the eng then prod pair"},"cwd":"/w/infra","sessionId":"","timestamp":"2026-10-15T14:00:00Z"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","input":{"command":"gh pr create"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"https://github.example/acme/infra-config/pull/319"}]},"cwd":"/w/infra"}`,
	}, "\n")+"\n"), 0o600)
	cfg.Why.TranscriptsDir = tr
	gh := &fakeGH{prs: map[string]model.PR{
		"acme/infra-config#318": {Repo: "acme/infra-config", Number: 318, State: "MERGED", Branch: "x-eng"},
		"acme/infra-config#319": {Repo: "acme/infra-config", Number: 319, State: "OPEN", Branch: "x-prod", Body: "Merge-after: #318",
			CI: model.CIPass, ReviewDecision: model.ReviewApproved, Mergeable: "MERGEABLE", LastCommitAt: now},
	}}
	dk := &deck.Deck{Cfg: cfg, GH: gh, Store: state.Store{Dir: cfg.Deck.StateDir}, Now: func() time.Time { return now }}
	host := &fakeHost{}
	rec := &recorder{}
	d := &Daemon{Deck: dk, Host: host, Notify: rec}
	if err := d.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if host.pane == nil || *host.pane["pr"] != "②#319●" {
		t.Fatalf("pane tokens = %v", host.pane)
	}
	if host.ws["prwin"] == nil || !strings.Contains(*host.ws["prwin"], "◉ prod window") {
		t.Fatalf("workspace tokens = %v", host.ws)
	}
	n := len(rec.titles)
	if n == 0 {
		t.Fatal("expected unblocked/window notices")
	}
	d.Tick(context.Background())
	if len(rec.titles) != n {
		t.Fatalf("notices must be sent once, got %v", rec.titles)
	}
}

func TestCmdNotifierSubstitutes(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	c := CmdNotifier{Cmd: []string{"sh", "-c", "printf '%s|%s' \"$0\" \"$1\" > " + out, "{title}", "{body}"}}
	if err := c.Notify("T", "B"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if string(b) != "T|B" {
		t.Fatalf("got %q", b)
	}
}

func TestLockIsExclusiveAndPoke(t *testing.T) {
	dir := t.TempDir()
	f, ok, err := Lock(dir)
	if err != nil || !ok {
		t.Fatalf("first lock: %v %v", ok, err)
	}
	if _, ok2, _ := Lock(dir); ok2 {
		t.Fatal("second daemon must not get the lock")
	}
	if !Running(dir) {
		t.Fatal("Running should see the held lock")
	}
	f.Close()
	if Running(dir) {
		t.Fatal("no daemon after close")
	}
	if _, ok3, _ := Lock(dir); !ok3 {
		t.Fatal("lock should be free after close")
	}
	if err := Poke(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(PokePath(dir)); err != nil {
		t.Fatal(err)
	}
}

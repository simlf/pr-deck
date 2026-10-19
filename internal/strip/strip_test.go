package strip

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/simlf/pr-deck/internal/config"
	"github.com/simlf/pr-deck/internal/deck"
	"github.com/simlf/pr-deck/internal/model"
	"github.com/simlf/pr-deck/internal/pairs"
	"github.com/simlf/pr-deck/internal/state"
	"github.com/simlf/pr-deck/internal/window"
)

func testView(t *testing.T) deck.View {
	t.Helper()
	cfg := config.Default()
	cfg.MergeWindows.AppliesTo = []string{"env:prod"}
	cfg.MergeWindows.Days = []string{"mon", "tue", "wed", "thu"}
	cfg.MergeWindows.Slots = []string{"09:30-11:40", "14:00-16:00"}
	rules, err := window.Compile(cfg.MergeWindows, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.Local) // Friday
	old := now.Add(-48 * time.Hour)
	prs := []model.PR{
		{Repo: "acme/infra-config", Number: 318, State: "OPEN", Env: "eng", CI: model.CIPass, ReviewDecision: model.ReviewApproved,
			Branch: "abc-412-body-size-eng", Ticket: "ABC-412", Topic: "Allow bigger uploads (ingress 25 MB)", LastCommitAt: old},
		{Repo: "acme/infra-config", Number: 319, State: "OPEN", Env: "prod", CI: model.CIPass, ReviewDecision: model.ReviewApproved,
			Branch: "abc-412-body-size-prod", Ticket: "ABC-412", Topic: "Allow bigger uploads (ingress 25 MB)", Body: "Merge-after: #318", LastCommitAt: old},
		{Repo: "acme/api-gateway", Number: 1042, State: "OPEN", CI: model.CIFail, Branch: "abc-398-rate-limit", LastCommitAt: now},
	}
	pairs.Link(prs, cfg.Envs, nil)
	var st state.State
	st, _ = state.Store{Dir: t.TempDir()}.Load()
	return deck.View{Snap: model.Snapshot{PRs: prs}, State: st, Rules: rules, Cfg: cfg, Now: now}
}

func TestLine1(t *testing.T) {
	got := Line1(testView(t), Plain)
	for _, want := range []string{"3 open", "●1 ready", "✗1 CI", "◇1 waiting", "⚑1 untouched today",
		"Allow bigger uploads ①●→②◇", "⊘ prod window Fri–Sun closed · opens Mon 26 Oct 09:30"} {
		if !strings.Contains(got, want) {
			t.Errorf("line 1 missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ABC-412") {
		t.Errorf("strip must lead with topics, not keys: %s", got)
	}
}

func TestTmuxEscapesHash(t *testing.T) {
	got := Line1(testView(t), Tmux)
	if !strings.Contains(got, "#[fg=green]●1#[default]") {
		t.Fatalf("tmux colors missing: %s", got)
	}
}

func TestLine2SessionPR(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "abc-398-rate-limit")
	run("remote", "add", "origin", "git@github.example:acme/api-gateway.git")
	got := Line2(testView(t), dir, Plain)
	if !strings.Contains(got, "this session ▸") || !strings.Contains(got, "api-gateway#1042 ✗") {
		t.Fatalf("line 2 = %q", got)
	}
}

func TestRepoFromRemote(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.example:acme/api-gateway.git":    "acme/api-gateway",
		"https://github.example/acme/infra-config":   "acme/infra-config",
		"ssh://git@github.example/acme/runbooks.git": "acme/runbooks",
		"": "",
	} {
		if got := repoFromRemote(in); got != want {
			t.Errorf("repoFromRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

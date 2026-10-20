package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/simlf/pr-deck/internal/cache"
	"github.com/simlf/pr-deck/internal/config"
	"github.com/simlf/pr-deck/internal/deck"
	"github.com/simlf/pr-deck/internal/model"
	"github.com/simlf/pr-deck/internal/state"
)

type fakeGH struct {
	prs    map[string]model.PR
	merged []string
}

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
	f.merged = append(f.merged, p.Key())
	return nil
}
func (f *fakeGH) RerunFailed(context.Context, model.PR) (int, error)    { return 1, nil }
func (f *fakeGH) Plans(context.Context, model.PR) ([]model.Plan, error) { return nil, nil }

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

var now = time.Date(2026, 10, 23, 10, 0, 0, 0, time.Local) // Friday

func fixture(t *testing.T) (*Model, *fakeGH) {
	t.Helper()
	cfg := config.Default()
	cfg.Deck.StateDir, cfg.Deck.CacheDir = t.TempDir(), t.TempDir()
	cfg.Review.Channel = "#team-reviews"
	cfg.ProductionLabel = "prod-change"
	cfg.MergeWindows.AppliesTo = []string{"env:prod"}
	cfg.MergeWindows.Days = []string{"mon", "tue", "wed", "thu"}
	cfg.MergeWindows.Slots = []string{"09:30-11:40", "14:00-16:00"}
	old := now.Add(-48 * time.Hour)
	posted := now.Add(-6 * 24 * time.Hour)
	mk := func(p model.PR) model.PR {
		p.State, p.Mergeable, p.LastCommitAt, p.CreatedAt = "OPEN", "MERGEABLE", old, old
		if p.CI == "" {
			p.CI = model.CIPass
		}
		p.URL = "https://github.example/" + p.Repo + fmt.Sprintf("/pull/%d", p.Number)
		return p
	}
	topic := "Allow bigger uploads (ingress 25 MB)"
	prs := []model.PR{
		mk(model.PR{Repo: "acme/infra-config", Number: 318, Env: "eng", Ticket: "ABC-412", Topic: topic, TicketStatus: "In Progress",
			ReviewDecision: model.ReviewApproved, Reviewer: "@dana", Branch: "abc-412-body-size-eng", Purpose: "Bigger uploads on eng first.",
			Why: &model.Why{Prompt: "bump the ingress body size to 25m, eng first", Cwd: "/w/infra", At: old}}),
		mk(model.PR{Repo: "acme/infra-config", Number: 319, Env: "prod", Ticket: "ABC-412", Topic: topic, TicketStatus: "In Progress",
			ReviewDecision: model.ReviewApproved, Reviewer: "@dana", Branch: "abc-412-body-size-prod", Body: "Merge-after: #318",
			Labels: []string{"prod-change"}, Purpose: "Same limit for prod."}),
		mk(model.PR{Repo: "acme/runbooks", Number: 88, Ticket: "ABC-415", Topic: "Renew redis TLS certs before 1 Nov",
			ReviewDecision: model.ReviewRequired, PostedKnown: true, Posted: &posted, Purpose: "Write the rotation steps down."}),
		mk(model.PR{Repo: "acme/api-gateway", Number: 1042, Ticket: "ABC-398", Topic: "Rate-limit /v2/search", CI: model.CIFail,
			ReviewDecision: model.ReviewChanges, Reviewer: "@lee", ReviewNote: "lower burst to 20"}),
		mk(model.PR{Repo: "acme/terraform-modules", Number: 203, Purpose: "AWS provider bump", ReviewDecision: model.ReviewRequired, PostedKnown: true}),
	}
	gh := &fakeGH{prs: map[string]model.PR{}}
	for _, p := range prs {
		gh.prs[p.Key()] = p
	}
	dk := &deck.Deck{Cfg: cfg, GH: gh, Store: state.Store{Dir: cfg.Deck.StateDir}, Now: func() time.Time { return now }}
	if err := cache.Save(dk.SnapshotPath(), model.Snapshot{At: now, PRs: prs}); err != nil {
		t.Fatal(err)
	}
	m := New(dk, Options{})
	m.width, m.height = 140, 40
	return m, gh
}

func press(m *Model, keys ...string) tea.Cmd {
	var last tea.Cmd
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		_, last = m.Update(msg)
	}
	return last
}

func run(m *Model, cmd tea.Cmd) {
	for cmd != nil {
		msg := cmd()
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				run(m, c)
			}
			return
		}
		_, cmd = m.Update(msg)
	}
}

func TestBoardTopicsFirst(t *testing.T) {
	m, _ := fixture(t)
	out := plain(m.View())
	for _, want := range []string{
		"▾ Allow bigger uploads (ingress 25 MB) ABC-412 · In Progress",
		"┌① ● infra-config#318",
		"└② ◇ infra-config#319 ▲",
		"waits ①",
		"✎ changes",
		"◔ 6d, no review",
		"○ not posted",
		"▾ AWS provider bump no ticket",
		"⊘ prod window Fri–Sun closed · opens Mon 26 Oct 09:30",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("board missing %q\n%s", want, out)
		}
	}
	if i, j := strings.Index(out, "Allow bigger uploads"), strings.Index(out, "ABC-412"); i < 0 || j < i {
		t.Errorf("topic must come before the key")
	}
}

func TestMergeFlowPairThenSchedule(t *testing.T) {
	m, gh := fixture(t)
	m.cursor = "acme/infra-config#319"
	press(m, "m")
	if m.mode != modeConfirm || m.plan.Kind != deck.PlanSibling || !strings.Contains(m.plan.Message, "Merge ① first?") {
		t.Fatalf("pair guard: mode=%v plan=%+v", m.mode, m.plan)
	}
	if !strings.Contains(plain(m.View()), "[y]es [n]o") {
		t.Fatal("confirm prompt not shown")
	}
	run(m, press(m, "y"))
	if len(gh.merged) != 1 || gh.merged[0] != "acme/infra-config#318" {
		t.Fatalf("merged = %v", gh.merged)
	}
	m.cursor = "acme/infra-config#319"
	press(m, "m")
	if m.plan.Kind != deck.PlanSchedule {
		t.Fatalf("Friday prod merge should offer a schedule: %+v (msg %q)", m.plan, m.msg)
	}
	press(m, "y")
	st, _ := m.dk.Store.Load()
	if _, ok := st.Schedules["acme/infra-config#319"]; !ok {
		t.Fatal("schedule not stored")
	}
	if !strings.Contains(plain(m.View()), "◷ Mon 09:30") {
		t.Fatalf("scheduled state not shown:\n%s", plain(m.View()))
	}
	press(m, "m")
	if m.plan.Kind != deck.PlanCancel {
		t.Fatalf("m on a scheduled PR offers cancel: %+v", m.plan)
	}
	press(m, "y")
	if st, _ := m.dk.Store.Load(); len(st.Schedules) != 0 {
		t.Fatal("cancel did not remove the schedule")
	}
}

func TestCoalescedRunes(t *testing.T) {
	m, _ := fixture(t)
	first := m.cursor
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jj")})
	ks := m.keys()
	if m.cursor == first || m.cursor != ks[2] {
		t.Fatalf("\"jj\" in one message should move twice: %q (keys %v)", m.cursor, ks)
	}
}

func TestRefusalsAndFlags(t *testing.T) {
	m, _ := fixture(t)
	m.cursor = "acme/runbooks#88"
	press(m, "m")
	if m.mode != modeNormal || !strings.Contains(m.msg, "posted in #team-reviews 6d ago") {
		t.Fatalf("unreviewed merge must be refused with the reason, msg=%q", m.msg)
	}
	before := m.view.Count().Flagged
	press(m, "f")
	if m.view.Count().Flagged != before-1 || !strings.Contains(m.msg, "until tomorrow") {
		t.Fatalf("f should clear the flag: %d→%d %q", before, m.view.Count().Flagged, m.msg)
	}
}

func TestRecallLayoutAndDrawer(t *testing.T) {
	m, _ := fixture(t)
	m.cursor = "acme/infra-config#318"
	press(m, "R")
	out := plain(m.View())
	for _, want := range []string{"PR · purpose", "topic", "Allow bigger uploads", "↳ you asked: \"bump the ingress body size",
		"pair ①● eng #318 → ②◇ prod #319", "· Merge-after trailer", "Bigger uploads on eng first."} {
		if !strings.Contains(out, want) {
			t.Errorf("recall missing %q\n%s", want, out)
		}
	}
	press(m, "s")
	if !strings.Contains(plain(m.View()), "sort: age") {
		t.Fatal("s should cycle the sort")
	}
}

func TestTopicAliasAndFilter(t *testing.T) {
	m, _ := fixture(t)
	m.cursor = "acme/infra-config#318"
	press(m, "t")
	for range []rune(m.view.TopicShort(m.view.Snap.PRs[0])) {
		press(m, "backspace")
	}
	press(m, "U", "p", "l", "o", "a", "d", "s", "enter")
	p, _ := m.selected()
	if got := m.view.TopicShort(p); got != "Uploads" {
		t.Fatalf("alias = %q", got)
	}
	press(m, "/", "r", "u", "n", "enter")
	if ks := m.keys(); len(ks) != 1 || ks[0] != "acme/runbooks#88" {
		t.Fatalf("filter keys = %v", ks)
	}
	press(m, "esc")
	if m.filter != "" {
		t.Fatal("esc should clear the filter")
	}
}

func TestMouseSelectAndFold(t *testing.T) {
	m, _ := fixture(t)
	m.View()
	target := -1
	for i, ln := range m.lines {
		if ln.key == "acme/api-gateway#1042" {
			target = i
		}
	}
	if target < 0 {
		t.Fatal("row not rendered")
	}
	m.Update(tea.MouseMsg{X: 5, Y: m.bodyTop + target - m.offset, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.cursor != "acme/api-gateway#1042" {
		t.Fatalf("click selected %q", m.cursor)
	}
	for i, ln := range m.lines {
		if ln.group == "ABC-412" {
			m.Update(tea.MouseMsg{X: 0, Y: m.bodyTop + i - m.offset, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		}
	}
	if !m.collapsed["ABC-412"] || !strings.Contains(plain(m.View()), "▸ Allow bigger uploads") {
		t.Fatal("clicking a group header should fold it")
	}
}

func TestNarrowWidthStillRenders(t *testing.T) {
	m, _ := fixture(t)
	m.width, m.height = 60, 20
	out := plain(m.View())
	if !strings.Contains(out, "Allow bigger uploads") || !strings.Contains(out, "infra-config#318") {
		t.Fatalf("narrow layout lost content:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if w := len([]rune(l)); w > 80 {
			t.Errorf("line too wide (%d): %q", w, l)
		}
	}
}

func TestPurposeGroups(t *testing.T) {
	m, gh := fixture(t)
	add := func(p model.PR) {
		p.State, p.Mergeable, p.CI, p.LastCommitAt = "OPEN", "MERGEABLE", model.CIPass, now
		m.view.Snap.PRs = append(m.view.Snap.PRs, p)
		gh.prs[p.Key()] = p
	}
	add(model.PR{Repo: "acme/infra-live", Number: 7, Purpose: "aws provider bump  ", ReviewDecision: model.ReviewRequired})
	add(model.PR{Repo: "acme/dns", Number: 1, Purpose: "Move the MX records."})
	add(model.PR{Repo: "acme/dns", Number: 2, Purpose: "Then drop the old ones.", Body: "Merge-after: #1"})
	add(model.PR{Repo: "acme/misc", Number: 5, Title: "chore: tidy"})
	m.recompute()
	gs := m.groups()
	byTopic := map[string]group{}
	for _, g := range gs {
		byTopic[g.topic] = g
	}
	if g := byTopic["AWS provider bump"]; len(g.prs) != 2 {
		t.Errorf("same Purpose should share a group: %+v", g.prs)
	}
	if g := byTopic["Move the MX records."]; len(g.prs) != 2 {
		t.Errorf("a Merge-after chain stays in its first PR's Purpose group: %+v", g.prs)
	}
	if last := gs[len(gs)-1]; last.topic != "No ticket" || len(last.prs) != 1 {
		t.Errorf("No ticket group must come last with the PR without a Purpose: %+v", last)
	}
	if !strings.Contains(plain(m.View()), "▾ Move the MX records. no ticket") {
		t.Errorf("purpose header missing:\n%s", plain(m.View()))
	}
	m.cursor = "acme/dns#2"
	press(m, "t")
	m.input = []rune("MX")
	press(m, "enter")
	if p, _ := m.view.Snap.Find("acme/dns#1"); m.view.TopicShort(p) != "MX" {
		t.Errorf("t alias should cover the whole Purpose group, got %q", m.view.TopicShort(p))
	}
	p, _ := m.view.Snap.Find("acme/infra-live#7")
	if out := plain(strings.Join(m.why(p, 120, false), "\n")); strings.Contains(out, "why ") {
		t.Errorf("drawer repeats the Purpose already in the topic line:\n%s", out)
	}
}

func TestPlanMarkAndDrawer(t *testing.T) {
	m, _ := fixture(t)
	m.dk.Cfg.Deck.OpenCmd = []string{"true"}
	key := "acme/infra-config#318"
	for i := range m.view.Snap.PRs {
		if m.view.Snap.PRs[i].Key() == key {
			m.view.Snap.PRs[i].Plans = []model.Plan{
				{Workspace: "eng", Add: 1, Destroy: 2, URL: "https://github.example/c/1"},
				{Workspace: "modules/dns", Summary: "Plan generated.", URL: "https://github.example/c/2"},
			}
		}
	}
	m.cursor = key
	out := plain(m.View())
	for _, want := range []string{"Δ 2 ws merge", "Δ eng +1 ~0 -2", "Δ modules/dns Plan generated."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n%s", want, out)
		}
	}
	press(m, "tab", "tab")
	if !strings.Contains(plain(m.View()), "▸ Δ modules/dns") {
		t.Fatalf("tab should select the second plan line:\n%s", plain(m.View()))
	}
	press(m, "enter")
	if !strings.Contains(m.msg, "https://github.example/c/2") {
		t.Fatalf("enter should open the selected plan comment, msg=%q", m.msg)
	}
	press(m, "tab")
	press(m, "enter")
	if !strings.Contains(m.msg, "/pull/318") {
		t.Fatalf("after the last plan line enter opens the PR again, msg=%q", m.msg)
	}
}

func TestRerunShowsRerunningUntilNewRuns(t *testing.T) {
	m, gh := fixture(t)
	rerunEvery = 0
	t.Cleanup(func() { rerunEvery = 5 * time.Second })
	key := "acme/api-gateway#1042"
	m.cursor = key
	done := press(m, "r")()
	_, next := m.Update(done)
	if out := plain(m.View()); !strings.Contains(out, "⟳ CI rerun") || !strings.Contains(out, "⟳ CI rerunning") {
		t.Fatalf("a rerun PR still read as failed must show it is rerunning:\n%s", out)
	}
	press(m, "r")
	if !strings.Contains(m.msg, "already rerunning") {
		t.Fatalf("r twice must not rerun again, msg=%q", m.msg)
	}
	p := gh.prs[key]
	p.CI = model.CIRunning
	gh.prs[key] = p
	run(m, next)
	if m.rerunning[key] {
		t.Fatal("the rerunning mark must clear once the new runs appear")
	}
	if got, _ := m.view.Snap.Find(key); got.CI != model.CIRunning || !strings.Contains(plain(m.View()), "CI ◌") {
		t.Fatalf("the PR should now show running CI: %+v", got.CI)
	}
}

type fakeSlack struct{ posts []string }

func (f *fakeSlack) FirstPost(context.Context, string, string) (*time.Time, error) { return nil, nil }
func (f *fakeSlack) Post(_ context.Context, ch, text string) error {
	f.posts = append(f.posts, ch+": "+text)
	return nil
}

func TestPostReviewRequest(t *testing.T) {
	m, _ := fixture(t)
	fs := &fakeSlack{}
	m.dk.Slack = fs
	m.cursor = "acme/terraform-modules#203"
	press(m, "P")
	if m.mode != modeConfirm || m.plan.Kind != deck.PlanPost || !strings.Contains(m.plan.Message, "to #team-reviews?") {
		t.Fatalf("P must ask before posting: mode=%v plan=%+v", m.mode, m.plan)
	}
	run(m, press(m, "y"))
	if len(fs.posts) != 1 || !strings.HasPrefix(fs.posts[0], "#team-reviews: Review please: AWS provider bump") {
		t.Fatalf("posts = %v (msg %q)", fs.posts, m.msg)
	}
	m.cursor = "acme/runbooks#88"
	m.dk.Cfg.Deck.CopyCmd = []string{"true"}
	press(m, "P")
	if m.mode != modeNormal || len(fs.posts) != 1 || !strings.Contains(m.msg, "already posted") {
		t.Fatalf("an already posted PR must not be posted again: posts=%v msg=%q", fs.posts, m.msg)
	}
}

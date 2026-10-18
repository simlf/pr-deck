package deck

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"prdeck/internal/config"
	"prdeck/internal/model"
	"prdeck/internal/sources/jira"
	"prdeck/internal/state"
	"prdeck/internal/window"
)

// ---- fakes ----

type fakeGH struct {
	prs    map[string]model.PR
	merged []string
	reruns []string
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

func (f *fakeGH) Get(_ context.Context, repo string, n int) (model.PR, error) {
	p, ok := f.prs[model.Key(repo, n)]
	if !ok {
		return p, fmt.Errorf("not found")
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

func (f *fakeGH) RerunFailed(_ context.Context, p model.PR) (int, error) {
	f.reruns = append(f.reruns, p.Key())
	return 1, nil
}

type fakeJira map[string]jira.Ticket

func (f fakeJira) Get(_ context.Context, k string) (jira.Ticket, error) {
	t, ok := f[k]
	if !ok {
		return t, fmt.Errorf("no %s", k)
	}
	return t, nil
}
func (f fakeJira) BrowseURL(k string) string { return "https://jira.example/browse/" + k }

type fakeSlack map[string]time.Time

func (f fakeSlack) FirstPost(_ context.Context, _, url string) (*time.Time, error) {
	if t, ok := f[url]; ok {
		return &t, nil
	}
	return nil, nil
}

// ---- fixture: the eng/prod pair from the design review ----

var (
	tue1102 = time.Date(2026, 10, 20, 11, 2, 0, 0, time.Local)
	fri1000 = time.Date(2026, 10, 23, 10, 0, 0, 0, time.Local)
	mon0930 = time.Date(2026, 10, 26, 9, 30, 0, 0, time.Local)
)

func scenario() *fakeGH {
	day := time.Date(2026, 10, 19, 15, 0, 0, 0, time.Local)
	mk := func(repo string, n int, branch, body string, mut func(*model.PR)) model.PR {
		p := model.PR{Repo: "acme/" + repo, Number: n, Branch: branch, Body: body, State: "OPEN", CI: model.CIPass,
			Mergeable: "MERGEABLE", ReviewDecision: model.ReviewApproved, Reviewer: "@dana",
			URL: fmt.Sprintf("https://github.example/acme/%s/pull/%d", repo, n), LastCommitAt: day, Title: branch}
		if mut != nil {
			mut(&p)
		}
		return p
	}
	prs := []model.PR{
		mk("infra-config", 318, "ops-412-body-size-eng", "Purpose: Bigger uploads on eng first.", nil),
		mk("infra-config", 319, "ops-412-body-size-prod", "Purpose: Same limit for prod.\nMerge-after: #318", func(p *model.PR) {
			p.Labels = []string{"prod-change"}
		}),
		mk("runbooks", 88, "ops-415-runbook", "", func(p *model.PR) { p.ReviewDecision = model.ReviewRequired; p.Reviewer = "" }),
		mk("terraform-modules", 203, "bump-aws-provider", "Purpose: AWS provider bump.", func(p *model.PR) {
			p.ReviewDecision = model.ReviewRequired
		}),
	}
	f := &fakeGH{prs: map[string]model.PR{}}
	for _, p := range prs {
		f.prs[p.Key()] = p
	}
	return f
}

func testCfg(t *testing.T) config.Config {
	c := config.Default()
	c.Deck.StateDir = t.TempDir()
	c.Deck.CacheDir = t.TempDir()
	c.Why.TranscriptsDir = filepath.Join(t.TempDir(), "none")
	c.GitHub.WebHost = "github.example"
	c.Review.Channel = "#team-reviews"
	c.ProductionLabel = "prod-change"
	c.MergeWindows.AppliesTo = []string{"env:prod"}
	c.MergeWindows.Days = []string{"mon", "tue", "wed", "thu"}
	c.MergeWindows.Slots = []string{"09:30-11:40", "14:00-16:00"}
	c.MergeWindows.Holidays = []string{"2026-11-11"}
	return c
}

func newDeck(t *testing.T, gh *fakeGH, now time.Time) *Deck {
	cfg := testCfg(t)
	return &Deck{
		Cfg: cfg, GH: gh, Store: state.Store{Dir: cfg.Deck.StateDir}, Now: func() time.Time { return now },
		Jira: fakeJira{
			"OPS-412": {Key: "OPS-412", Summary: "Allow bigger uploads (ingress 25 MB)", Status: "In Progress"},
			"OPS-415": {Key: "OPS-415", Summary: "Renew redis TLS certs before 1 Nov", Status: "In Progress"},
		},
		Slack: fakeSlack{"https://github.example/acme/runbooks/pull/88": now.Add(-6 * 24 * time.Hour)},
	}
}

func view(t *testing.T, d *Deck, now time.Time) View {
	t.Helper()
	snap, err := d.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st, _ := d.Store.Load()
	r, err := d.Rules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return View{Snap: snap, State: st, Rules: r, Cfg: d.Cfg, Now: now}
}

func get(t *testing.T, v View, key string) model.PR {
	t.Helper()
	p, ok := v.Snap.Find(key)
	if !ok {
		t.Fatalf("%s not in snapshot", key)
	}
	return p
}

// ---- tests ----

func TestRefreshEnriches(t *testing.T) {
	gh := scenario()
	d := newDeck(t, gh, tue1102)
	v := view(t, d, tue1102)
	prod := get(t, v, "acme/infra-config#319")
	if prod.Ticket != "OPS-412" || prod.Topic != "Allow bigger uploads (ingress 25 MB)" || prod.Env != "prod" {
		t.Fatalf("ticket/env enrichment: %+v", prod)
	}
	if prod.Pair == nil || prod.Pair.Order != 2 || prod.Pair.After != "acme/infra-config#318" {
		t.Fatalf("pair: %+v", prod.Pair)
	}
	if prod.Purpose != "Same limit for prod." || prod.TicketURL != "https://jira.example/browse/OPS-412" {
		t.Fatalf("purpose/url: %q %q", prod.Purpose, prod.TicketURL)
	}
	rb := get(t, v, "acme/runbooks#88")
	if !rb.PostedKnown || rb.Posted == nil {
		t.Fatalf("posted: %+v", rb)
	}
	tf := get(t, v, "acme/terraform-modules#203")
	if !tf.PostedKnown || tf.Posted != nil || v.Topic(tf) != "AWS provider bump." {
		t.Fatalf("no-ticket PR: posted=%v topic=%q", tf.Posted, v.Topic(tf))
	}
	if v.TopicShort(prod) != "Allow bigger uploads" {
		t.Fatalf("short topic = %q", v.TopicShort(prod))
	}
}

func TestMergedSiblingIsFetched(t *testing.T) {
	gh := scenario()
	eng := gh.prs["acme/infra-config#318"]
	eng.State = "MERGED"
	gh.prs[eng.Key()] = eng
	d := newDeck(t, gh, tue1102)
	v := view(t, d, tue1102)
	sib := get(t, v, "acme/infra-config#318")
	if !sib.Sibling || !sib.Merged() {
		t.Fatalf("merged eng should be fetched as a sibling: %+v", sib)
	}
	if s := v.StateOf(get(t, v, "acme/infra-config#319")); s != StReady {
		t.Fatalf("prod should be ready once eng merged, got %s", s)
	}
}

func TestPlanMergeGuards(t *testing.T) {
	gh := scenario()
	d := newDeck(t, gh, fri1000)
	v := view(t, d, fri1000)
	prod := get(t, v, "acme/infra-config#319")
	if s := v.StateOf(prod); s != StWaiting {
		t.Fatalf("prod state = %s", s)
	}
	pl := v.PlanMerge(prod)
	if pl.Kind != PlanSibling || pl.Target.Number != 318 || !strings.Contains(pl.Message, "Merge ① first?") {
		t.Fatalf("pair guard: %+v", pl)
	}
	// Eng is not critical: it merges any time, even on a Friday.
	if pl := v.PlanMerge(pl.Target); pl.Kind != PlanMerge {
		t.Fatalf("eng plan: %+v", pl)
	}
	if err := d.Merge(context.Background(), pl.Target); err != nil {
		t.Fatal(err)
	}
	v = view(t, d, fri1000)
	pl = v.PlanMerge(get(t, v, "acme/infra-config#319"))
	if pl.Kind != PlanSchedule || !pl.At.Equal(mon0930) || !strings.Contains(pl.Message, "Fri–Sun closed") {
		t.Fatalf("window guard: %+v", pl)
	}
	if err := d.Schedule(pl.Target, pl.At); err != nil {
		t.Fatal(err)
	}
	v = view(t, d, fri1000)
	prod = get(t, v, "acme/infra-config#319")
	if v.StateOf(prod) != StScheduled || v.PlanMerge(prod).Kind != PlanCancel {
		t.Fatalf("scheduled prod should offer cancel: %s %+v", v.StateOf(prod), v.PlanMerge(prod))
	}
	if !strings.Contains(v.Next(prod), "◷ merges Mon 26 Oct 09:30") {
		t.Fatalf("next = %q", v.Next(prod))
	}
	rb := get(t, v, "acme/runbooks#88")
	if pl := v.PlanMerge(rb); pl.Kind != PlanRefuse || !strings.Contains(pl.Message, "posted in #team-reviews 6d ago") {
		t.Fatalf("review guard: %+v", pl)
	}
}

func TestProductionLabelIsCritical(t *testing.T) {
	gh := scenario()
	p := gh.prs["acme/terraform-modules#203"]
	p.Labels = []string{"prod-change"}
	p.ReviewDecision = model.ReviewApproved
	gh.prs[p.Key()] = p
	d := newDeck(t, gh, fri1000)
	v := view(t, d, fri1000)
	if pl := v.PlanMerge(get(t, v, p.Key())); pl.Kind != PlanSchedule {
		t.Fatalf("production label should make it obey windows: %+v", pl)
	}
	d.Cfg.ProductionLabelIsCritical = false
	v.Cfg = d.Cfg
	if pl := v.PlanMerge(get(t, v, p.Key())); pl.Kind != PlanMerge {
		t.Fatalf("with the default off it merges any time: %+v", pl)
	}
}

func TestOutsideWindowBlockMode(t *testing.T) {
	gh := scenario()
	eng := gh.prs["acme/infra-config#318"]
	eng.State = "MERGED"
	gh.prs[eng.Key()] = eng
	d := newDeck(t, gh, fri1000)
	d.Cfg.MergeWindows.OutsideWindow = "block"
	v := view(t, d, fri1000)
	if pl := v.PlanMerge(get(t, v, "acme/infra-config#319")); pl.Kind != PlanRefuse {
		t.Fatalf("block mode must refuse: %+v", pl)
	}
}

func TestFlaggedUntouchedToday(t *testing.T) {
	gh := scenario()
	d := newDeck(t, gh, tue1102)
	v := view(t, d, tue1102)
	eng := get(t, v, "acme/infra-config#318")
	prod := get(t, v, "acme/infra-config#319")
	if !v.Flagged(eng) {
		t.Fatal("ready PR untouched since yesterday should be flagged")
	}
	if v.Flagged(prod) {
		t.Fatal("PR waiting on its sibling cannot move: no flag")
	}
	d.MarkReviewed(eng.Key())
	v = view(t, d, tue1102)
	if v.Flagged(eng) {
		t.Fatal("reviewed PR should not be flagged until tomorrow")
	}
	if v.Count().Flagged != 2 { // runbooks#88 and terraform#203
		t.Fatalf("flag count = %d", v.Count().Flagged)
	}
}

func TestFireDue(t *testing.T) {
	gh := scenario()
	eng := gh.prs["acme/infra-config#318"]
	eng.State = "MERGED"
	gh.prs[eng.Key()] = eng
	d := newDeck(t, gh, fri1000)
	prod := gh.prs["acme/infra-config#319"]
	d.Schedule(prod, mon0930)
	rules, _ := d.Rules(context.Background())

	ev, _ := d.FireDue(context.Background(), rules, mon0930.Add(-8*time.Minute))
	if len(ev) != 1 || !strings.Contains(ev[0].Title, "merges at 09:30") {
		t.Fatalf("10-minute reminder: %+v", ev)
	}
	if ev, _ := d.FireDue(context.Background(), rules, mon0930.Add(-5*time.Minute)); len(ev) != 0 {
		t.Fatalf("reminder must fire once: %+v", ev)
	}

	// CI went red over the weekend: skip, keep scheduled, report once.
	p := gh.prs[prod.Key()]
	p.CI = model.CIFail
	gh.prs[prod.Key()] = p
	ev, _ = d.FireDue(context.Background(), rules, mon0930.Add(time.Minute))
	if len(ev) != 1 || !strings.Contains(ev[0].Body, "CI is fail") {
		t.Fatalf("skip event: %+v", ev)
	}
	if ev, _ := d.FireDue(context.Background(), rules, mon0930.Add(2*time.Minute)); len(ev) != 0 {
		t.Fatalf("same skip reason must not repeat: %+v", ev)
	}

	// Fixed: the next tick merges.
	p.CI = model.CIPass
	gh.prs[prod.Key()] = p
	ev, _ = d.FireDue(context.Background(), rules, mon0930.Add(3*time.Minute))
	if len(ev) != 1 || !strings.Contains(ev[0].Title, "scheduled merge ran") || len(gh.merged) != 1 {
		t.Fatalf("fire: %+v merged=%v", ev, gh.merged)
	}
	st, _ := d.Store.Load()
	if len(st.Schedules) != 0 {
		t.Fatalf("schedule should be cleared: %+v", st.Schedules)
	}
}

func TestFireDueMissedWindowRollsOver(t *testing.T) {
	gh := scenario()
	eng := gh.prs["acme/infra-config#318"]
	eng.State = "MERGED"
	gh.prs[eng.Key()] = eng
	d := newDeck(t, gh, fri1000)
	d.Schedule(gh.prs["acme/infra-config#319"], mon0930)
	rules, _ := d.Rules(context.Background())
	// Laptop asleep until 12:15: the morning slot is over.
	ev, _ := d.FireDue(context.Background(), rules, time.Date(2026, 10, 26, 12, 15, 0, 0, time.Local))
	if len(ev) != 1 || !strings.Contains(ev[0].Body, "rolled to 14:00") || len(gh.merged) != 0 {
		t.Fatalf("roll over: %+v", ev)
	}
	// Woke inside the window: catch up immediately.
	ev, _ = d.FireDue(context.Background(), rules, time.Date(2026, 10, 26, 14, 5, 0, 0, time.Local))
	if len(ev) != 1 || len(gh.merged) != 1 {
		t.Fatalf("catch up: %+v merged=%v", ev, gh.merged)
	}
}

func TestNotices(t *testing.T) {
	gh := scenario()
	eng := gh.prs["acme/infra-config#318"]
	eng.State = "MERGED"
	gh.prs[eng.Key()] = eng
	d := newDeck(t, gh, tue1102)
	d.Cfg.Herdr.RecallAt = "11:00"
	v := view(t, d, tue1102)
	ids := map[string]Event{}
	for _, e := range v.Notices() {
		ids[strings.SplitN(e.ID, ":", 2)[0]] = e
	}
	for _, want := range []string{"unblocked", "overdue", "window-open", "recall"} {
		if _, ok := ids[want]; !ok {
			t.Errorf("missing %s notice in %v", want, ids)
		}
	}
	if e := ids["unblocked"]; e.Title != "◆ Allow bigger uploads" {
		t.Errorf("unblocked title leads with the topic, got %q", e.Title)
	}
}

func TestTokensAndAgentPRs(t *testing.T) {
	gh := scenario()
	d := newDeck(t, gh, tue1102)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "p"), 0o700)
	line := func(s string) string { return s + "\n" }
	os.WriteFile(filepath.Join(dir, "p", "s.jsonl"), []byte(
		line(`{"type":"user","message":{"content":"open the pair"},"cwd":"/w/infra","sessionId":"S1","timestamp":"2026-10-15T14:00:00Z"}`)+
			line(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","input":{"command":"gh pr create"}}]}}`)+
			line(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"https://github.example/acme/infra-config/pull/318 https://github.example/acme/infra-config/pull/319"}]},"sessionId":"S1","cwd":"/w/infra"}`)), 0o600)
	d.Cfg.Why.TranscriptsDir = dir
	v := view(t, d, tue1102)
	prs := v.AgentPRs("S1", "")
	if len(prs) != 2 {
		t.Fatalf("agent PRs = %d", len(prs))
	}
	if got := v.Tokens(prs, "acme/infra-config"); got != "①#318●⚑ ②#319◇" {
		t.Fatalf("tokens = %q", got)
	}
	if w := window.When(tue1102, mon0930); w != "Mon 26 Oct 09:30" {
		t.Fatalf("When = %q", w)
	}
}

type countingJira struct {
	fakeJira
	calls int
}

func (c *countingJira) Get(ctx context.Context, k string) (jira.Ticket, error) {
	c.calls++
	return c.fakeJira.Get(ctx, k)
}

type countingSlack struct {
	fakeSlack
	calls int
}

func (c *countingSlack) FirstPost(ctx context.Context, ch, url string) (*time.Time, error) {
	c.calls++
	return c.fakeSlack.FirstPost(ctx, ch, url)
}

func TestSlowSourcesAreCached(t *testing.T) {
	gh := scenario()
	now := tue1102
	d := newDeck(t, gh, now)
	cj := &countingJira{fakeJira: d.Jira.(fakeJira)}
	cs := &countingSlack{fakeSlack: d.Slack.(fakeSlack)}
	d.Jira, d.Slack = cj, cs
	d.Now = func() time.Time { return now }
	d.Refresh(context.Background())
	j1, s1 := cj.calls, cs.calls
	if j1 != 2 || s1 != 2 { // OPS-412, OPS-415; runbooks#88 and terraform#203
		t.Fatalf("first refresh: jira=%d slack=%d", j1, s1)
	}
	now = now.Add(time.Minute)
	d.Refresh(context.Background())
	if cj.calls != j1 || cs.calls != s1 {
		t.Fatalf("a refresh one minute later must reuse cached Jira/Slack answers: jira=%d slack=%d", cj.calls, cs.calls)
	}
	now = now.Add(20 * time.Minute)
	d.Refresh(context.Background())
	if cj.calls != j1+2 {
		t.Fatalf("tickets re-fetched after the TTL: %d", cj.calls)
	}
	if cs.calls != s1+1 { // only the not-posted PR is re-checked; a found post never changes
		t.Fatalf("slack calls after TTL = %d, want %d", cs.calls, s1+1)
	}
}

func TestTopicNeverJustAKeyWithoutJira(t *testing.T) {
	v := View{Cfg: config.Default()}
	p := model.PR{Ticket: "OPS-412", Title: "OPS-412 ingress: body size 25m (prod)"}
	if got := v.Topic(p); got != "ingress: body size 25m (prod)" {
		t.Fatalf("title fallback = %q", got)
	}
	p.Purpose = "Same 25 MB upload limit for prod."
	if got := v.Topic(p); got != p.Purpose {
		t.Fatalf("purpose fallback = %q", got)
	}
	p.Topic = "Allow bigger uploads"
	if got := v.Topic(p); got != "Allow bigger uploads" {
		t.Fatalf("Jira summary wins = %q", got)
	}
}

func TestWindowToastOnlyWhenSomethingWaits(t *testing.T) {
	gh := scenario()
	d := newDeck(t, gh, tue1102)
	v := view(t, d, tue1102) // prod waits on eng: nothing critical can merge
	for _, e := range v.Notices() {
		if strings.HasPrefix(e.ID, "window-open:") {
			t.Fatalf("no critical PR is ready, but got %+v", e)
		}
	}
}

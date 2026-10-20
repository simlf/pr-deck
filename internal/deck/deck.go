// Package deck is pr-deck's core: it builds a snapshot from GitHub, Jira,
// Slack and agent transcripts, derives each PR's state, and plans merges
// under the pair-order and merge-window guards.
package deck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/simlf/pr-deck/internal/cache"
	"github.com/simlf/pr-deck/internal/config"
	"github.com/simlf/pr-deck/internal/model"
	"github.com/simlf/pr-deck/internal/pairs"
	"github.com/simlf/pr-deck/internal/sources/github"
	"github.com/simlf/pr-deck/internal/sources/ics"
	"github.com/simlf/pr-deck/internal/sources/jira"
	"github.com/simlf/pr-deck/internal/sources/slack"
	"github.com/simlf/pr-deck/internal/sources/transcript"
	"github.com/simlf/pr-deck/internal/state"
	"github.com/simlf/pr-deck/internal/ticket"
	"github.com/simlf/pr-deck/internal/trailer"
	"github.com/simlf/pr-deck/internal/window"
)

// Deck wires sources, state and rules together.
type Deck struct {
	Cfg   config.Config
	GH    github.API
	Jira  jira.API  // nil when Jira is not configured
	Slack slack.API // nil when review tracking is off
	HTTP  *http.Client
	Store state.Store
	Now   func() time.Time
}

// New builds a Deck with real clients from cfg. Missing optional sources
// (Jira, Slack) are left nil and simply produce less enrichment.
func New(cfg config.Config) *Deck {
	httpc := &http.Client{Timeout: 20 * time.Second}
	d := &Deck{Cfg: cfg, HTTP: httpc, Store: state.Store{Dir: cfg.Deck.StateDir}, Now: time.Now}
	d.GH = github.New(httpc, cfg.GitHub.APIURL, config.Secret(cfg.GitHub.TokenEnv, cfg.GitHub.TokenCmd), cfg.GitHub.Query)
	if cfg.Jira.BaseURL != "" {
		d.Jira = &jira.Client{HTTP: httpc, BaseURL: cfg.Jira.BaseURL, Email: cfg.Jira.Email,
			Token: config.Secret([]string{cfg.Jira.TokenEnv}, cfg.Jira.TokenCmd)}
	}
	if cfg.Review.Channel != "" {
		if tok := config.Secret([]string{cfg.Review.TokenEnv}, cfg.Review.TokenCmd); tok != "" {
			d.Slack = &slack.Client{HTTP: httpc, APIURL: cfg.Review.APIURL, Token: tok}
		}
	}
	return d
}

// SnapshotPath is where the cached snapshot lives.
func (d *Deck) SnapshotPath() string { return cache.SnapshotPath(d.Cfg.Deck.CacheDir) }

// Cached returns the last snapshot (ok=false when none yet).
func (d *Deck) Cached() (model.Snapshot, bool) {
	s, ok, _ := cache.Load(d.SnapshotPath())
	return s, ok
}

// Refresh rebuilds the snapshot from every source and caches it. Source
// failures are recorded in Snapshot.Errors; only a GitHub failure is fatal.
func (d *Deck) Refresh(ctx context.Context) (model.Snapshot, error) {
	now := d.Now()
	prev, _ := d.Cached()
	st, err := d.Store.Load()
	if err != nil {
		return model.Snapshot{}, fmt.Errorf("state: %w", err)
	}
	open, err := d.GH.SearchOpen(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}
	snap := model.Snapshot{At: now}
	for i := range open {
		d.enrichBasic(&open[i])
	}
	// Fetch PRs referenced by Merge-after or manual pairs (often merged).
	for _, key := range pairs.ReferencedMissing(open, st.Pairs) {
		repo, n, ok := splitKey(key)
		if !ok {
			continue
		}
		sib, err := d.GH.Get(ctx, repo, n)
		if err != nil {
			snap.Errors = append(snap.Errors, fmt.Sprintf("sibling %s: %v", key, err))
			continue
		}
		d.enrichBasic(&sib)
		sib.Sibling = true
		open = append(open, sib)
	}
	snap.Errors = append(snap.Errors, d.enrichTickets(ctx, open, prev, now)...)
	snap.Errors = append(snap.Errors, d.enrichWhy(open)...)
	snap.Errors = append(snap.Errors, d.enrichPosted(ctx, open, prev, now)...)
	snap.Errors = append(snap.Errors, d.enrichPlans(ctx, open, prev, now)...)
	pairs.Link(open, d.Cfg.Envs, st.Pairs)
	sort.SliceStable(open, func(i, j int) bool { return open[i].Key() < open[j].Key() })
	snap.PRs = open
	if err := cache.Save(d.SnapshotPath(), snap); err != nil {
		snap.Errors = append(snap.Errors, "cache: "+err.Error())
	}
	return snap, nil
}

// RefreshPR re-reads key's CI from GitHub and writes it into the cached
// snapshot, for a quick look without a full refresh.
func (d *Deck) RefreshPR(ctx context.Context, key string) (model.PR, error) {
	repo, n, ok := splitKey(key)
	if !ok {
		return model.PR{}, fmt.Errorf("bad PR key %q", key)
	}
	fresh, err := d.GH.Get(ctx, repo, n)
	if err != nil {
		return model.PR{}, err
	}
	snap, ok := d.Cached()
	if !ok {
		return fresh, nil
	}
	for i := range snap.PRs {
		if snap.PRs[i].Key() == key {
			snap.PRs[i].CI, snap.PRs[i].FailedRuns = fresh.CI, fresh.FailedRuns
		}
	}
	return fresh, cache.Save(d.SnapshotPath(), snap)
}

func (d *Deck) enrichBasic(p *model.PR) {
	p.Ticket = ticket.Extract(d.Cfg.Jira.Projects, p.Branch, p.Title, p.Body)
	p.Env = pairs.DetectEnv(d.Cfg.Envs, p.Branch, p.Title, p.Labels)
	p.Purpose = trailer.Purpose(p.Body)
}

// Re-ask Jira and Slack at most this often; GitHub is asked every refresh.
const (
	ticketTTL = 15 * time.Minute
	postedTTL = 10 * time.Minute
	plansTTL  = 5 * time.Minute
)

func (d *Deck) enrichTickets(ctx context.Context, prs []model.PR, prev model.Snapshot, now time.Time) []string {
	known := map[string]model.PR{}
	for _, p := range prev.PRs {
		if p.Ticket != "" && p.Topic != "" {
			known[p.Ticket] = p
		}
	}
	type info struct {
		topic, status, url string
		fetched            time.Time
	}
	got := map[string]info{}
	var errs []string
	for _, p := range prs {
		if p.Ticket == "" {
			continue
		}
		if _, done := got[p.Ticket]; done {
			continue
		}
		if d.Jira == nil {
			got[p.Ticket] = info{}
			continue
		}
		k, haveOld := known[p.Ticket]
		if haveOld && now.Sub(k.TicketFetched) < ticketTTL {
			got[p.Ticket] = info{k.Topic, k.TicketStatus, k.TicketURL, k.TicketFetched}
			continue
		}
		t, err := d.Jira.Get(ctx, p.Ticket)
		if err != nil {
			errs = append(errs, "jira: "+err.Error())
			if haveOld { // keep the last good topic
				got[p.Ticket] = info{k.Topic, k.TicketStatus, k.TicketURL, k.TicketFetched}
			} else {
				got[p.Ticket] = info{url: d.Jira.BrowseURL(p.Ticket)}
			}
			continue
		}
		got[p.Ticket] = info{t.Summary, t.Status, d.Jira.BrowseURL(p.Ticket), now}
	}
	for i := range prs {
		if in, ok := got[prs[i].Ticket]; ok {
			prs[i].Topic, prs[i].TicketStatus, prs[i].TicketURL, prs[i].TicketFetched = in.topic, in.status, in.url, in.fetched
		}
	}
	return dedupe(errs)
}

func (d *Deck) enrichWhy(prs []model.PR) []string {
	path := filepath.Join(d.Cfg.Deck.CacheDir, "why.json")
	ix := transcript.Load(path)
	if _, err := os.Stat(d.Cfg.Why.TranscriptsDir); err == nil {
		if err := ix.Scan(d.Cfg.Why.TranscriptsDir, d.Cfg.GitHub.WebHost); err != nil {
			return []string{"transcripts: " + err.Error()}
		}
		if err := ix.Save(path); err != nil {
			return []string{"transcripts: " + err.Error()}
		}
	}
	for i := range prs {
		if w, ok := ix.Lookup(prs[i].Key()); ok {
			w := w
			prs[i].Why = &w
		}
	}
	return nil
}

func (d *Deck) enrichPosted(ctx context.Context, prs []model.PR, prev model.Snapshot, now time.Time) []string {
	if d.Slack == nil || d.Cfg.Review.Channel == "" {
		return nil
	}
	var errs []string
	for i := range prs {
		p := &prs[i]
		if p.Merged() || p.Sibling || p.ReviewDecision == model.ReviewApproved || (p.Draft && d.Cfg.Review.SkipDrafts) {
			continue
		}
		if old, ok := prev.Find(p.Key()); ok && old.PostedKnown {
			if old.Posted != nil || now.Sub(old.PostedChecked) < postedTTL {
				// The first post never changes; "not posted" is re-checked
				// every postedTTL.
				p.Posted, p.PostedKnown, p.PostedChecked = old.Posted, true, old.PostedChecked
				continue
			}
		}
		at, err := d.Slack.FirstPost(ctx, d.Cfg.Review.Channel, p.URL)
		if err != nil {
			errs = append(errs, "slack: "+err.Error())
			continue
		}
		p.Posted, p.PostedKnown, p.PostedChecked = at, true, now
	}
	return dedupe(errs)
}

// enrichPlans re-reads plan comments when the PR changed or plansTTL passed.
func (d *Deck) enrichPlans(ctx context.Context, prs []model.PR, prev model.Snapshot, now time.Time) []string {
	var errs []string
	for i := range prs {
		p := &prs[i]
		if p.Merged() || p.Sibling {
			continue
		}
		if old, ok := prev.Find(p.Key()); ok && old.UpdatedAt.Equal(p.UpdatedAt) && now.Sub(old.PlansChecked) < plansTTL {
			p.Plans, p.PlansChecked = old.Plans, old.PlansChecked
			continue
		}
		pl, err := d.GH.Plans(ctx, *p)
		if err != nil {
			errs = append(errs, "plans: "+err.Error())
			continue
		}
		p.Plans, p.PlansChecked = pl, now
	}
	return dedupe(errs)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func splitKey(key string) (string, int, bool) {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '#' {
			var n int
			if _, err := fmt.Sscanf(key[i+1:], "%d", &n); err != nil {
				return "", 0, false
			}
			return key[:i], n, true
		}
	}
	return "", 0, false
}

// Rules compiles the merge-window rules, loading holidays from the ICS feed
// at most once a day (cached; a failed fetch keeps the last good list).
func (d *Deck) Rules(ctx context.Context) (window.Rules, error) {
	hol := d.holidays(ctx)
	return window.Compile(d.Cfg.MergeWindows, hol)
}

type holidayCache struct {
	Source  string    `json:"source"`
	Fetched time.Time `json:"fetched"`
	Dates   []string  `json:"dates"`
}

// RulesCached compiles the rules from cached holidays only, never touching
// the network (for the tmux strip, which runs every few seconds).
func (d *Deck) RulesCached() (window.Rules, error) {
	return window.Compile(d.Cfg.MergeWindows, d.cachedHolidays().Dates)
}

func (d *Deck) cachedHolidays() holidayCache {
	var hc holidayCache
	if b, err := os.ReadFile(filepath.Join(d.Cfg.Deck.CacheDir, "holidays.json")); err == nil {
		_ = json.Unmarshal(b, &hc)
	}
	return hc
}

func (d *Deck) holidays(ctx context.Context) []string {
	src := d.Cfg.MergeWindows.HolidaysICS
	if src == "" {
		return nil
	}
	path := filepath.Join(d.Cfg.Deck.CacheDir, "holidays.json")
	hc := d.cachedHolidays()
	if hc.Source == src && d.Now().Sub(hc.Fetched) < 24*time.Hour {
		return hc.Dates
	}
	dates, err := ics.Load(ctx, d.HTTP, src)
	if err != nil {
		return hc.Dates
	}
	hc = holidayCache{Source: src, Fetched: d.Now(), Dates: dates}
	if b, err := json.Marshal(hc); err == nil {
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		_ = os.WriteFile(path, b, 0o600)
	}
	return dates
}

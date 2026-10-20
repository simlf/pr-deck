// Package tui is pr-deck's terminal UI: a board grouped by topic and an
// end-of-day recall layout, both mouse- and keyboard-driven.
package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/simlf/pr-deck/internal/daemon"
	"github.com/simlf/pr-deck/internal/deck"
	"github.com/simlf/pr-deck/internal/desktop"
	"github.com/simlf/pr-deck/internal/model"
	"github.com/simlf/pr-deck/internal/pairs"
	"github.com/simlf/pr-deck/internal/window"
)

// Layouts.
const (
	Board  = "board"
	Recall = "recall"
)

type mode int

const (
	modeNormal mode = iota
	modeConfirm
	modeAlias
	modeFilter
	modePair
	modeHelp
)

// Options configure a TUI session.
type Options struct {
	Layout string
	Focus  string // PR key or URL to select at start (herdr link handler)
}

// Model is the Bubble Tea model.
type Model struct {
	dk     *deck.Deck
	view   deck.View
	layout string
	width  int
	height int

	cursor    string
	offset    int
	collapsed map[string]bool
	sort      int
	filter    string
	whyOpen   bool

	mode     mode
	plan     deck.Plan
	input    []rune
	pairFrom string
	tfKey    string // PR whose terraform plan line tfSel points at
	tfSel    int
	// rerunning holds PRs whose CI was just rerun but still reads as failed.
	rerunning map[string]bool

	msg    string
	msgErr bool
	busy   string

	lastClick    time.Time
	lastClickKey string
	lines        []line
	bodyTop      int
	focus        string
	loaded       bool
}

// New builds the model. The first snapshot comes from the cache; a refresh
// starts in the background when the cache is missing or stale.
func New(dk *deck.Deck, opt Options) *Model {
	layout := opt.Layout
	if layout != Recall {
		layout = Board
	}
	m := &Model{dk: dk, layout: layout, collapsed: map[string]bool{}, rerunning: map[string]bool{}, focus: opt.Focus, width: 120, height: 32}
	m.reload()
	return m
}

type snapMsg struct {
	snap model.Snapshot
	err  error
}
type tickMsg time.Time
type doneMsg struct {
	text    string
	err     error
	refresh bool
	rerun   string // PR key whose CI was rerun
}
type rerunPollMsg struct {
	key string
	try int
	pr  model.PR
	err error
}

// GitHub can report the old failed runs for a while after a rerun, so the
// PR is re-read rerunTries times, rerunEvery apart.
var (
	rerunEvery = 5 * time.Second
	rerunTries = 6
)

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tick()}
	if !m.loaded || m.dk.Now().Sub(m.view.Snap.At) > m.dk.Cfg.Deck.Refresh.Duration {
		m.busy = "refreshing…"
		cmds = append(cmds, m.refresh())
	}
	return tea.Batch(cmds...)
}

func tick() tea.Cmd {
	return tea.Tick(20*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) refresh() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		s, err := m.dk.Refresh(ctx)
		_, _ = m.dk.Rules(ctx) // warms the holiday cache off the UI thread
		return snapMsg{s, err}
	}
}

// reload re-reads cache and state (the daemon may have changed them) and
// recomputes the view.
func (m *Model) reload() {
	if s, ok := m.dk.Cached(); ok {
		m.view.Snap = s
		m.loaded = true
	}
	m.recompute()
}

func (m *Model) recompute() {
	if st, err := m.dk.Store.Load(); err == nil {
		m.view.State = st
	}
	rules, err := m.dk.RulesCached() // never blocks the UI on the network
	if err != nil {
		m.setErr(err)
	}
	m.view.Rules = rules
	m.view.Cfg = m.dk.Cfg
	m.view.Now = m.dk.Now()
	pairs.Link(m.view.Snap.PRs, m.dk.Cfg.Envs, m.view.State.Pairs)
	m.applyFocus()
	m.ensureCursor()
}

func (m *Model) applyFocus() {
	if m.focus == "" {
		return
	}
	for _, p := range m.view.Snap.PRs {
		if p.Key() == m.focus || p.URL == m.focus || strings.TrimSuffix(m.focus, "/") == p.URL {
			m.cursor, m.whyOpen, m.focus = p.Key(), true, ""
			return
		}
	}
}

// keys lists selectable PR keys in display order.
func (m *Model) keys() []string {
	var out []string
	if m.layout == Recall {
		for _, p := range m.recallRows() {
			out = append(out, p.Key())
		}
		return out
	}
	for _, g := range m.groups() {
		if m.collapsed[g.id] {
			continue
		}
		for _, p := range g.prs {
			out = append(out, p.Key())
		}
	}
	return out
}

func (m *Model) ensureCursor() {
	ks := m.keys()
	for _, k := range ks {
		if k == m.cursor {
			return
		}
	}
	m.cursor = ""
	if len(ks) > 0 {
		m.cursor = ks[0]
	}
}

func (m *Model) move(delta int) {
	ks := m.keys()
	if len(ks) == 0 {
		return
	}
	i := 0
	for j, k := range ks {
		if k == m.cursor {
			i = j
		}
	}
	i += delta
	if i < 0 {
		i = 0
	}
	if i >= len(ks) {
		i = len(ks) - 1
	}
	m.cursor = ks[i]
}

func (m *Model) selected() (model.PR, bool) {
	if m.cursor == "" {
		return model.PR{}, false
	}
	return m.view.Snap.Find(m.cursor)
}

// planSel is the selected terraform plan line of p, or -1.
func (m *Model) planSel(p model.PR) int {
	if m.tfKey != p.Key() || m.tfSel >= len(p.Plans) {
		return -1
	}
	return m.tfSel
}

func (m *Model) say(s string)     { m.msg, m.msgErr = s, false }
func (m *Model) setErr(err error) { m.msg, m.msgErr = err.Error(), true }
func (m *Model) touch(key string) { _ = m.dk.Touch(key); m.recompute() }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		m.reload()
		cmds := []tea.Cmd{tick()}
		if m.busy == "" && m.dk.Now().Sub(m.view.Snap.At) > 2*m.dk.Cfg.Deck.Refresh.Duration {
			m.busy = "refreshing…"
			cmds = append(cmds, m.refresh())
		}
		return m, tea.Batch(cmds...)
	case snapMsg:
		m.busy = ""
		if msg.err != nil {
			m.setErr(fmt.Errorf("refresh: %w", msg.err))
			return m, nil
		}
		m.view.Snap, m.loaded = msg.snap, true
		m.recompute()
		if len(msg.snap.Errors) > 0 && !m.msgErr && m.msg == "" {
			m.say(fmt.Sprintf("%d source warnings (e.g. %s)", len(msg.snap.Errors), msg.snap.Errors[0]))
		}
		return m, nil
	case doneMsg:
		m.busy = ""
		if msg.err != nil {
			m.setErr(msg.err)
		} else {
			m.say(msg.text)
		}
		m.recompute()
		var cmds []tea.Cmd
		if msg.rerun != "" && msg.err == nil {
			m.rerunning[msg.rerun] = true
			cmds = append(cmds, m.pollRerun(msg.rerun, 1))
		}
		if msg.refresh {
			m.busy = "refreshing…"
			cmds = append(cmds, m.refresh())
		}
		return m, tea.Batch(cmds...)
	case rerunPollMsg:
		m.reload()
		if msg.err == nil && msg.pr.CI != model.CIFail || msg.try >= rerunTries {
			delete(m.rerunning, msg.key)
			return m, nil
		}
		return m, m.pollRerun(msg.key, msg.try+1)
	case tea.MouseMsg:
		return m, m.mouse(msg)
	case tea.KeyMsg:
		// Fast typing or a paste can deliver several runes in one message;
		// outside text input each rune is its own command.
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && m.mode != modeAlias && m.mode != modeFilter && !msg.Paste {
			var cmds []tea.Cmd
			for _, r := range msg.Runes {
				_, c := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				cmds = append(cmds, c)
			}
			return m, tea.Batch(cmds...)
		}
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeHelp:
		m.mode = modeNormal
		return m, nil
	case modeConfirm:
		switch s {
		case "y", "Y", "enter":
			m.mode = modeNormal
			return m, m.execute(m.plan)
		default:
			m.mode = modeNormal
			m.say("cancelled")
		}
		return m, nil
	case modeAlias, modeFilter:
		return m, m.textKey(k)
	case modePair:
		switch s {
		case "esc":
			m.mode = modeNormal
			m.say("pairing cancelled")
		case "u":
			m.mode = modeNormal
			_ = m.dk.SetPair(m.pairFrom, "")
			m.recompute()
			m.say("unpaired")
		case "enter", " ":
			m.mode = modeNormal
			if m.cursor == m.pairFrom {
				m.say("pick a different PR (u unpairs)")
				return m, nil
			}
			_ = m.dk.SetPair(m.pairFrom, m.cursor)
			m.recompute()
			if p, ok := m.view.Snap.Find(m.pairFrom); ok {
				m.cursor = p.Key()
				m.say(fmt.Sprintf("paired: %s merges after %s", p.Ref(), m.cursor))
			}
			return m, m.refresh()
		case "j", "down":
			m.move(1)
		case "k", "up":
			m.move(-1)
		}
		return m, nil
	}
	m.msg = ""
	p, have := m.selected()
	switch s {
	case "q", "esc":
		if m.filter != "" && s == "esc" {
			m.filter = ""
			m.ensureCursor()
			return m, nil
		}
		return m, tea.Quit
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g", "home":
		m.move(-1 << 20)
	case "G", "end":
		m.move(1 << 20)
	case "pgdown", "ctrl+d":
		m.move(10)
	case "pgup", "ctrl+u":
		m.move(-10)
	case "h", "left":
		if have && m.layout == Board {
			m.collapsed[m.view.GroupKey(p)] = true
			m.ensureCursor()
		}
	case "l", "right":
		m.collapsed = map[string]bool{}
	case "w":
		m.whyOpen = !m.whyOpen
	case "R":
		if m.layout == Board {
			m.layout = Recall
		} else {
			m.layout = Board
		}
		m.offset = 0
	case "s":
		m.sort = (m.sort + 1) % len(sortNames)
		if m.layout == Board {
			m.layout = Recall
		}
	case "/":
		m.mode, m.input = modeFilter, []rune(m.filter)
	case "?":
		m.mode = modeHelp
	case "ctrl+r":
		m.busy = "refreshing…"
		return m, m.refresh()
	}
	if !have {
		return m, nil
	}
	switch s {
	case "tab":
		next := m.planSel(p) + 1
		if next >= len(p.Plans) {
			next = -1
		}
		m.tfKey, m.tfSel = p.Key(), next
	case "enter", "o":
		if i := m.planSel(p); i >= 0 && s == "enter" {
			m.open(p.Plans[i].URL, p.Key())
			return m, nil
		}
		m.open(p.URL, p.Key())
	case "J":
		if p.TicketURL == "" {
			m.say("no Jira link for this PR")
		} else {
			m.open(p.TicketURL, p.Key())
		}
	case "y":
		m.copy(p.URL, "copied link "+p.Ref(), p.Key())
	case "Y":
		m.copy(m.summary(p), "copied summary for "+p.Ref(), p.Key())
	case "P":
		switch {
		case p.ReviewDecision == model.ReviewApproved || p.Merged():
			m.say(p.Ref() + " is already approved")
		case m.dk.Slack == nil:
			m.copy(m.reviewRequest(p), "copied review request for "+m.channel(), p.Key())
		case p.Posted != nil:
			m.copy(m.reviewRequest(p), "already posted in "+m.channel()+": copied a nudge instead", p.Key())
		default:
			m.plan = deck.Plan{Kind: deck.PlanPost, Target: p, Message: "post a review request for " + p.Ref() + " to " + m.channel() + "?"}
			m.mode = modeConfirm
		}
	case "r":
		if p.CI != model.CIFail {
			m.say("CI is not failing on " + p.Ref())
			return m, nil
		}
		if m.rerunning[p.Key()] {
			m.say("CI is already rerunning on " + p.Ref())
			return m, nil
		}
		m.busy = "rerunning failed jobs…"
		return m, m.rerun(p)
	case "m":
		pl := m.view.PlanMerge(p)
		if pl.Kind == deck.PlanRefuse {
			m.say(pl.Message)
			return m, nil
		}
		m.plan, m.mode = pl, modeConfirm
	case "p":
		m.mode, m.pairFrom = modePair, p.Key()
		m.say("pair " + p.Ref() + ": move to the PR that must merge first, enter to confirm · u unpair · esc cancel")
	case "f":
		if !m.view.Flagged(p) {
			m.say(p.Ref() + " is not flagged")
			return m, nil
		}
		_ = m.dk.MarkReviewed(p.Key())
		m.recompute()
		m.say("reviewed " + p.Ref() + ": flag cleared until tomorrow")
	case "t":
		m.mode, m.input = modeAlias, []rune(m.view.TopicShort(p))
	}
	return m, nil
}

func (m *Model) textKey(k tea.KeyMsg) tea.Cmd {
	switch k.Type {
	case tea.KeyEsc:
		if m.mode == modeFilter {
			m.filter = ""
			m.ensureCursor()
		}
		m.mode = modeNormal
		return nil
	case tea.KeyEnter:
		val := strings.TrimSpace(string(m.input))
		if m.mode == modeFilter {
			m.filter = val
			m.ensureCursor()
		} else if p, ok := m.selected(); ok {
			short := m.view.Topic(p)
			if val == short {
				val = ""
			}
			_ = m.dk.SetAlias(m.view.AliasKey(p), val)
			m.recompute()
			m.say("topic short form saved: " + m.view.TopicShort(p))
		}
		m.mode = modeNormal
		return nil
	case tea.KeyBackspace:
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case tea.KeyRunes, tea.KeySpace:
		m.input = append(m.input, k.Runes...)
		if k.Type == tea.KeySpace {
			m.input = append(m.input, ' ')
		}
	}
	if m.mode == modeFilter {
		m.filter = string(m.input)
		m.ensureCursor()
	}
	return nil
}

func (m *Model) mouse(ev tea.MouseMsg) tea.Cmd {
	switch {
	case ev.Button == tea.MouseButtonWheelUp:
		m.move(-1)
		return nil
	case ev.Button == tea.MouseButtonWheelDown:
		m.move(1)
		return nil
	case ev.Action != tea.MouseActionPress || ev.Button != tea.MouseButtonLeft:
		return nil
	}
	i := ev.Y - m.bodyTop + m.offset
	if i < 0 || i >= len(m.lines) || ev.Y < m.bodyTop {
		return nil
	}
	ln := m.lines[i]
	switch {
	case ln.group != "":
		if ln.jiraX[1] > 0 && ev.X >= ln.jiraX[0] && ev.X < ln.jiraX[1] {
			for _, g := range m.groups() {
				if g.id == ln.group && len(g.prs) > 0 && g.prs[0].TicketURL != "" {
					m.open(g.prs[0].TicketURL, "")
				}
			}
			return nil
		}
		m.collapsed[ln.group] = !m.collapsed[ln.group]
		m.ensureCursor()
	case ln.key != "":
		now := time.Now()
		if ln.key == m.lastClickKey && now.Sub(m.lastClick) < 400*time.Millisecond {
			if p, ok := m.view.Snap.Find(ln.key); ok {
				m.open(p.URL, p.Key())
			}
			m.lastClickKey = ""
			return nil
		}
		m.cursor, m.lastClick, m.lastClickKey = ln.key, now, ln.key
	}
	return nil
}

func (m *Model) open(url, touchKey string) {
	if err := desktop.Open(url, m.dk.Cfg.Deck.OpenCmd); err != nil {
		m.setErr(err)
		return
	}
	m.say("opened " + url)
	if touchKey != "" {
		m.touch(touchKey)
	}
}

func (m *Model) copy(text, ok, touchKey string) {
	if err := desktop.Copy(text, m.dk.Cfg.Deck.CopyCmd); err != nil {
		m.setErr(err)
		return
	}
	m.say(ok)
	m.touch(touchKey)
}

func (m *Model) channel() string {
	if m.dk.Cfg.Review.Channel == "" {
		return "your review channel"
	}
	return m.dk.Cfg.Review.Channel
}

func (m *Model) summary(p model.PR) string {
	topic := m.view.Topic(p)
	if p.Ticket != "" && topic != p.Ticket {
		topic += " (" + p.Ticket + ")"
	}
	purpose := p.Purpose
	if purpose == "" {
		purpose = p.Title
	}
	return fmt.Sprintf("%s · %s · %s %s", topic, p.Ref(), purpose, p.URL)
}

func (m *Model) reviewRequest(p model.PR) string {
	text := reviewText(m.view.Topic(p), p.Ticket, p.Title, p.URL)
	if p.Posted != nil {
		return "Gentle nudge, still needs a review: " + text
	}
	return text
}

var ccPrefix = regexp.MustCompile(`^[a-z]+(\([^)]*\))?!?:\s*`)

// reviewText reads "topic (KEY): title without its conventional-commit prefix url".
func reviewText(topic, key, title, url string) string {
	text := topic
	if key != "" && topic != key {
		text += " (" + key + ")"
	}
	if t := ccPrefix.ReplaceAllString(title, ""); t != "" && t != topic {
		text += ": " + t
	}
	return text + " " + url
}

func (m *Model) rerun(p model.PR) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		n, err := m.dk.GH.RerunFailed(ctx, p)
		_ = m.dk.Touch(p.Key())
		return doneMsg{text: fmt.Sprintf("◌ rerunning %d failed run(s) on %s", n, p.Ref()), err: err, refresh: err == nil, rerun: p.Key()}
	}
}

func (m *Model) pollRerun(key string, try int) tea.Cmd {
	return tea.Tick(rerunEvery, func(time.Time) tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		p, err := m.dk.RefreshPR(ctx, key)
		return rerunPollMsg{key: key, try: try, pr: p, err: err}
	})
}

func (m *Model) execute(pl deck.Plan) tea.Cmd {
	t := pl.Target
	switch pl.Kind {
	case deck.PlanSchedule:
		if err := m.dk.Schedule(t, pl.At); err != nil {
			m.setErr(err)
			return nil
		}
		m.recompute()
		note := " (re-checks first; the daemon runs it)"
		if !daemon.Running(m.dk.Cfg.Deck.CacheDir) {
			note = " · no daemon running: start it with prd daemon --detach"
		}
		m.say("◷ " + t.Ref() + " scheduled for " + window.When(m.view.Now, pl.At) + note)
		return nil
	case deck.PlanCancel:
		_ = m.dk.Cancel(t)
		m.recompute()
		m.say("schedule cancelled for " + t.Ref())
		return nil
	case deck.PlanPost:
		m.busy = "posting to " + m.channel() + "…"
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := m.dk.PostReview(ctx, t, m.reviewRequest(t)); err != nil {
				return doneMsg{err: err}
			}
			return doneMsg{text: "posted review request for " + t.Ref() + " in " + m.channel(), refresh: true}
		}
	case deck.PlanMerge, deck.PlanSibling:
		m.busy = "merging " + t.Ref() + "…"
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := m.dk.Merge(ctx, t); err != nil {
				return doneMsg{err: fmt.Errorf("merge %s: %w", t.Ref(), err)}
			}
			text := "✓ merged " + t.Ref()
			if pl.Kind == deck.PlanSibling {
				text += " · its sibling stays put until you check " + nonEmpty(t.Env, "it")
			}
			return doneMsg{text: text, refresh: true}
		}
	}
	return nil
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

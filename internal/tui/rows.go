package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/simlf/pr-deck/internal/deck"
	"github.com/simlf/pr-deck/internal/model"
	"github.com/simlf/pr-deck/internal/review"
)

// line is one rendered body line. key/group make it a mouse target.
type line struct {
	text  string
	key   string // PR key when the line belongs to a PR row
	group string // group id for a group header
	jiraX [2]int // columns of the ticket key in a group header
}

type group struct {
	id     string // deck.View.GroupKey
	ticket string
	topic  string
	status string
	prs    []model.PR
}

var stateRank = map[string]int{
	deck.StFail: 0, deck.StReady: 1, deck.StConflict: 2, deck.StReview: 3, deck.StScheduled: 4,
	deck.StRunning: 5, deck.StWaiting: 6, deck.StDraft: 7, deck.StMerged: 9,
}

func (m *Model) visible(p model.PR) bool {
	if m.filter == "" {
		return true
	}
	f := strings.ToLower(m.filter)
	v := m.view
	for _, s := range []string{v.Topic(p), v.TopicShort(p), p.Ref(), p.Ticket, p.Title, p.Branch} {
		if strings.Contains(strings.ToLower(s), f) {
			return true
		}
	}
	return false
}

// groups builds the board's topic groups: one per ticket, one per Purpose of
// ticketless PRs, plus a single "No ticket" group.
func (m *Model) groups() []group {
	v := m.view
	byID := map[string]*group{}
	var order []string
	for _, p := range v.Snap.PRs {
		if !m.visible(p) {
			continue
		}
		id := v.GroupKey(p)
		g, ok := byID[id]
		if !ok {
			g = &group{id: id, ticket: p.Ticket, status: p.TicketStatus}
			switch {
			case id == deck.NoGroup:
				g.topic = "No ticket"
			case p.Ticket == "":
				g.topic = v.GroupPurpose(p)
			default:
				g.topic = v.Topic(p)
			}
			byID[id] = g
			order = append(order, id)
		}
		g.prs = append(g.prs, p)
	}
	var out []group
	for _, id := range order {
		g := byID[id]
		sortChains(g.prs)
		out = append(out, *g)
	}
	urgency := func(g group) int {
		best := 99
		for _, p := range g.prs {
			r := stateRank[v.StateOf(p)]
			if v.Flagged(p) {
				r -= 10
			}
			if r < best {
				best = r
			}
		}
		return best
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].id == deck.NoGroup) != (out[j].id == deck.NoGroup) {
			return out[j].id == deck.NoGroup
		}
		ui, uj := urgency(out[i]), urgency(out[j])
		if ui != uj {
			return ui < uj
		}
		return out[i].topic < out[j].topic
	})
	return out
}

// sortChains keeps chain members adjacent and in merge order.
func sortChains(prs []model.PR) {
	chainKey := func(p model.PR) string {
		if p.Pair != nil {
			return p.Pair.ID
		}
		return p.Key()
	}
	sort.SliceStable(prs, func(i, j int) bool {
		ci, cj := chainKey(prs[i]), chainKey(prs[j])
		if ci != cj {
			return ci < cj
		}
		oi, oj := 0, 0
		if prs[i].Pair != nil {
			oi = prs[i].Pair.Order
		}
		if prs[j].Pair != nil {
			oj = prs[j].Pair.Order
		}
		return oi < oj
	})
}

// recallRows is every PR sorted for the end-of-day pass, chains kept
// together: needs-you first, or by age, or by repo.
func (m *Model) recallRows() []model.PR {
	v := m.view
	units := map[string][]model.PR{}
	var ids []string
	for _, p := range v.Snap.PRs {
		if !m.visible(p) {
			continue
		}
		id := p.Key()
		if p.Pair != nil {
			id = p.Pair.ID
		}
		if _, ok := units[id]; !ok {
			ids = append(ids, id)
		}
		units[id] = append(units[id], p)
	}
	score := func(u []model.PR) (int, time.Time, string) {
		best, oldest := 99, time.Now()
		for _, p := range u {
			r := stateRank[v.StateOf(p)]
			if v.Flagged(p) {
				r -= 10
			}
			if r < best {
				best = r
			}
			if !p.CreatedAt.IsZero() && p.CreatedAt.Before(oldest) {
				oldest = p.CreatedAt
			}
		}
		return best, oldest, u[0].Repo
	}
	sort.SliceStable(ids, func(i, j int) bool {
		si, ai, ri := score(units[ids[i]])
		sj, aj, rj := score(units[ids[j]])
		switch m.sort {
		case 1:
			return ai.Before(aj)
		case 2:
			if ri != rj {
				return ri < rj
			}
		}
		if si != sj {
			return si < sj
		}
		return ids[i] < ids[j]
	})
	var out []model.PR
	for _, id := range ids {
		u := units[id]
		sortChains(u)
		out = append(out, u...)
	}
	return out
}

var sortNames = []string{"needs you", "age", "repo"}

// lead draws the chain connector and order digit: ┌① └② (┆ when inferred).
func lead(p model.PR, first, last bool) string {
	if p.Pair == nil {
		return "   "
	}
	c := "├"
	switch {
	case p.Pair.Inferred:
		c = "┆"
	case first:
		c = "┌"
	case last:
		c = "└"
	}
	return c + deck.OrderMark(p) + " "
}

func chainPos(prs []model.PR, i int) (first, last bool) {
	p := prs[i]
	if p.Pair == nil {
		return false, false
	}
	first = i == 0 || prs[i-1].Pair == nil || prs[i-1].Pair.ID != p.Pair.ID
	last = i == len(prs)-1 || prs[i+1].Pair == nil || prs[i+1].Pair.ID != p.Pair.ID
	return
}

func (m *Model) stateLabel(p model.PR) string {
	v := m.view
	switch st := v.StateOf(p); st {
	case deck.StWaiting:
		if sib, ok := v.Sibling(p); ok {
			return "waits " + deck.OrderMark(sib)
		}
		return "waits"
	case deck.StScheduled:
		if sch, ok := v.State.Schedules[p.Key()]; ok {
			return "◷ " + shortWhen(v.Now, sch.At)
		}
		return "◷"
	case deck.StFail:
		if m.rerunning[p.Key()] {
			return "⟳ CI rerun"
		}
		return "CI ✗"
	case deck.StRunning:
		return "CI ◌"
	default:
		return st
	}
}

func (m *Model) next(p model.PR) string {
	if m.rerunning[p.Key()] && p.CI == model.CIFail {
		return "⟳ CI rerunning, waiting for the new runs"
	}
	return m.view.Next(p)
}

func shortWhen(now, t time.Time) string {
	t = t.In(now.Location())
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return t.Format("15:04")
	}
	return t.Format("Mon 15:04")
}

func (m *Model) age(p model.PR) string {
	if p.CreatedAt.IsZero() {
		return ""
	}
	return review.Age(m.view.Now.Sub(p.CreatedAt))
}

// planMark is the list mark for terraform plans with changes ("Δ 2 ws "),
// red when one of them destroys something.
func planMark(p model.PR) (string, int) {
	if len(p.Plans) == 0 {
		return "", 0
	}
	st := sYellow
	for _, pl := range p.Plans {
		if pl.Destroy > 0 {
			st = sRed
		}
	}
	t := fmt.Sprintf("Δ %d ws ", len(p.Plans))
	return st.Render(t), runewidth.StringWidth(t)
}

// nextCell is the next action, after the plan mark when there is one.
func (m *Model) nextCell(p model.PR, st lipgloss.Style, w int) string {
	mark, mw := planMark(p)
	return mark + cell(st, m.next(p), max(1, w-mw))
}

func (m *Model) flagMark(p model.PR) string {
	if m.view.Flagged(p) {
		return sYellow.Render("⚑")
	}
	return " "
}

func (m *Model) refText(p model.PR) (string, bool) {
	return p.Ref(), m.view.Production(p)
}

func (m *Model) renderRef(p model.PR, w int, selected bool) string {
	ref, prod := m.refText(p)
	st := sPlain
	if selected {
		st = sBold
	}
	if !prod {
		return cell(st, ref, w)
	}
	r := ref
	if runewidth.StringWidth(r) > w-3 {
		r = runewidth.Truncate(r, w-3, "…")
	}
	return st.Render(r) + sOrange.Render(" ▲") + strings.Repeat(" ", max(1, w-runewidth.StringWidth(r)-2))
}

// boardLines renders the board layout body.
func (m *Model) boardLines(width int) []line {
	v := m.view
	narrow := width < 100
	refW := 24
	envW, stW, revW, ageW := 6, 12, 16, 4
	if narrow {
		envW, revW = 0, 2
	}
	if width < 70 {
		ageW = 0
	}
	nextW := width - 3 - 2 - refW - envW - stW - revW - ageW - 2 - 1
	if nextW < 10 {
		nextW = 10
	}
	var out []line
	hdr := sDim.Render(fit("", 5) + fit("PR", refW) + fit("env", envW) + fit("state", stW) + fit("review", revW) + fit("next", nextW) + fit("age", ageW))
	out = append(out, line{text: hdr})
	for _, g := range m.groups() {
		fold := "▾"
		if m.collapsed[g.id] {
			fold = "▸"
		}
		topic := g.topic
		keyPart := ""
		if g.ticket != "" {
			keyPart = g.ticket
			if g.status != "" {
				keyPart += " · " + g.status
			}
		} else if g.id != deck.NoGroup {
			keyPart = "no ticket"
		}
		maxTopic := width - 4 - runewidth.StringWidth(keyPart) - 2
		t := fit(topic, min(maxTopic, runewidth.StringWidth(topic)))
		text := sDim.Render(fold+" ") + sTopic.Render(t)
		jx := [2]int{}
		if keyPart != "" {
			if g.ticket != "" {
				start := 2 + runewidth.StringWidth(t) + 1
				jx = [2]int{start, start + len(g.ticket)}
			}
			text += " " + sDim.Render(keyPart)
		}
		if m.collapsed[g.id] {
			text += sDim.Render(fmt.Sprintf("  (%d)", len(g.prs)))
		}
		out = append(out, line{text: text, group: g.id, jiraX: jx})
		if m.collapsed[g.id] {
			continue
		}
		for i, p := range g.prs {
			first, last := chainPos(g.prs, i)
			sel := p.Key() == m.cursor
			st := v.StateOf(p)
			ri := v.Review(p)
			revText := ri.Glyph + " " + ri.Short
			if narrow {
				revText = ri.Glyph
			}
			row := sDim.Render(lead(p, first, last)) + stateStyle(st).Render(fit(deck.Glyph(st), 2)) +
				m.renderRef(p, refW, sel) + cell(sDim, p.Env, envW) + cell(stateStyle(st), m.stateLabel(p), stW) +
				cell(reviewStyle(ri.Kind), revText, revW) + m.nextCell(p, sDim, nextW) + cell(sDim, m.age(p), ageW) + m.flagMark(p)
			if sel {
				row = sSel.Render("▌") + row
			} else {
				row = " " + row
			}
			out = append(out, line{text: row, key: p.Key()})
		}
	}
	return out
}

// recallLines renders the recall table (two lines per PR).
func (m *Model) recallLines(width int) []line {
	v := m.view
	refW, topicW, ageW := 23, m.dk.Cfg.Jira.ShortLen+2, 5
	nextW := width - 2 - 3 - 2 - refW - topicW - ageW - 1
	if nextW < 10 {
		nextW = 10
	}
	var out []line
	out = append(out, line{text: sDim.Render(fit("⚑", 2)+fit("", 5)+fit("PR · purpose", refW)+fit("topic", topicW)+fit("age", ageW)) + sDim.Render("next action")})
	rows := m.recallRows()
	for i, p := range rows {
		first, last := chainPos(rows, i)
		sel := p.Key() == m.cursor
		st := v.StateOf(p)
		ri := v.Review(p)
		l1 := m.flagMark(p) + " " + sDim.Render(lead(p, first, last)) + stateStyle(st).Render(fit(deck.Glyph(st), 2)) +
			m.renderRef(p, refW, sel) + cell(sCyan, v.TopicShort(p), topicW) + cell(sDim, m.age(p), ageW) + m.nextCell(p, sPlain, nextW)
		cont := "   "
		if p.Pair != nil && !last {
			if p.Pair.Inferred {
				cont = "┆  "
			} else {
				cont = "│  "
			}
		}
		purpose := p.Purpose
		if purpose == "" && p.Why != nil {
			purpose = p.Why.Prompt
		}
		if purpose == "" {
			purpose = p.Title
		}
		l2 := "  " + sDim.Render(cont) + "  " + cell(reviewStyle(ri.Kind), ri.Glyph+" "+ri.Short, 17) + cell(sDim, purpose, width-2-3-2-17-1)
		mark := " "
		if sel {
			mark = sSel.Render("▌")
		}
		out = append(out, line{text: mark + l1, key: p.Key()}, line{text: mark + l2, key: p.Key()})
	}
	return out
}

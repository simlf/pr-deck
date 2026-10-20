package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/simlf/pr-deck/internal/deck"
	"github.com/simlf/pr-deck/internal/model"
	"github.com/simlf/pr-deck/internal/review"
)

// View implements tea.Model.
func (m *Model) View() string {
	w, h := m.width, m.height
	if w < 40 {
		w = 40
	}
	if h < 12 {
		h = 12
	}
	if m.mode == modeHelp {
		return m.help(w)
	}
	header := m.header(w)
	footer := m.footer(w)
	drawerSide := m.layout == Recall && w >= 120
	drawerW := max(44, w/3)
	bodyW := w
	if drawerSide {
		bodyW = w - drawerW - 2
	}
	var body []line
	if m.layout == Recall {
		body = m.recallLines(bodyW)
	} else {
		body = m.boardLines(bodyW)
	}
	var why []string
	if p, ok := m.selected(); ok {
		if drawerSide {
			why = m.why(p, drawerW, true)
		} else {
			why = m.why(p, w-2, m.whyOpen)
		}
	}
	avail := h - len(header) - len(footer)
	if !drawerSide {
		avail -= len(why) + 1
	}
	if avail < 3 {
		avail = 3
	}
	m.lines = body
	m.bodyTop = len(header)
	m.scrollTo(avail)
	var out []string
	out = append(out, header...)
	end := min(len(body), m.offset+avail)
	visible := body[m.offset:end]
	for i := 0; i < avail; i++ {
		left := ""
		if i < len(visible) {
			left = visible[i].text
		}
		if drawerSide {
			right := ""
			if i < len(why) {
				right = why[i]
			}
			out = append(out, padLine(left, bodyW)+sDim.Render("│ ")+right)
		} else {
			out = append(out, left)
		}
	}
	if !drawerSide {
		out = append(out, sDim.Render(strings.Repeat("─", w)))
		out = append(out, why...)
	}
	out = append(out, footer...)
	return strings.Join(out, "\n")
}

// scrollTo keeps the cursor's lines inside the viewport.
func (m *Model) scrollTo(avail int) {
	first, last := -1, -1
	for i, ln := range m.lines {
		if ln.key == m.cursor && m.cursor != "" {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first >= 0 {
		// keep the group header visible when possible
		if first > 0 && m.lines[first-1].group != "" {
			first--
		}
		if first < m.offset {
			m.offset = first
		}
		if last >= m.offset+avail {
			m.offset = last - avail + 1
		}
	}
	if m.offset > len(m.lines)-avail {
		m.offset = max(0, len(m.lines)-avail)
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *Model) header(w int) []string {
	v := m.view
	c := v.Count()
	counts := fmt.Sprintf("%d open %s %s %s %s %s",
		c.Open, sGreen.Render(fmt.Sprintf("●%d", c.Ready)), sRed.Render(fmt.Sprintf("✗%d", c.Fail)),
		sMagenta.Render(fmt.Sprintf("◇%d", c.Waiting)), sYellow.Render(fmt.Sprintf("◔%d", c.Review)), sYellow.Render(fmt.Sprintf("⚑%d", c.Flagged)))
	if c.Scheduled > 0 {
		counts += " " + sCyan.Render(fmt.Sprintf("◷%d", c.Scheduled))
	}
	title := sCyan.Bold(true).Render("pr-deck") + "  " + counts + sDim.Render("  · "+m.layout)
	if m.layout == Recall {
		title += sDim.Render(" · sort: " + sortNames[m.sort] + " (s)")
	}
	if m.filter != "" {
		title += sYellow.Render("  /" + m.filter)
	}
	right := ""
	switch {
	case m.busy != "":
		right = sYellow.Render(m.busy)
	case !m.loaded:
		right = sYellow.Render("no data yet")
	default:
		age := v.Now.Sub(v.Snap.At).Round(1e9)
		right = sDim.Render("⟳ " + review.Age(age))
		if age > m.dk.Cfg.Deck.StaleWarn.Duration && m.dk.Cfg.Deck.StaleWarn.Duration > 0 {
			right = sYellow.Render("stale " + review.Age(age))
		}
	}
	lines := []string{padLine(title, w-lipgloss.Width(right)) + right}
	if wd := v.Window(); wd.Enabled {
		st := sYellow
		if wd.Open {
			st = sGreen
		}
		lines = append(lines, st.Render(wd.Describe(v.Now)))
	}
	return lines
}

func (m *Model) footer(w int) []string {
	var msg string
	switch m.mode {
	case modeConfirm:
		msg = sMsg.Render(fit(m.plan.Message, max(10, w-14))) + "  " + sGreen.Render("[y]es") + " " + sRed.Render("[n]o")
	case modeAlias:
		msg = sMsg.Render("short topic: ") + fit(string(m.input), max(10, w-60)) + "▏" + sDim.Render("  enter save · empty resets · esc cancel")
	case modeFilter:
		msg = sMsg.Render("/") + string(m.input) + "▏" + sDim.Render("  enter keep · esc clear")
	default:
		if m.msgErr {
			msg = sErr.Render(fit(m.msg, w))
		} else if m.msg != "" {
			msg = sMsg.Render(fit(m.msg, w))
		}
	}
	hint := "j/k move · enter open · w why · m merge · y copy · P review req · r rerun · f reviewed · t topic · p pair · R " +
		map[string]string{Board: "recall", Recall: "board"}[m.layout] + " · ? help · q quit"
	return []string{msg, sDim.Render(fit(hint, w))}
}

// runewidthOf is the visible width of a styled string.
func runewidthOf(s string) int { return lipgloss.Width(s) }

// why renders the drawer: topic first, then the PR, purpose, the prompt
// that created it, pair, review and next action.
func (m *Model) why(p model.PR, w int, full bool) []string {
	v := m.view
	var out []string
	add := func(s string) { out = append(out, s) }
	topic := v.Topic(p)
	keyPart := ""
	if p.Ticket != "" {
		keyPart = p.Ticket
		if p.TicketStatus != "" {
			keyPart += " · " + p.TicketStatus
		}
	} else {
		keyPart = "(no ticket)"
	}
	for i, l := range wrap(topic, w-runewidthOf(keyPart)-1) {
		if i == 0 {
			add(sCyan.Bold(true).Render(l) + " " + sDim.Render(keyPart))
		} else {
			add(sCyan.Bold(true).Render(l))
		}
	}
	meta := sBold.Render(p.Ref()) + sDim.Render(" · "+m.age(p)+" old")
	var marks []string
	if v.Critical(p) {
		marks = append(marks, sYellow.Render("critical"))
	}
	if v.Production(p) {
		marks = append(marks, sOrange.Render("▲ "+v.Cfg.ProductionLabel))
	}
	if len(marks) > 0 {
		tail := sDim.Render(" · ") + strings.Join(marks, sDim.Render(" · "))
		if lipgloss.Width(meta+tail) <= w {
			meta += tail
			add(meta)
		} else {
			add(meta)
			add(strings.Join(marks, sDim.Render(" · ")))
		}
	} else {
		add(meta)
	}
	for i, pl := range p.Plans {
		lead := "  "
		if m.planSel(p) == i {
			lead = sSel.Render("▸") + " "
		}
		add(lead + sYellow.Render("Δ "+pl.Workspace) + " " + planCounts(pl))
	}
	purpose := p.Purpose
	if purpose == topic {
		purpose = ""
	}
	if !full {
		if purpose == "" && p.Why != nil {
			purpose = "you asked: \"" + p.Why.Prompt + "\""
		}
		if purpose != "" {
			add(sDim.Render("why ") + fit(purpose, w-4))
		}
		add(sDim.Render("next ") + v.Next(p))
		return clip(out, w)
	}
	if purpose != "" {
		for i, l := range wrap("Purpose: "+purpose, w) {
			if i == 0 {
				l = sDim.Render("Purpose:") + strings.TrimPrefix(l, "Purpose:")
			}
			add(l)
		}
	}
	if p.Why != nil {
		ls := wrap("↳ you asked: \""+p.Why.Prompt+"\"", w)
		if len(ls) > 5 {
			ls = append(ls[:5], "  …")
		}
		for _, l := range ls {
			add(sYellow.Render(l))
		}
		ctx := p.Why.Cwd
		if !p.Why.At.IsZero() {
			ctx += " · " + p.Why.At.Local().Format("Mon 2 Jan 15:04")
		}
		add(sDim.Render("  " + fit(ctx, w-2)))
	} else if p.Purpose == "" {
		add(sDim.Render("no Purpose: line and no agent transcript for this PR"))
	}
	if p.Pair != nil {
		chain, src := m.chain(p)
		if lipgloss.Width(chain+src)+5 <= w {
			add(sDim.Render("pair ") + chain + src)
		} else {
			add(sDim.Render("pair ") + chain)
			add(sDim.Render("    ") + src)
		}
	}
	ri := v.Review(p)
	for i, l := range wrap(ri.Long, w-9) {
		if i == 0 {
			add(sDim.Render("review ") + reviewStyle(ri.Kind).Render(ri.Glyph) + " " + l)
		} else {
			add("         " + l)
		}
	}
	add(sDim.Render("next ") + v.Next(p))
	return clip(out, w)
}

func planCounts(pl model.Plan) string {
	if pl.Add+pl.Change+pl.Destroy == 0 {
		return sDim.Render(pl.Summary)
	}
	d := sDim
	if pl.Destroy > 0 {
		d = sRed
	}
	return sGreen.Render(fmt.Sprintf("+%d", pl.Add)) + " " + sYellow.Render(fmt.Sprintf("~%d", pl.Change)) + " " + d.Render(fmt.Sprintf("-%d", pl.Destroy))
}

// clip cuts styled lines to w visible cells.
func clip(lines []string, w int) []string {
	st := lipgloss.NewStyle().MaxWidth(w)
	for i, l := range lines {
		if lipgloss.Width(l) > w {
			lines[i] = st.Render(l)
		}
	}
	return lines
}

func (m *Model) chain(p model.PR) (string, string) {
	v := m.view
	var members []model.PR
	for _, x := range v.Snap.PRs {
		if x.Pair != nil && x.Pair.ID == p.Pair.ID {
			members = append(members, x)
		}
	}
	sortChains(members)
	var parts []string
	for _, x := range members {
		st := v.StateOf(x)
		parts = append(parts, sMagenta.Render(deck.OrderMark(x))+stateStyle(st).Render(deck.Glyph(st))+" "+nonEmpty(x.Env, x.RepoName())+fmt.Sprintf(" #%d", x.Number))
	}
	src := map[string]string{"trailer": "Merge-after trailer", "manual": "paired by you", "heuristic": "┆ inferred (same ticket + branch stem) · p to confirm"}[p.Pair.Source]
	if p.Pair.Inferred {
		src = "┆ inferred (same ticket + branch stem) · p to confirm"
	}
	return strings.Join(parts, sDim.Render(" → ")), sDim.Render(" · " + src)
}

func (m *Model) help(w int) string {
	rows := [][2]string{
		{"j/k ↑/↓", "move · g/G top/bottom · wheel scrolls"},
		{"enter / o", "open the PR in the browser (double-click a row too)"},
		{"tab", "step through the PR's terraform plans with changes; enter opens the selected plan"},
		{"J", "open the Jira ticket (click the grey key in a group header too)"},
		{"w", "why: full purpose, the prompt that created the PR, pair, review"},
		{"y / Y", "copy link / copy \"topic (KEY) · PR · purpose\" for chat"},
		{"P", "copy a review request (or nudge) for " + m.channel()},
		{"r", "rerun failed CI jobs"},
		{"m", "merge: pair order first, then CI, review and prod windows; outside a window it offers a schedule; on a scheduled PR it cancels"},
		{"p", "pair: mark which PR must merge first (u unpairs)"},
		{"f", "reviewed: clear the ⚑ untouched-today flag until tomorrow"},
		{"t", "rename the topic's short form"},
		{"R / s", "switch board ↔ recall / cycle recall sort"},
		{"h / l", "fold / unfold topic groups (click a header too)"},
		{"/", "filter by topic, repo, ticket or title"},
		{"ctrl+r", "refresh now · q / esc quit"},
	}
	var b strings.Builder
	b.WriteString(sCyan.Bold(true).Render("pr-deck keys") + "\n\n")
	for _, r := range rows {
		b.WriteString(sBold.Render(fit(r[0], 12)) + " " + fit(r[1], w-14) + "\n")
	}
	b.WriteString("\n" + sDim.Render("glyphs: ● ready  Δ terraform plans with changes  ✓ merged  ○ review  ✗ CI/conflict  ◌ running  ◇ waits on sibling  ◦ draft  ◷ scheduled  ⚑ untouched today  ▲ production label"))
	b.WriteString("\n" + sDim.Render("review: ✓ approved  ✎ changes  ◌ posted/waiting  ◔ overdue  ○ not posted") + "\n\n" + sDim.Render("any key closes"))
	return b.String()
}

// Package prview is a read-only PR diff pager: move to a file:line and send
// a note about it to firstmate. It never writes to GitHub.
package prview

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/simlf/pr-deck/internal/config"
)

// Line is one diff line; File and New place it in the new version of the file.
type Line struct {
	Text string
	File string
	New  int
}

var hunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)`)

// Parse splits a unified diff into lines that know their file:line. A
// removed line points at the new line where it was.
func Parse(diff string) []Line {
	var out []Line
	file, n := "", 0
	for _, t := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		l := Line{Text: t}
		switch {
		case strings.HasPrefix(t, "diff --git "):
			if i := strings.LastIndex(t, " b/"); i >= 0 {
				file = t[i+3:]
			}
			n = 0
		case strings.HasPrefix(t, "+++ b/"):
			file = t[6:]
		case strings.HasPrefix(t, "+++ "), strings.HasPrefix(t, "--- "), strings.HasPrefix(t, `\`):
		case hunkRe.MatchString(t):
			n, _ = strconv.Atoi(hunkRe.FindStringSubmatch(t)[1])
			l.New = n
		case n > 0 && strings.HasPrefix(t, "-"):
			l.New = n
		case n > 0:
			l.New = n
			n++
		}
		l.File = file
		out = append(out, l)
	}
	return out
}

// Note sends text to firstmate's captain inbox with fm-inbox.sh.
func Note(fm config.Firstmate, text string) error {
	if fm.Home == "" || fm.Bin == "" {
		return fmt.Errorf("notes are off: set [firstmate] home and bin in the pr-deck config")
	}
	cmd := exec.Command(filepath.Join(fm.Bin, "fm-inbox.sh"), "note", "--", text)
	cmd.Env = append(os.Environ(), "FM_HOME="+fm.Home)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fm-inbox.sh note: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Model is the pager.
type Model struct {
	URL    string
	Lines  []Line
	Send   func(string) error // nil turns notes off
	cursor int
	offset int
	width  int
	height int
	note   []rune
	typing bool
	msg    string
}

// New builds the pager on url's diff.
func New(url, diff string, send func(string) error) *Model {
	return &Model{URL: url, Lines: Parse(diff), Send: send, width: 120, height: 40}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// Where is the cursor's file:line.
func (m *Model) Where() string {
	if len(m.Lines) == 0 {
		return ""
	}
	l := m.Lines[m.cursor]
	if l.New == 0 {
		return l.File
	}
	return fmt.Sprintf("%s:%d", l.File, l.New)
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if m.typing {
			return m, m.typeKey(msg)
		}
		page := max(1, m.height-4)
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			m.move(1)
		case "k", "up":
			m.move(-1)
		case "ctrl+d", "pgdown", " ":
			m.move(page / 2)
		case "ctrl+u", "pgup":
			m.move(-page / 2)
		case "g", "home":
			m.move(-len(m.Lines))
		case "G", "end":
			m.move(len(m.Lines))
		case "n":
			m.jumpFile(1)
		case "N":
			m.jumpFile(-1)
		case "c":
			if m.Send == nil {
				m.msg = "notes are off: set [firstmate] home and bin in the pr-deck config"
			} else if m.Where() != "" {
				m.typing, m.note, m.msg = true, nil, ""
			}
		}
	}
	return m, nil
}

func (m *Model) typeKey(k tea.KeyMsg) tea.Cmd {
	switch k.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.typing, m.msg = false, "note dropped"
	case tea.KeyEnter:
		m.typing = false
		text := strings.TrimSpace(string(m.note))
		if text == "" {
			m.msg = "empty note dropped"
			return nil
		}
		full := m.URL + " " + m.Where() + " " + text
		if err := m.Send(full); err != nil {
			m.msg = err.Error()
		} else {
			m.msg = "sent to firstmate: " + full
		}
	case tea.KeyBackspace:
		if len(m.note) > 0 {
			m.note = m.note[:len(m.note)-1]
		}
	case tea.KeySpace:
		m.note = append(m.note, ' ')
	case tea.KeyRunes:
		m.note = append(m.note, k.Runes...)
	}
	return nil
}

func (m *Model) move(d int) {
	m.cursor = max(0, min(len(m.Lines)-1, m.cursor+d))
}

func (m *Model) jumpFile(d int) {
	cur := m.Lines[m.cursor].File
	for i := m.cursor + d; i >= 0 && i < len(m.Lines); i += d {
		if strings.HasPrefix(m.Lines[i].Text, "diff --git ") && (d > 0 || m.Lines[i].File != cur) {
			m.cursor = i
			return
		}
	}
}

var (
	sAdd  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	sDel  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	sHunk = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	sFile = lipgloss.NewStyle().Bold(true)
	sDim  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	sSel  = lipgloss.NewStyle().Reverse(true)
)

// View implements tea.Model.
func (m *Model) View() string {
	body := max(1, m.height-3)
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+body {
		m.offset = m.cursor - body + 1
	}
	var b strings.Builder
	b.WriteString(sFile.Render(runewidth.Truncate(m.URL+" · "+m.Where(), m.width, "…")) + "\n")
	for i := m.offset; i < min(len(m.Lines), m.offset+body); i++ {
		l := m.Lines[i]
		num := "     "
		if l.New > 0 && !hunkRe.MatchString(l.Text) {
			num = fmt.Sprintf("%5d", l.New)
		}
		mark := " "
		if i == m.cursor {
			mark = sSel.Render("▌")
		}
		b.WriteString(mark + sDim.Render(num) + " " + style(l.Text).Render(runewidth.Truncate(l.Text, max(1, m.width-8), "…")) + "\n")
	}
	switch {
	case m.typing:
		b.WriteString("note on " + m.Where() + ": " + string(m.note) + "█\n")
	case m.msg != "":
		b.WriteString(sDim.Render(runewidth.Truncate(m.msg, m.width, "…")) + "\n")
	default:
		b.WriteString("\n")
	}
	b.WriteString(sDim.Render("j/k move · ctrl+d/u page · n/N next/prev file · c note to firstmate · q quit"))
	return b.String()
}

func style(t string) lipgloss.Style {
	switch {
	case strings.HasPrefix(t, "diff --git "), strings.HasPrefix(t, "+++ "), strings.HasPrefix(t, "--- "):
		return sFile
	case strings.HasPrefix(t, "@@"):
		return sHunk
	case strings.HasPrefix(t, "+"):
		return sAdd
	case strings.HasPrefix(t, "-"):
		return sDel
	}
	return lipgloss.NewStyle()
}

package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"prdeck/internal/deck"
)

// Basic ANSI colors so pr-deck follows the user's terminal theme.
var (
	cGreen   = lipgloss.Color("2")
	cRed     = lipgloss.Color("1")
	cYellow  = lipgloss.Color("3")
	cBlue    = lipgloss.Color("4")
	cMagenta = lipgloss.Color("5")
	cCyan    = lipgloss.Color("6")
	cOrange  = lipgloss.Color("208")
	cDim     = lipgloss.Color("8")

	sPlain   = lipgloss.NewStyle()
	sDim     = lipgloss.NewStyle().Foreground(cDim)
	sBold    = lipgloss.NewStyle().Bold(true)
	sTopic   = lipgloss.NewStyle().Bold(true)
	sCyan    = lipgloss.NewStyle().Foreground(cCyan)
	sYellow  = lipgloss.NewStyle().Foreground(cYellow)
	sGreen   = lipgloss.NewStyle().Foreground(cGreen)
	sRed     = lipgloss.NewStyle().Foreground(cRed)
	sMagenta = lipgloss.NewStyle().Foreground(cMagenta)
	sOrange  = lipgloss.NewStyle().Foreground(cOrange)
	sBlue    = lipgloss.NewStyle().Foreground(cBlue)
	sSel     = lipgloss.NewStyle().Reverse(true)
	sMsg     = lipgloss.NewStyle().Foreground(cYellow)
	sErr     = lipgloss.NewStyle().Foreground(cRed)
)

func stateStyle(st string) lipgloss.Style {
	switch st {
	case deck.StReady, deck.StMerged:
		return sGreen
	case deck.StFail, deck.StConflict:
		return sRed
	case deck.StRunning:
		return sYellow
	case deck.StWaiting:
		return sMagenta
	case deck.StScheduled:
		return sCyan
	case deck.StReview:
		return sBlue
	}
	return sDim
}

func reviewStyle(kind string) lipgloss.Style {
	switch kind {
	case "approved":
		return sGreen
	case "changes":
		return sRed
	case "not_posted", "overdue":
		return sYellow
	}
	return sDim
}

// fit truncates or pads plain text to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) > w {
		s = runewidth.Truncate(s, w, "…")
	}
	return runewidth.FillRight(s, w)
}

// cell styles plain text fitted to w cells, the last one a column gap.
func cell(st lipgloss.Style, s string, w int) string {
	if w <= 1 {
		return st.Render(fit(s, w))
	}
	return st.Render(fit(s, w-1)) + " "
}

// wrap breaks text into lines of at most w cells on word boundaries.
func wrap(s string, w int) []string {
	if w < 8 {
		w = 8
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			for runewidth.StringWidth(word) > w {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				cut := runewidth.Truncate(word, w, "")
				out = append(out, cut)
				word = word[len(cut):]
			}
			switch {
			case line == "":
				line = word
			case runewidth.StringWidth(line)+1+runewidth.StringWidth(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// padLine pads a styled line to w visible cells (for drawers side by side).
func padLine(s string, w int) string {
	vis := lipgloss.Width(s)
	if vis >= w {
		return s
	}
	return s + strings.Repeat(" ", w-vis)
}

// Package render turns a branch map into text, JSON, diagrams, and images.
package render

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/j-a-man/visual-branches/internal/theme"
)

// Styles are the terminal styles derived from a theme.
type Styles struct {
	Text, Subtle, Muted, Tree         lipgloss.Style
	Accent, Success, Warning, Danger  lipgloss.Style
	Info, Merged, Agent               lipgloss.Style
	Current, TrunkName, Header, Title lipgloss.Style
}

func fg(c string, fallback lipgloss.Style) lipgloss.Style {
	if c == "" {
		return fallback
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c))
}

// NewStyles builds styles for a theme. Roles without a color degrade to
// bold or faint text, which is how the mono theme works.
func NewStyles(t theme.Theme) Styles {
	plain := lipgloss.NewStyle()
	faint := lipgloss.NewStyle().Faint(true)
	bold := lipgloss.NewStyle().Bold(true)
	s := Styles{
		Text:    fg(t.Text, plain),
		Subtle:  fg(t.Subtle, plain),
		Muted:   fg(t.Muted, faint),
		Tree:    fg(t.Tree, faint),
		Accent:  fg(t.Accent, bold),
		Success: fg(t.Success, plain),
		Warning: fg(t.Warning, bold),
		Danger:  fg(t.Danger, bold),
		Info:    fg(t.Info, plain),
		Merged:  fg(t.Merged, faint),
		Agent:   fg(t.Agent, faint),
	}
	s.Current = s.Accent.Bold(true)
	s.TrunkName = s.Text.Bold(true)
	s.Header = s.Accent.Bold(true)
	s.Title = s.Subtle.Bold(true)
	return s
}

// Icons is a glyph set.
type Icons struct {
	Tee, Last, Pipe, Blank string
	Pass, Fail, Pending    string
	Ahead, Behind          string
	High, Medium, Ready    string
	Low, Sep, Ellipsis     string
	Current                string
}

// UnicodeIcons is the default glyph set.
var UnicodeIcons = Icons{
	Tee: "├─ ", Last: "└─ ", Pipe: "│  ", Blank: "   ",
	Pass: "✓", Fail: "✗", Pending: "●",
	Ahead: "↑", Behind: "↓",
	High: "●", Medium: "●", Ready: "✓", Low: "○",
	Sep: " · ", Ellipsis: "…", Current: "*",
}

// ASCIIIcons works in any terminal and font.
var ASCIIIcons = Icons{
	Tee: "|- ", Last: "`- ", Pipe: "|  ", Blank: "   ",
	Pass: "ok", Fail: "x", Pending: "~",
	Ahead: "+", Behind: "-",
	High: "!", Medium: "*", Ready: "+", Low: "-",
	Sep: " - ", Ellipsis: "...", Current: "*",
}

// IconSet returns the icons for a config value.
func IconSet(name string) Icons {
	if name == "ascii" {
		return ASCIIIcons
	}
	return UnicodeIcons
}

// Age renders a duration compactly: now, 5m, 3h, 4d, 2w, 5mo, 1y.
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	}
	return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
}

// Truncate shortens s to at most w cells, ending with ellipsis.
func Truncate(s string, w int, ellipsis string) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	ew := lipgloss.Width(ellipsis)
	if w <= ew {
		ellipsis, ew = "", 0
	}
	var b strings.Builder
	width := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if width+rw > w-ew {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	return b.String() + ellipsis
}

// ShortPath shortens a slash-separated path to at most w cells by replacing
// leading directories with an ellipsis, keeping the last element whole when
// possible: .claude/worktrees/refactor-db-pool -> …/refactor-db-pool.
func ShortPath(p string, w int, ellipsis string) string {
	if lipgloss.Width(p) <= w {
		return p
	}
	parts := strings.Split(p, "/")
	for i := 1; i < len(parts); i++ {
		s := ellipsis + "/" + strings.Join(parts[i:], "/")
		if lipgloss.Width(s) <= w {
			return s
		}
	}
	return Truncate(parts[len(parts)-1], w, ellipsis)
}

// pad right-pads a styled string to width cells.
func pad(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// padLeft left-pads a styled string to width cells.
func padLeft(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return strings.Repeat(" ", width-w) + s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

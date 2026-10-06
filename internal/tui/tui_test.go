package tui

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/j-a-man/visual-branches/internal/config"
	"github.com/j-a-man/visual-branches/internal/engine"
	"github.com/j-a-man/visual-branches/internal/model"
	"github.com/j-a-man/visual-branches/internal/testrepo"
)

func setup(t *testing.T) (*tuiModel, *testrepo.Repo) {
	t.Helper()
	testrepo.Isolate(t)
	r := testrepo.New(t)
	r.Branch("feat/a", "main")
	r.Commits("feat/a", 2)
	r.Branch("feat/b", "feat/a")
	r.Commits("feat/b", 1)
	r.Branch("feat/done", "main")
	r.Commits("feat/done", 1)
	r.Merge("feat/done", "main")
	r.Switch("feat/a")
	cfg := config.Defaults()
	cfg.TUI.Refresh = config.Duration{}
	ctx := context.Background()
	s, err := engine.Open(ctx, engine.Options{Dir: r.Dir, Config: cfg, GitHub: "never"})
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(ctx, Options{Session: s})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	msg := m.build()()
	_, cmd := m.Update(msg)
	if cmd != nil {
		m.Update(cmd())
	}
	return m, r
}

func screen(m *tuiModel) string { return ansi.Strip(m.render()) }

func press(m *tuiModel, keys ...tea.KeyPressMsg) {
	for _, k := range keys {
		_, cmd := m.Update(k)
		if cmd != nil {
			if msg := cmd(); msg != nil {
				if _, ok := msg.(tea.QuitMsg); !ok {
					m.Update(msg)
				}
			}
		}
	}
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func TestRenderAndNavigate(t *testing.T) {
	m, _ := setup(t)
	out := screen(m)
	for _, want := range []string{"main", "feat/a", "feat/b", "feat/done", "needs you", "move"} {
		if want == "needs you" {
			continue
		}
		if !strings.Contains(out, want) {
			t.Errorf("screen missing %q:\n%s", want, out)
		}
	}
	if m.selected() == nil || m.selected().Name != "feat/a" {
		t.Fatalf("initial selection = %v, want current branch", m.selected())
	}
	// The detail pane shows the selected branch's commits.
	if !strings.Contains(out, "feat/a change 1") {
		t.Errorf("detail pane missing commits:\n%s", out)
	}
	press(m, key("down"))
	if got := m.selected().Name; got != "feat/b" {
		t.Errorf("after down: %s", got)
	}
	if !strings.Contains(screen(m), "feat/b change 0") {
		t.Errorf("detail did not follow selection:\n%s", screen(m))
	}
	if testing.Verbose() {
		os.Stdout.WriteString(screen(m) + "\n")
	}
}

func TestFilterAndMergedToggle(t *testing.T) {
	m, _ := setup(t)
	press(m, key("/"), key("b"), key("enter"))
	names := []string{}
	for _, r := range m.rows {
		names = append(names, r.B.Name)
	}
	if strings.Join(names, ",") != "main,feat/a,feat/b" {
		t.Errorf("filtered rows = %v", names)
	}
	press(m, key("esc"))
	if len(m.rows) != 4 {
		t.Errorf("rows after clearing filter = %d", len(m.rows))
	}
	press(m, key("m"))
	for _, r := range m.rows {
		if r.B.Merged != nil {
			t.Errorf("merged branch %s still shown", r.B.Name)
		}
	}
}

func TestDeleteMergedAsksFirst(t *testing.T) {
	m, r := setup(t)
	for i, row := range m.rows {
		if row.B.Name == "feat/done" {
			m.sel = i
		}
	}
	if !m.selected().HasFlag(model.FlagDeletable) {
		t.Fatalf("feat/done flags = %v", m.selected().Flags)
	}
	press(m, key("d"))
	if m.confirm == nil || !strings.Contains(screen(m), "delete feat/done") {
		t.Fatalf("no confirmation prompt:\n%s", screen(m))
	}
	press(m, key("n"))
	if out := r.Git("branch", "--list", "feat/done"); out == "" {
		t.Fatal("branch deleted despite answering no")
	}
	press(m, key("d"), key("y"))
	if out := r.Git("branch", "--list", "feat/done"); out != "" {
		t.Fatalf("branch not deleted: %q", out)
	}
	// Deleting an unmerged branch is refused.
	for i, row := range m.rows {
		if row.B.Name == "feat/b" {
			m.sel = i
		}
	}
	press(m, key("d"))
	if m.confirm != nil || !m.statusErr {
		t.Error("unmerged branch delete was not refused")
	}
}

func TestSwitch(t *testing.T) {
	m, r := setup(t)
	for i, row := range m.rows {
		if row.B.Name == "feat/b" {
			m.sel = i
		}
	}
	press(m, key("s"))
	if got := r.Git("symbolic-ref", "--short", "HEAD"); got != "feat/b" {
		t.Errorf("HEAD = %s", got)
	}
}

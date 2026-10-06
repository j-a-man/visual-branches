package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/j-a-man/visual-branches/internal/config"
	"github.com/j-a-man/visual-branches/internal/model"
	"github.com/j-a-man/visual-branches/internal/theme"
)

var update = flag.Bool("update", false, "rewrite golden files")

var now = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

// fixture is a hand-built map covering every visual state.
func fixture() *model.Map {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	m := &model.Map{
		SchemaVersion: model.SchemaVersion,
		State:         "abc123def0",
		Repo:          model.Repo{Name: "webapp", Head: "feat/ui", Trunks: []string{"main"}, Root: "/src/webapp"},
		GitHub:        model.GitHubStatus{Status: model.GitHubOK},
		Branches: []*model.Branch{
			{Name: "main", Trunk: true, Depth: 0, CommittedAt: ago(2 * time.Hour), Children: []string{"feat/api", "claude/db", "old/done"}},
			{Name: "feat/api", Parent: "main", ParentSource: model.SourceTrunk, ParentConfidence: model.High, Depth: 1, Ahead: 3, CommittedAt: ago(20 * time.Hour), Children: []string{"feat/ui"},
				PR:       &model.PR{Number: 12, State: model.PROpen, Review: model.ReviewApproved, CI: model.CIPass, Base: "main"},
				Upstream: &model.Upstream{Name: "origin/feat/api"}},
			{Name: "feat/ui", Head: true, Parent: "feat/api", ParentSource: model.SourcePR, ParentConfidence: model.High, Depth: 2, Ahead: 2, Behind: 1, CommittedAt: ago(26 * time.Hour),
				PR:       &model.PR{Number: 13, State: model.PROpen, Review: model.ReviewRequired, CI: model.CIFail, Base: "feat/api", Checks: []model.Check{{Name: "lint", Status: model.CIFail}, {Name: "test", Status: model.CIPass}}},
				Upstream: &model.Upstream{Name: "origin/feat/ui", Ahead: 1},
				Flags:    []string{model.FlagRestack, model.FlagUnpushed}},
			{Name: "claude/db", Agent: "claude", Parent: "main", ParentSource: model.SourceTrunk, ParentConfidence: model.High, Depth: 1, Ahead: 1, CommittedAt: ago(35 * time.Minute),
				Worktree: &model.Worktree{Path: "/src/webapp/.claude/worktrees/db", Display: ".claude/worktrees/db", Untracked: 2},
				Flags:    []string{model.FlagCollision, model.FlagDirty, model.FlagLocal}},
			{Name: "old/done", Parent: "main", ParentSource: model.SourceTrunk, ParentConfidence: model.High, Depth: 1, CommittedAt: ago(30 * 24 * time.Hour),
				Merged: &model.Merge{How: model.MergedSquash, Into: "main"}, Flags: []string{model.FlagDeletable}},
		},
		Attention: []model.Issue{
			{Kind: model.IssueCIFailing, Severity: model.SevHigh, Branch: "feat/ui", Message: "#13 CI failing: lint", Hint: "gh pr checks 13"},
			{Kind: model.IssueRestack, Severity: model.SevMedium, Branch: "feat/ui", Other: "feat/api", Message: "feat/ui is 1 behind its parent feat/api", Hint: "git rebase feat/api feat/ui"},
			{Kind: model.IssueReady, Severity: model.SevReady, Branch: "feat/api", Message: "#12 is ready to merge", Hint: "gh pr merge 12"},
			{Kind: model.IssueDeletable, Severity: model.SevLow, Branch: "old/done", Message: "old/done is merged (squash)", Hint: "git branch -D old/done"},
		},
	}
	fetched := now.Add(-3 * time.Minute)
	m.GitHub.FetchedAt = &fetched
	m.Index()
	return m
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/render -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from golden file.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func treeOptions(width int) TreeOptions {
	cfg := config.Defaults()
	th, _ := theme.Builtin(theme.Default)
	return TreeOptions{Config: cfg, Styles: NewStyles(th), Icons: UnicodeIcons, Width: width, Now: now, Plain: true, Filter: Filter{ShowMerged: true}, CurrentRoot: "/src/webapp"}
}

func TestTreeGolden(t *testing.T) {
	golden(t, "tree", ansi.Strip(Tree(fixture(), treeOptions(0))))
}

func TestTreeNarrow(t *testing.T) {
	out := ansi.Strip(Tree(fixture(), treeOptions(60)))
	for _, line := range strings.Split(out, "\n") {
		// Footer hints move to their own line when narrow; tree rows must fit.
		if strings.Contains(line, "├") || strings.Contains(line, "└") {
			if w := len([]rune(line)); w > 60 {
				t.Errorf("row wider than 60 columns (%d): %q", w, line)
			}
		}
	}
	golden(t, "tree-narrow", out)
}

func TestTreeASCII(t *testing.T) {
	o := treeOptions(0)
	o.Icons = ASCIIIcons
	out := ansi.Strip(Tree(fixture(), o))
	for _, r := range out {
		if r > 127 {
			t.Fatalf("non-ASCII rune %q in ascii output:\n%s", r, out)
		}
	}
}

func TestAgentGolden(t *testing.T) {
	golden(t, "agent", Agent(fixture(), AgentOptions{Now: now, Filter: Filter{ShowMerged: true}, Hints: true, Legend: true}))
}

func TestAgentBudget(t *testing.T) {
	m := fixture()
	full := Agent(m, AgentOptions{Now: now, Filter: Filter{ShowMerged: true}, Hints: true, Legend: true})
	for _, budget := range []int{400, 200, 120, 80} {
		out := Agent(m, AgentOptions{Now: now, Filter: Filter{ShowMerged: true}, Hints: true, Legend: true, MaxTokens: budget})
		if EstimateTokens(out) > budget && len(out) >= len(full) {
			t.Errorf("budget %d not applied: %d tokens", budget, EstimateTokens(out))
		}
		if !strings.Contains(out, "state=abc123def0") {
			t.Errorf("budget %d dropped the state token", budget)
		}
		if !strings.Contains(out, "#13 CI failing") {
			t.Errorf("budget %d dropped the most urgent issue:\n%s", budget, out)
		}
	}
}

func TestFocusAndHide(t *testing.T) {
	m := fixture()
	rows := Rows(m, Filter{Focus: "feat/ui", ShowMerged: true})
	var names []string
	for _, r := range rows {
		names = append(names, r.B.Name)
	}
	if strings.Join(names, ",") != "main,feat/api,feat/ui" {
		t.Errorf("focus rows = %v", names)
	}
	rows = Rows(m, Filter{ShowMerged: false, Hide: []string{"claude/*"}})
	names = nil
	for _, r := range rows {
		names = append(names, r.B.Name)
	}
	if strings.Join(names, ",") != "main,feat/api,feat/ui" {
		t.Errorf("hide rows = %v", names)
	}
}

func TestExports(t *testing.T) {
	th, _ := theme.Builtin(theme.Default)
	o := ExportOptions{Theme: th, Now: now, Filter: Filter{ShowMerged: true}}
	golden(t, "mermaid", Mermaid(fixture(), o))
	golden(t, "dot", DOT(fixture(), o))
	svg := SVG(fixture(), o)
	if !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, "feat/ui") || strings.Count(svg, "<path") != 4 {
		t.Errorf("unexpected svg:\n%s", svg)
	}
}

func TestStatusLine(t *testing.T) {
	th, _ := theme.Builtin(theme.Default)
	got := ansi.Strip(StatusLine(fixture(), "{branch}{parent}{ab}{pr}{ci}{review}{dirty}{attention}", NewStyles(th), UnicodeIcons))
	want := "feat/ui on feat/api ↑2↓1 #13 ✗ review · 2 need you"
	if got != want {
		t.Errorf("status line = %q, want %q", got, want)
	}
}

func TestLayout(t *testing.T) {
	m := fixture()
	lay := ComputeLayout(m, Rows(m, Filter{ShowMerged: true}), now)
	if len(lay.Nodes) != 5 || len(lay.Edges) != 4 {
		t.Fatalf("nodes=%d edges=%d", len(lay.Nodes), len(lay.Edges))
	}
	pos := map[string]LayoutNode{}
	for _, n := range lay.Nodes {
		pos[n.Name] = n
		if n.X < 0 || n.Y < 0 || n.X+n.W > lay.Width || n.Y+n.H > lay.Height {
			t.Errorf("%s outside the canvas: %+v (canvas %.0fx%.0f)", n.Name, n, lay.Width, lay.Height)
		}
	}
	if !(pos["main"].X < pos["feat/api"].X && pos["feat/api"].X < pos["feat/ui"].X) {
		t.Error("depth should increase left to right")
	}
	for _, a := range lay.Nodes {
		for _, b := range lay.Nodes {
			if a.Name < b.Name && a.X == b.X && a.Y < b.Y+b.H && b.Y < a.Y+a.H {
				t.Errorf("%s overlaps %s", a.Name, b.Name)
			}
		}
	}
	if pos["feat/ui"].Status != StatusDanger || pos["old/done"].Status != StatusMerged || pos["main"].Status != StatusTrunk {
		t.Errorf("statuses: %+v", pos)
	}
}

func TestShortPathAndTruncate(t *testing.T) {
	if got := ShortPath(".claude/worktrees/refactor-db-pool", 26, "…"); got != "…/refactor-db-pool" {
		t.Errorf("ShortPath = %q", got)
	}
	if got := Truncate("feat/a-very-long-branch-name", 10, "…"); got != "feat/a-ve…" {
		t.Errorf("Truncate = %q", got)
	}
	if got := Age(90 * time.Minute); got != "1h" {
		t.Errorf("Age = %q", got)
	}
}

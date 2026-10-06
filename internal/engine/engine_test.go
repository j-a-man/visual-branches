package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/j-a-man/visual-branches/internal/config"
	"github.com/j-a-man/visual-branches/internal/engine"
	"github.com/j-a-man/visual-branches/internal/model"
	"github.com/j-a-man/visual-branches/internal/testrepo"
)

func build(t *testing.T, r *testrepo.Repo, mutate ...func(*config.Config)) *model.Map {
	t.Helper()
	cfg := config.Defaults()
	for _, fn := range mutate {
		fn(cfg)
	}
	s, err := engine.Open(context.Background(), engine.Options{
		Dir:    r.Dir,
		Config: cfg,
		GitHub: "never",
		Now:    func() time.Time { return testrepo.Epoch.Add(10 * 24 * time.Hour) },
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func must(t *testing.T, m *model.Map, name string) *model.Branch {
	t.Helper()
	b := m.Branch(name)
	if b == nil {
		var names []string
		for _, x := range m.Branches {
			names = append(names, x.Name)
		}
		t.Fatalf("branch %q missing; have %v", name, names)
	}
	return b
}

func expectParent(t *testing.T, m *model.Map, name, parent string) *model.Branch {
	t.Helper()
	b := must(t, m, name)
	if b.Parent != parent {
		t.Errorf("%s: parent = %q (%s, %s), want %q", name, b.Parent, b.ParentSource, b.ParentConfidence, parent)
	}
	return b
}

func hasIssue(m *model.Map, kind, branch string) bool {
	for _, is := range m.Attention {
		if is.Kind == kind && (is.Branch == branch || is.Other == branch) {
			return true
		}
	}
	return false
}

func TestStackInference(t *testing.T) {
	testrepo.Isolate(t)
	r := testrepo.New(t)
	r.Branch("feat/a", "main")
	r.Commits("feat/a", 2)
	r.Branch("feat/b", "feat/a")
	r.Commits("feat/b", 1)
	r.Branch("feat/c", "main")
	r.Commits("feat/c", 3)
	r.Switch("main")

	m := build(t, r)
	if got := m.Repo.Trunks; len(got) != 1 || got[0] != "main" {
		t.Fatalf("trunks = %v", got)
	}
	a := expectParent(t, m, "feat/a", "main")
	b := expectParent(t, m, "feat/b", "feat/a")
	c := expectParent(t, m, "feat/c", "main")
	if a.Ahead != 2 || a.Behind != 0 {
		t.Errorf("feat/a ahead/behind = %d/%d", a.Ahead, a.Behind)
	}
	if b.Ahead != 1 || b.Depth != 2 {
		t.Errorf("feat/b ahead=%d depth=%d", b.Ahead, b.Depth)
	}
	if b.ParentConfidence != model.High {
		t.Errorf("feat/b confidence = %s", b.ParentConfidence)
	}
	if c.Ahead != 3 {
		t.Errorf("feat/c ahead = %d", c.Ahead)
	}
	if !must(t, m, "main").Head {
		t.Error("main should be HEAD")
	}
	// Tree order: main first, then depth-first.
	if m.Branches[0].Name != "main" {
		t.Errorf("first branch = %s", m.Branches[0].Name)
	}
}

func TestRestackAndParentMerged(t *testing.T) {
	testrepo.Isolate(t)
	r := testrepo.New(t)
	r.Branch("feat/a", "main")
	r.Commits("feat/a", 1)
	r.Branch("feat/b", "feat/a")
	r.Commits("feat/b", 1)
	r.Commits("feat/a", 1) // parent moves after the child was created

	m := build(t, r)
	b := expectParent(t, m, "feat/b", "feat/a")
	if b.Behind != 1 || !b.HasFlag(model.FlagRestack) {
		t.Errorf("feat/b behind=%d flags=%v", b.Behind, b.Flags)
	}
	if !hasIssue(m, model.IssueRestack, "feat/b") {
		t.Error("missing restack issue")
	}

	r.SquashMerge("feat/a", "main")
	m = build(t, r)
	a := must(t, m, "feat/a")
	if a.Merged == nil || a.Merged.How != model.MergedSquash {
		t.Fatalf("feat/a merged = %+v", a.Merged)
	}
	b = expectParent(t, m, "feat/b", "feat/a")
	if !b.HasFlag(model.FlagParentMerged) {
		t.Errorf("feat/b flags = %v", b.Flags)
	}
	for _, is := range m.Attention {
		if is.Kind == model.IssueParentMerged && !strings.Contains(is.Hint, "--onto main feat/a feat/b") {
			t.Errorf("parent merged hint = %q", is.Hint)
		}
	}
}

func TestMergedEmptyAndDeletable(t *testing.T) {
	testrepo.Isolate(t)
	r := testrepo.New(t)
	r.Branch("feat/done", "main")
	r.Commits("feat/done", 2)
	r.Merge("feat/done", "main")
	r.Branch("feat/fresh", "main") // no commits yet
	r.Branch("feat/rebased", "main")
	r.Commits("feat/rebased", 1)
	r.Switch("main")
	r.Git("cherry-pick", "feat/rebased")

	m := build(t, r)
	done := must(t, m, "feat/done")
	if done.Merged == nil || done.Merged.How != model.MergedAncestry {
		t.Errorf("feat/done merged = %+v", done.Merged)
	}
	if !done.HasFlag(model.FlagDeletable) {
		t.Errorf("feat/done flags = %v", done.Flags)
	}
	fresh := must(t, m, "feat/fresh")
	if fresh.Merged != nil || !fresh.Empty {
		t.Errorf("feat/fresh merged=%+v empty=%v", fresh.Merged, fresh.Empty)
	}
	rebased := must(t, m, "feat/rebased")
	if rebased.Merged == nil || (rebased.Merged.How != model.MergedSquash && rebased.Merged.How != model.MergedRebase) {
		t.Errorf("feat/rebased merged = %+v", rebased.Merged)
	}
}

func TestEqualTipsAndMidFork(t *testing.T) {
	testrepo.Isolate(t)
	r := testrepo.New(t)
	r.Branch("feat/a", "main")
	r.Commits("feat/a", 3)
	r.Branch("feat/copy", "feat/a") // same tip, created later
	r.Branch("feat/mid", "feat/a~1")
	r.Commits("feat/mid", 1)

	m := build(t, r)
	expectParent(t, m, "feat/a", "main")
	cp := expectParent(t, m, "feat/copy", "feat/a")
	if cp.ParentSource != model.SourceReflog {
		t.Errorf("feat/copy source = %s", cp.ParentSource)
	}
	mid := expectParent(t, m, "feat/mid", "feat/a")
	if mid.ParentConfidence != model.Medium {
		t.Errorf("feat/mid confidence = %s", mid.ParentConfidence)
	}
}

func TestConflictForecastAndCollision(t *testing.T) {
	testrepo.Isolate(t)
	r := testrepo.New(t)
	r.Commit("main", "shared.txt", "line one\nline two\n", "add shared")
	r.Branch("feat/x", "main")
	r.Commit("feat/x", "shared.txt", "line one from x\nline two\n", "x edits shared")
	r.Branch("feat/y", "main")
	r.Commit("feat/y", "shared.txt", "line one from y\nline two\n", "y edits shared")
	r.Branch("feat/z", "main")
	r.Commit("feat/z", "other.txt", "z\n", "z adds other")
	r.Commit("main", "shared.txt", "line one from main\nline two\n", "main edits shared")

	m := build(t, r)
	x := must(t, m, "feat/x")
	if x.Forecast == nil || x.Forecast.Clean {
		t.Fatalf("feat/x forecast = %+v", x.Forecast)
	}
	if len(x.Forecast.Files) != 1 || x.Forecast.Files[0] != "shared.txt" {
		t.Errorf("feat/x conflict files = %v", x.Forecast.Files)
	}
	if z := must(t, m, "feat/z"); z.Forecast == nil || !z.Forecast.Clean {
		t.Errorf("feat/z forecast = %+v", z.Forecast)
	}
	if !hasIssue(m, model.IssueCollision, "feat/x") || !x.HasFlag(model.FlagCollision) {
		t.Errorf("expected collision between feat/x and feat/y; flags=%v overlaps=%+v", x.Flags, m.Overlaps)
	}
	if len(m.Overlaps) != 1 {
		t.Errorf("overlaps = %+v", m.Overlaps)
	}
}

func TestDormantBranchesAreNotAnalyzed(t *testing.T) {
	testrepo.Isolate(t)
	r := testrepo.New(t)
	r.Commit("main", "shared.txt", "one\n", "add shared")
	r.Branch("feat/old", "main")
	r.Commit("feat/old", "shared.txt", "one from old\n", "old edits shared")
	r.Commit("main", "shared.txt", "one from main\n", "main edits shared")

	cfg := config.Defaults()
	s, err := engine.Open(context.Background(), engine.Options{
		Dir: r.Dir, Config: cfg, GitHub: "never",
		Now: func() time.Time { return testrepo.Epoch.Add(90 * 24 * time.Hour) },
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	old := must(t, m, "feat/old")
	if old.Forecast != nil || old.HasFlag(model.FlagConflict) {
		t.Errorf("stale branch was analyzed: forecast=%+v flags=%v", old.Forecast, old.Flags)
	}
	if !old.HasFlag(model.FlagStale) || !hasIssue(m, model.IssueStale, "feat/old") {
		t.Errorf("stale branch not reported: flags=%v", old.Flags)
	}
	if hasIssue(m, model.IssueConflict, "feat/old") {
		t.Error("stale branch should not raise a conflict issue")
	}
}

func TestWorktreesAgentsAndUpstream(t *testing.T) {
	testrepo.Isolate(t)
	r := testrepo.New(t)
	r.AddRemote()
	r.Branch("claude/refactor", "main")
	r.Commits("claude/refactor", 1)
	r.Branch("feat/pair", "main")
	r.Commit("feat/pair", "pair.txt", "x\n", "pair work", "Co-authored-by: Codex <codex@openai.com>")
	r.Branch("feat/pushed", "main")
	r.Commits("feat/pushed", 1)
	r.Push("feat/pushed")
	r.Commits("feat/pushed", 1)
	r.Switch("main")
	wt := r.Worktree("wt-pair", "feat/pair")
	r.WriteIn(wt, "scratch.txt", "dirty\n")

	m := build(t, r)
	if a := must(t, m, "claude/refactor"); a.Agent != "claude" || a.AgentBy != "branch name" {
		t.Errorf("claude/refactor agent = %q (%s)", a.Agent, a.AgentBy)
	}
	pair := must(t, m, "feat/pair")
	if pair.Agent != "codex" {
		t.Errorf("feat/pair agent = %q (%s)", pair.Agent, pair.AgentBy)
	}
	if pair.Worktree == nil || pair.Worktree.Untracked != 1 || !pair.HasFlag(model.FlagDirty) {
		t.Errorf("feat/pair worktree = %+v flags=%v", pair.Worktree, pair.Flags)
	}
	if pair.Worktree != nil && pair.Worktree.Display != "../wt-pair" {
		t.Errorf("worktree display = %q", pair.Worktree.Display)
	}
	pushed := must(t, m, "feat/pushed")
	if pushed.Upstream == nil || pushed.Upstream.Ahead != 1 || !pushed.HasFlag(model.FlagUnpushed) {
		t.Errorf("feat/pushed upstream = %+v flags=%v", pushed.Upstream, pushed.Flags)
	}
	if !must(t, m, "claude/refactor").HasFlag(model.FlagLocal) {
		t.Error("claude/refactor should be local-only")
	}

	r.Git("push", "-q", "origin", "--delete", "feat/pushed")
	r.Git("fetch", "-q", "--prune")
	m = build(t, r)
	if p := must(t, m, "feat/pushed"); !p.HasFlag(model.FlagGone) {
		t.Errorf("feat/pushed flags after delete = %v", p.Flags)
	}
	if !hasIssue(m, model.IssueUpstreamGone, "feat/pushed") {
		t.Error("missing upstream_gone issue")
	}
}

func TestRemoteSourceAndOverrides(t *testing.T) {
	testrepo.Isolate(t)
	r := testrepo.New(t)
	r.AddRemote()
	r.Branch("feat/api", "main")
	r.Commits("feat/api", 1)
	r.Branch("feat/ui", "main")
	r.Commits("feat/ui", 1)
	r.Push("feat/api")
	r.Push("feat/ui")
	r.Switch("main")

	m := build(t, r, func(c *config.Config) { c.Parents["feat/ui"] = "feat/api" })
	ui := expectParent(t, m, "feat/ui", "feat/api")
	if ui.ParentSource != model.SourceConfig {
		t.Errorf("feat/ui source = %s", ui.ParentSource)
	}

	r.Git("config", "branch.feat/api.vbparent", "main")
	s, err := engine.Open(context.Background(), engine.Options{Dir: r.Dir, Config: config.Defaults(), GitHub: "never", Source: engine.SourceRemote})
	if err != nil {
		t.Fatal(err)
	}
	rm, err := s.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rm.Repo.Source != engine.SourceRemote {
		t.Fatalf("source = %s", rm.Repo.Source)
	}
	must(t, rm, "feat/api")
	must(t, rm, "feat/ui")
	if rm.Repo.Head != "" {
		t.Errorf("remote source should not mark a head, got %q", rm.Repo.Head)
	}
}

func TestStackToolMetadata(t *testing.T) {
	testrepo.Isolate(t)
	r := testrepo.New(t)
	for _, b := range []string{"base", "town", "graphite", "machete"} {
		r.Branch(b, "main")
		r.Commits(b, 1)
	}
	r.Switch("main")
	// Without metadata, all four are siblings on main.
	m := build(t, r)
	for _, b := range []string{"town", "graphite", "machete"} {
		expectParent(t, m, b, "main")
	}

	// git-town stores parents in git config.
	r.Git("config", "git-town-branch.town.parent", "base")
	// Graphite stores a JSON blob under refs/branch-metadata/<branch>.
	blob := filepath.Join(t.TempDir(), "meta.json")
	if err := os.WriteFile(blob, []byte(`{"parentBranchName":"base","parentBranchRevision":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	oid := r.Git("hash-object", "-w", blob)
	r.Git("update-ref", "refs/branch-metadata/graphite", oid)
	// git-machete keeps an indented layout file in the git directory.
	if err := os.WriteFile(filepath.Join(r.Dir, ".git", "machete"), []byte("main\n  base\n    machete\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = build(t, r)
	for b, source := range map[string]string{"town": model.SourceGitTown, "graphite": model.SourceGraphite, "machete": model.SourceMachete} {
		br := expectParent(t, m, b, "base")
		if br.ParentSource != source {
			t.Errorf("%s: source = %s, want %s", b, br.ParentSource, source)
		}
	}

	// Stack metadata can be ignored.
	m = build(t, r, func(c *config.Config) { c.Analysis.ReadStackMetadata = false })
	expectParent(t, m, "town", "main")
}

func TestStateTokenAndDiff(t *testing.T) {
	testrepo.Isolate(t)
	r := testrepo.New(t)
	r.Branch("feat/a", "main")
	r.Commits("feat/a", 1)
	m1 := build(t, r)
	m2 := build(t, r)
	if m1.State != m2.State {
		t.Fatalf("state not stable: %s vs %s", m1.State, m2.State)
	}
	r.Commits("feat/a", 1)
	r.Branch("feat/b", "main")
	m3 := build(t, r)
	if m3.State == m1.State {
		t.Fatal("state did not change")
	}
	changes := engine.Diff(engine.TakeSnapshot(m1), engine.TakeSnapshot(m3))
	var kinds []string
	for _, c := range changes {
		kinds = append(kinds, c.Kind+":"+c.Branch)
	}
	got := strings.Join(kinds, " ")
	if !strings.Contains(got, "added:feat/b") || !strings.Contains(got, "changed:feat/a") {
		t.Errorf("changes = %s", got)
	}
}

func TestGlob(t *testing.T) {
	cases := []struct {
		pat, s string
		want   bool
	}{
		{"claude/*", "claude/x", true},
		{"claude/*", "claude/x/y", true},
		{"claude/*", "feat/claude", false},
		{"*/.claude/worktrees/*", "C:/repo/.claude/worktrees/a1", true},
		{"dependabot/*", "dependabot/npm/x", true},
		{"feat-?", "feat-1", true},
		{"*", "", true},
	}
	for _, c := range cases {
		if got := engine.Glob(c.pat, c.s); got != c.want {
			t.Errorf("Glob(%q, %q) = %v", c.pat, c.s, got)
		}
	}
}

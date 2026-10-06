// Package mcpserver exposes vb to coding agents over the Model Context
// Protocol. Every tool is read-only, so clients can safely auto-approve them.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/j-a-man/visual-branches/internal/engine"
	"github.com/j-a-man/visual-branches/internal/model"
	"github.com/j-a-man/visual-branches/internal/render"
)

// Instructions tell the client how to use the tools efficiently.
const Instructions = `vb maps the git branches of the current repository: parents, stacks,
ahead/behind, worktrees, pull requests, CI, reviews, predicted merge
conflicts, and branches that edit the same files.

Use branch_map first; it replaces git status, git branch -vv,
git log --graph, git worktree list, gh pr list, and gh pr checks in one
compact call. It returns a state token: pass it to changes_since to poll
cheaply instead of re-reading the whole map. Before editing files in a repo
where other agents or people work in parallel, call who_touches with the
paths you plan to change. Use check_conflicts before pushing or rebasing.
Every item under "attention" includes a suggested command.`

// Options configure the server.
type Options struct {
	// Dir is the default repository directory.
	Dir string
	// Version is reported to clients.
	Version string
	// Offline never contacts GitHub.
	Offline bool
}

// New builds the MCP server with all tools registered.
func New(o Options) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "vb", Title: "visual-branches", Version: o.Version}, &mcp.ServerOptions{Instructions: Instructions})
	h := &handlers{o: o}
	readOnly := func(title string) *mcp.ToolAnnotations {
		f := false
		return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &f}
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "branch_map",
		Description: "Map of every branch: parent (stacks), ahead/behind, worktree, PR state, review, CI, predicted conflicts, overlapping branches, and a ranked attention list with suggested commands. Returns a state token for changes_since.",
		Annotations: readOnly("Branch map"),
	}, h.branchMap)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "branch_detail",
		Description: "Everything about one branch: parent evidence, commits and files not on its parent, PR checks, merge forecast, overlaps, and issues. Defaults to the current branch.",
		Annotations: readOnly("Branch detail"),
	}, h.branchDetail)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "changes_since",
		Description: "What changed since a state token returned by branch_map or a previous changes_since call: new, deleted, and moved branches, PR/CI/review changes, and new or resolved attention items. Much cheaper than a full map.",
		Annotations: readOnly("Changes since"),
	}, h.changesSince)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "check_conflicts",
		Description: "Predict whether merging a branch into a target (default: its parent) conflicts, and in which files, without touching any working tree.",
		Annotations: readOnly("Check conflicts"),
	}, h.checkConflicts)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "who_touches",
		Description: "Pre-flight check for parallel work: which other unmerged branches change the given files or directories. Call before editing files when other agents or people may be working on the same code.",
		Annotations: readOnly("Who touches these files"),
	}, h.whoTouches)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "cleanup_candidates",
		Description: "Merged branches (including squash and rebase merges) that are not checked out anywhere and can be deleted, with the exact commands. Does not delete anything.",
		Annotations: readOnly("Cleanup candidates"),
	}, h.cleanup)
	return s
}

// Run serves over stdio until the client disconnects.
func Run(ctx context.Context, o Options) error {
	return New(o).Run(ctx, &mcp.StdioTransport{})
}

type handlers struct {
	o Options
}

type repoArg struct {
	Repo string `json:"repo,omitempty" jsonschema:"path inside the repository; defaults to the server's working directory"`
}

func (h *handlers) build(ctx context.Context, repo string, source string, mutate func(*engine.Session)) (*engine.Session, *model.Map, error) {
	dir := repo
	if dir == "" {
		dir = h.o.Dir
	}
	s, err := engine.Open(ctx, engine.Options{Dir: dir, Offline: h.o.Offline, Source: source, Tool: "vb " + h.o.Version})
	if err != nil {
		return nil, nil, err
	}
	if mutate != nil {
		mutate(s)
	}
	m, err := s.Build(ctx)
	if err != nil {
		return nil, nil, err
	}
	return s, m, nil
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func jsonText(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return text(string(data)), nil
}

type branchMapIn struct {
	repoArg
	Format        string `json:"format,omitempty" jsonschema:"agent (compact text, default) or json (full structured map)"`
	Focus         string `json:"focus,omitempty" jsonschema:"only this branch's lineage and descendants"`
	IncludeRemote bool   `json:"include_remote,omitempty" jsonschema:"include remote-only branches, for example ones pushed by cloud agents"`
	MaxTokens     int    `json:"max_tokens,omitempty" jsonschema:"token budget for agent format; output degrades gracefully to fit"`
	HideMerged    bool   `json:"hide_merged,omitempty" jsonschema:"omit merged branches"`
}

func (h *handlers) branchMap(ctx context.Context, _ *mcp.CallToolRequest, in branchMapIn) (*mcp.CallToolResult, any, error) {
	source := ""
	if in.IncludeRemote {
		source = engine.SourceAll
	}
	s, m, err := h.build(ctx, in.Repo, source, nil)
	if err != nil {
		return nil, nil, err
	}
	if in.Focus != "" && m.Branch(in.Focus) == nil {
		return nil, nil, fmt.Errorf("branch %q not found", in.Focus)
	}
	engine.SaveSnapshot(s.Store, m)
	flt := render.Filter{Focus: in.Focus, ShowMerged: s.Config.Display.ShowMerged && !in.HideMerged, Hide: s.Config.Display.Hide}
	if in.Format == "json" {
		res, err := jsonText(m)
		return res, nil, err
	}
	budget := in.MaxTokens
	if budget == 0 {
		budget = s.Config.AgentOutput.MaxTokens
	}
	return text(render.Agent(m, render.AgentOptions{
		Now: time.Now(), Filter: flt, MaxTokens: budget,
		Hints: s.Config.AgentOutput.Hints, Legend: s.Config.AgentOutput.Legend,
	})), nil, nil
}

type branchDetailIn struct {
	repoArg
	Branch string `json:"branch,omitempty" jsonschema:"branch name; defaults to the current branch"`
	Format string `json:"format,omitempty" jsonschema:"agent (default) or json"`
}

func (h *handlers) branchDetail(ctx context.Context, _ *mcp.CallToolRequest, in branchDetailIn) (*mcp.CallToolResult, any, error) {
	s, m, err := h.build(ctx, in.Repo, "", nil)
	if err != nil {
		return nil, nil, err
	}
	name := in.Branch
	if name == "" {
		name = m.Repo.Head
	}
	if name == "" {
		return nil, nil, fmt.Errorf("HEAD is detached; pass a branch name")
	}
	if m.Branch(name) == nil {
		// Allow remote-only branches by name.
		s2, m2, err := h.build(ctx, in.Repo, engine.SourceAll, nil)
		if err == nil && m2.Branch(name) != nil {
			s, m = s2, m2
		}
	}
	d, err := s.Detail(ctx, m, name)
	if err != nil {
		return nil, nil, err
	}
	if in.Format == "json" {
		res, err := jsonText(d)
		return res, nil, err
	}
	return text(render.ShowAgent(m, d, time.Now())), nil, nil
}

type changesIn struct {
	repoArg
	State string `json:"state" jsonschema:"state token from branch_map or a previous changes_since call"`
}

func (h *handlers) changesSince(ctx context.Context, _ *mcp.CallToolRequest, in changesIn) (*mcp.CallToolResult, any, error) {
	s, m, err := h.build(ctx, in.Repo, "", nil)
	if err != nil {
		return nil, nil, err
	}
	old, ok := engine.LoadSnapshot(s.Store, in.State)
	engine.SaveSnapshot(s.Store, m)
	if !ok {
		out := fmt.Sprintf("# unknown state %q; full map follows\n", in.State)
		out += render.Agent(m, render.AgentOptions{Now: time.Now(), Filter: render.Filter{ShowMerged: true}, Hints: true, Legend: true})
		return text(out), nil, nil
	}
	return text(render.Delta(in.State, m.State, engine.Diff(old, engine.TakeSnapshot(m)))), nil, nil
}

type conflictsIn struct {
	repoArg
	Branch string `json:"branch,omitempty" jsonschema:"branch to merge; defaults to the current branch"`
	Target string `json:"target,omitempty" jsonschema:"branch or ref to merge into; defaults to the branch's parent"`
}

func (h *handlers) checkConflicts(ctx context.Context, _ *mcp.CallToolRequest, in conflictsIn) (*mcp.CallToolResult, any, error) {
	s, m, err := h.build(ctx, in.Repo, "", nil)
	if err != nil {
		return nil, nil, err
	}
	name := in.Branch
	if name == "" {
		name = m.Repo.Head
	}
	b := m.Branch(name)
	if b == nil {
		return nil, nil, fmt.Errorf("branch %q not found", name)
	}
	target := in.Target
	targetRef := target
	if target == "" {
		if b.Parent == "" {
			return nil, nil, fmt.Errorf("%s has no parent; pass target", name)
		}
		target = b.Parent
	}
	if tb := m.Branch(target); tb != nil {
		targetRef = s.CompareRef(ctx, tb)
	}
	res, err := s.Repo.PredictMerge(ctx, targetRef, b.Tip, true)
	if err != nil {
		return nil, nil, err
	}
	if res.Clean {
		return text(fmt.Sprintf("%s merges cleanly into %s", name, target)), nil, nil
	}
	return text(fmt.Sprintf("%s CONFLICTS with %s in %d %s: %s\nhint: git rebase %s %s",
		name, target, len(res.Files), pluralFile(len(res.Files)), strings.Join(res.Files, ", "), target, name)), nil, nil
}

func pluralFile(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
}

type whoTouchesIn struct {
	repoArg
	Paths   []string `json:"paths" jsonschema:"files or directories, relative to the repository root"`
	Exclude string   `json:"exclude_branch,omitempty" jsonschema:"branch to ignore; defaults to the current branch"`
}

func (h *handlers) whoTouches(ctx context.Context, _ *mcp.CallToolRequest, in whoTouchesIn) (*mcp.CallToolResult, any, error) {
	if len(in.Paths) == 0 {
		return nil, nil, fmt.Errorf("pass at least one path")
	}
	_, m, err := h.build(ctx, in.Repo, engine.SourceAll, func(s *engine.Session) {
		s.Config.Analysis.Overlap = true
	})
	if err != nil {
		return nil, nil, err
	}
	exclude := in.Exclude
	if exclude == "" {
		exclude = m.Repo.Head
	}
	hits := engine.Touching(m, in.Paths, exclude)
	if len(hits) == 0 {
		return text(fmt.Sprintf("no other unmerged branch changes %s", strings.Join(in.Paths, ", "))), nil, nil
	}
	names := make([]string, 0, len(hits))
	for n := range hits {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s also change these paths:\n", len(names), plural(len(names), "branch", "branches"))
	for _, n := range names {
		br := m.Branch(n)
		extra := ""
		if br.Agent != "" {
			extra += " agent=" + br.Agent
		}
		if br.Worktree != nil {
			extra += " wt=" + br.Worktree.Display
		}
		if br.PR.Open() {
			extra += fmt.Sprintf(" pr#%d", br.PR.Number)
		}
		fmt.Fprintf(&b, "- %s (%s ago%s): %s\n", n, render.Age(time.Since(br.CommittedAt)), extra, strings.Join(hits[n], ", "))
	}
	return text(b.String()), nil, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (h *handlers) cleanup(ctx context.Context, _ *mcp.CallToolRequest, in repoArg) (*mcp.CallToolResult, any, error) {
	_, m, err := h.build(ctx, in.Repo, "", nil)
	if err != nil {
		return nil, nil, err
	}
	var b strings.Builder
	n := 0
	for _, is := range m.Attention {
		if is.Kind != model.IssueDeletable && is.Kind != model.IssueMergedCheckedOut {
			continue
		}
		n++
		fmt.Fprintf(&b, "- %s -> %s\n", is.Message, is.Hint)
	}
	if n == 0 {
		return text("no merged branches to clean up"), nil, nil
	}
	return text(fmt.Sprintf("%d cleanup %s (nothing was deleted):\n%s", n, plural(n, "candidate", "candidates"), b.String())), nil, nil
}

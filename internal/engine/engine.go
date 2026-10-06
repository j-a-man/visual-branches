// Package engine builds the branch map. It is the single core that every
// vb surface (terminal, TUI, web, MCP, exports) renders.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/j-a-man/visual-branches/internal/cache"
	"github.com/j-a-man/visual-branches/internal/config"
	"github.com/j-a-man/visual-branches/internal/git"
	"github.com/j-a-man/visual-branches/internal/github"
	"github.com/j-a-man/visual-branches/internal/infer"
	"github.com/j-a-man/visual-branches/internal/model"
)

// Options control a build.
type Options struct {
	// Dir is any directory inside the repository.
	Dir string
	// Config overrides configuration loading when set.
	Config *config.Config
	// Offline never contacts GitHub and uses cached data only.
	Offline bool
	// GitHub overrides github.enabled: "auto", "always", or "never".
	GitHub string
	// Source is local, all, or remote. Empty uses display.show_remote.
	Source string
	// Light skips expensive analyses (conflicts, overlaps, squash detection).
	Light bool
	// Tool is the tool name and version recorded in the map.
	Tool string
	// Now overrides the clock, for tests.
	Now func() time.Time
}

// Session is an open repository with its configuration.
type Session struct {
	Repo   *git.Repo
	Config *config.Config
	Store  *cache.Store
	Opts   Options
}

// Open discovers the repository and loads configuration.
func Open(ctx context.Context, opts Options) (*Session, error) {
	repo, err := git.Open(ctx, opts.Dir)
	if err != nil {
		return nil, err
	}
	cfg := opts.Config
	if cfg == nil {
		cfg, err = config.Load(repo.Root, repo.CommonDir)
		if err != nil {
			return nil, err
		}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Tool == "" {
		opts.Tool = "vb"
	}
	return &Session{Repo: repo, Config: cfg, Store: cache.ForRepo(repo.CommonDir), Opts: opts}, nil
}

func (s *Session) source() string {
	switch s.Opts.Source {
	case SourceLocal, SourceAll, SourceRemote:
		return s.Opts.Source
	}
	if s.Config.Display.ShowRemote {
		return SourceAll
	}
	return SourceLocal
}

// Build collects all data and returns the branch map.
func (s *Session) Build(ctx context.Context) (*model.Map, error) {
	b := &builder{s: s, now: s.Opts.Now(), source: s.source(), restackFrom: map[string]string{}, start: time.Now()}
	if !s.Opts.Light {
		b.kv = s.Store.OpenKV("analysis.json")
		defer b.kv.Flush()
	}
	if err := b.run(ctx); err != nil {
		return nil, err
	}
	return b.m, nil
}

type builder struct {
	s      *Session
	now    time.Time
	source string
	kv     *cache.KV
	m      *model.Map

	refs      map[string]git.Ref
	gitConfig map[string]string
	trunks    []trunkInfo
	trunkOf   map[string]trunkInfo // trunk name -> info
	cands     []candidate
	byName    map[string]*model.Branch
	refOf     map[string]string // branch name -> ref used for comparisons
	graph     *git.Graph
	contain   *infer.Containment
	creations map[string]git.Creation
	trunkAB   map[string]map[string][2]int // trunk name -> ref -> ahead/behind
	prs       map[string]*model.PR
	worktrees map[string]worktreeInfo
	stashes   map[string]int
	explicit  map[string]infer.Explicit
	agentHits []agentHit
	// restackFrom maps a branch to the old parent tip to rebase from when
	// its parent was rewritten.
	restackFrom map[string]string
	start, last time.Time
	// missing counts analyses skipped for objects absent in a partial clone.
	missing  int
	mu       sync.Mutex
	warnings []string
}

// phase logs elapsed time per build phase when VB_DEBUG is set.
func (b *builder) phase(name string) {
	if os.Getenv("VB_DEBUG") == "" {
		return
	}
	now := time.Now()
	if b.last.IsZero() {
		b.last = b.start
	}
	fmt.Fprintf(os.Stderr, "vb: phase %-12s %6.1fms\n", name, float64(now.Sub(b.last).Microseconds())/1000)
	b.last = now
}

func (b *builder) warn(format string, args ...any) {
	b.mu.Lock()
	b.warnings = append(b.warnings, fmt.Sprintf(format, args...))
	b.mu.Unlock()
}

func (b *builder) run(ctx context.Context) error {
	s := b.s
	repo := s.Repo
	// Refs and config are independent; read them together.
	cfgDone := make(chan struct{})
	go func() {
		b.gitConfig = repo.ConfigList(ctx)
		close(cfgDone)
	}()
	all, err := repo.Refs(ctx)
	<-cfgDone
	if err != nil {
		return err
	}
	b.refs = map[string]git.Ref{}
	headRef, headOID := "", ""
	for _, r := range all {
		b.refs[r.Name] = r
		if r.Head && strings.HasPrefix(r.Name, "refs/heads/") {
			headRef, headOID = r.Name, r.OID
		}
	}
	if headRef == "" {
		_, headOID = repo.Head(ctx)
	}
	b.trunks = s.detectTrunks(b.refs, b.source)
	if len(b.trunks) == 0 && headRef != "" && b.source != SourceRemote {
		// No conventional trunk: treat the current branch as the trunk.
		n := strings.TrimPrefix(headRef, "refs/heads/")
		b.trunks = []trunkInfo{{name: n, ref: headRef, compare: headRef, exclude: []string{headRef}}}
		b.warn("no trunk branch found; using %s (set trunks in .vb.toml)", n)
	}
	b.trunkOf = map[string]trunkInfo{}
	var trunkNames []string
	for _, t := range b.trunks {
		b.trunkOf[t.name] = t
		trunkNames = append(trunkNames, t.name)
	}
	b.cands = s.selectBranches(all, b.trunks, b.source)
	b.capBranches(headRef)

	b.m = &model.Map{
		SchemaVersion: model.SchemaVersion,
		Tool:          s.Opts.Tool,
		GeneratedAt:   b.now,
		Repo: model.Repo{
			Name:       filepath.Base(repo.Root),
			Root:       filepath.ToSlash(repo.Root),
			Trunks:     trunkNames,
			GitVersion: repo.Version.String(),
			Source:     b.source,
			HeadOID:    headOID,
		},
	}
	if headRef != "" {
		b.m.Repo.Head = strings.TrimPrefix(headRef, "refs/heads/")
	} else {
		b.m.Repo.HeadDetached = true
	}
	if b.source == SourceRemote {
		b.m.Repo.Head = ""
	}
	b.detectRemote()

	// Model skeletons.
	b.byName = map[string]*model.Branch{}
	b.refOf = map[string]string{}
	names := map[string]bool{}
	for _, c := range b.cands {
		br := &model.Branch{
			Name:        c.name,
			Ref:         c.ref.Name,
			RemoteOnly:  c.remote && !c.trunk,
			Tip:         c.ref.OID,
			Subject:     c.ref.Subject,
			Author:      c.ref.AuthorName,
			AuthorEmail: c.ref.AuthorEmail,
			CommittedAt: c.ref.CommittedAt,
			Trunk:       c.trunk,
		}
		if c.ref.Name == headRef && b.source != SourceRemote {
			br.Head = true
		}
		if c.ref.Upstream != "" && !c.ref.IsRemote() {
			br.Upstream = &model.Upstream{
				Name:   git.ShortName(c.ref.Upstream),
				Ahead:  c.ref.UpstreamAhead,
				Behind: c.ref.UpstreamBehind,
				Gone:   c.ref.UpstreamGone,
			}
		}
		b.byName[c.name] = br
		b.refOf[c.name] = c.ref.Name
		if c.trunk {
			b.refOf[c.name] = b.trunkOf[c.name].compare
		}
		names[c.name] = true
		b.m.Branches = append(b.m.Branches, br)
	}

	// The unmerged commit graph.
	var tips, exclude []string
	seenTip := map[string]bool{}
	for _, c := range b.cands {
		if !c.trunk && !seenTip[c.ref.OID] {
			seenTip[c.ref.OID] = true
			tips = append(tips, c.ref.OID)
		}
	}
	for _, t := range b.trunks {
		exclude = append(exclude, t.exclude...)
	}
	b.phase("refs")

	// Independent collectors.
	var wg sync.WaitGroup
	run := func(fn func()) {
		wg.Add(1)
		go func() { defer wg.Done(); fn() }()
	}
	var graphErr error
	run(func() {
		b.graph, graphErr = repo.UnmergedGraph(ctx, tips, exclude, s.Config.Analysis.MaxCommits)
	})
	run(func() { b.collectTrunkAB(ctx) })
	run(func() { b.collectGitHub(ctx) })
	run(func() { b.collectWorktrees(ctx) })
	run(func() { b.stashes = repo.StashCounts(ctx) })
	run(func() { b.collectCreations(ctx) })
	run(func() { b.explicit = s.explicitParents(ctx, b.refs, b.gitConfig, names) })
	run(func() { b.collectAgentCommits(ctx) })
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if graphErr != nil {
		return graphErr
	}
	if b.graph.Truncated {
		b.warn("commit walk stopped at %d commits; raise analysis.max_commits for exact results", s.Config.Analysis.MaxCommits)
	}

	b.phase("collect")
	b.inferParents(names)
	b.buildTree()
	b.phase("infer")
	b.computeAheadBehind(ctx)
	b.attachLocalState()
	b.phase("local state")
	b.detectMerged(ctx)
	b.detectAgents()
	b.phase("merged")
	if !s.Opts.Light {
		b.forecast(ctx)
		b.phase("forecast")
		b.overlaps(ctx)
		b.phase("overlaps")
	}
	b.flagsAndIssues()
	if b.missing > 0 {
		b.warn("%d %s skipped: objects are missing locally (partial clone); vb never downloads them", b.missing, plural(b.missing, "analysis", "analyses"))
	}
	b.m.Warnings = append(b.m.Warnings, b.warnings...)
	b.m.Warnings = append(b.m.Warnings, s.Config.Warnings...)
	b.m.State = StateToken(b.m)
	b.m.Index()
	return nil
}

func (b *builder) capBranches(headRef string) {
	max := b.s.Config.Analysis.MaxBranches
	nonTrunk := 0
	for _, c := range b.cands {
		if !c.trunk {
			nonTrunk++
		}
	}
	if max <= 0 || nonTrunk <= max {
		return
	}
	sort.SliceStable(b.cands, func(i, j int) bool {
		ci, cj := b.cands[i], b.cands[j]
		if ci.trunk != cj.trunk {
			return ci.trunk
		}
		if (ci.ref.Name == headRef) != (cj.ref.Name == headRef) {
			return ci.ref.Name == headRef
		}
		return ci.ref.CommittedAt.After(cj.ref.CommittedAt)
	})
	kept := 0
	var out []candidate
	for _, c := range b.cands {
		if c.trunk {
			out = append(out, c)
			continue
		}
		if kept < max {
			out = append(out, c)
			kept++
		}
	}
	b.warn("showing the %d most recently updated of %d branches (analysis.max_branches)", max, nonTrunk)
	b.cands = out
}

func (b *builder) detectRemote() {
	remote := b.s.Config.Remote
	url := b.gitConfig["remote."+remote+".url"]
	if url == "" {
		return
	}
	r := &model.Remote{Name: remote, URL: url}
	if host, owner, repo, ok := github.ParseRemote(url); ok {
		r.Host, r.Owner, r.Repo = host, owner, repo
	}
	if h := b.s.Config.GitHub.Host; h != "" && r.Host != "" && !strings.EqualFold(h, r.Host) {
		// Configured enterprise host differs from the URL host, for example
		// an SSH alias. Trust the configuration.
		r.Host = h
	}
	b.m.Repo.Remote = r
}

func (b *builder) collectTrunkAB(ctx context.Context) {
	b.trunkAB = map[string]map[string][2]int{}
	var refs []string
	for _, c := range b.cands {
		refs = append(refs, c.ref.Name)
	}
	for _, t := range b.trunks {
		ab, err := b.s.Repo.AheadBehind(ctx, t.compare, refs)
		if err != nil {
			b.warn("comparing with %s: %v", t.name, err)
			continue
		}
		b.trunkAB[t.name] = ab
	}
}

func (b *builder) githubMode() string {
	if b.s.Opts.GitHub != "" {
		return b.s.Opts.GitHub
	}
	return b.s.Config.GitHub.Enabled
}

func (b *builder) collectGitHub(ctx context.Context) {
	mode := b.githubMode()
	remote := b.m.Repo.Remote
	switch {
	case mode == "never":
		b.m.GitHub = model.GitHubStatus{Status: model.GitHubDisabled}
		return
	case remote == nil || remote.Owner == "":
		b.m.GitHub = model.GitHubStatus{Status: model.GitHubNotHub, Message: "no GitHub remote"}
		return
	}
	offline := b.s.Opts.Offline
	if mode != "always" && !offline && !github.IsGitHubHost(remote.Host, b.s.Config.GitHub.Host) {
		b.m.GitHub = model.GitHubStatus{Status: model.GitHubNotHub, Message: remote.Host + " is not a GitHub host"}
		return
	}
	// Most recently active first: with many branches, only the most recent
	// ones get individual merged-PR lookups.
	cands := append([]candidate(nil), b.cands...)
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].ref.CommittedAt.After(cands[j].ref.CommittedAt) })
	var heads []string
	headOf := map[string]string{}
	for _, c := range cands {
		if c.trunk {
			continue
		}
		h := b.s.headName(c, b.source)
		heads = append(heads, h)
		headOf[h] = c.name
	}
	f := &github.Fetcher{
		Host:    remote.Host,
		Owner:   remote.Owner,
		Repo:    remote.Repo,
		Store:   b.s.Store,
		TTL:     b.s.Config.GitHub.CacheTTL.Duration,
		Offline: offline,
	}
	prs, status := f.PRs(ctx, heads)
	b.prs = map[string]*model.PR{}
	for h, pr := range prs {
		b.prs[headOf[h]] = pr
	}
	if offline && status.Status == model.GitHubCached && len(prs) == 0 {
		status = model.GitHubStatus{Status: model.GitHubOffline, Message: "offline"}
	}
	b.m.GitHub = status
}

// collectWorktrees reads the dirty state of every work tree that has a
// branch checked out. Paths come from for-each-ref (%(worktreepath)), so no
// separate `git worktree list` call is needed.
func (b *builder) collectWorktrees(ctx context.Context) {
	b.worktrees = map[string]worktreeInfo{}
	mainPath := ""
	if filepath.Base(b.s.Repo.CommonDir) == ".git" {
		mainPath = filepath.Dir(b.s.Repo.CommonDir)
	}
	for _, c := range b.cands {
		if c.ref.WorktreePath == "" || c.ref.IsRemote() {
			continue
		}
		path := filepath.Clean(c.ref.WorktreePath)
		b.worktrees[c.name] = worktreeInfo{path: path, main: strings.EqualFold(path, mainPath)}
	}
	if !b.s.Config.Analysis.WorktreeStatus {
		return
	}
	// Workers record statuses in their own map; b.worktrees is only updated
	// after every worker is done, since it is being iterated meanwhile.
	statuses := map[string]git.Status{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for name, wt := range b.worktrees {
		if _, err := os.Stat(wt.path); err != nil {
			continue // prunable: the directory is gone
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(name, path string) {
			defer wg.Done()
			defer func() { <-sem }()
			if st, err := b.s.Repo.WorktreeStatus(ctx, path); err == nil {
				mu.Lock()
				statuses[name] = st
				mu.Unlock()
			}
		}(name, wt.path)
	}
	wg.Wait()
	for name, st := range statuses {
		wt := b.worktrees[name]
		wt.status = st
		b.worktrees[name] = wt
	}
}

type worktreeInfo struct {
	path   string
	main   bool
	status git.Status
}

func (b *builder) collectCreations(ctx context.Context) {
	b.creations = map[string]git.Creation{}
	for _, c := range b.cands {
		if c.remote || c.trunk || !strings.HasPrefix(c.ref.Name, "refs/heads/") {
			continue
		}
		if cr, ok := b.s.Repo.BranchCreation(ctx, c.ref.Short()); ok {
			b.creations[c.name] = cr
		}
	}
}

func (b *builder) inferParents(names map[string]bool) {
	in := infer.Input{
		Trunks:      b.m.Repo.Trunks,
		Graph:       b.graph,
		Explicit:    b.explicit,
		PRBase:      map[string]string{},
		CreatedFrom: map[string]string{},
		CreatedAt:   map[string]time.Time{},
		TrunkAhead:  map[string]map[string]int{},
	}
	for _, c := range b.cands {
		in.Nodes = append(in.Nodes, infer.Node{Name: c.name, Tip: c.ref.OID, Trunk: c.trunk})
		if pr := b.prs[c.name]; pr.Open() {
			if base := b.resolveBase(pr.Base, names); base != "" {
				in.PRBase[c.name] = base
			}
		}
		if cr, ok := b.creations[c.name]; ok {
			in.CreatedAt[c.name] = cr.At
			if from := b.s.resolveStart(cr.From, names, b.source); from != "" {
				in.CreatedFrom[c.name] = from
			}
		}
		ta := map[string]int{}
		for t, ab := range b.trunkAB {
			if v, ok := ab[c.ref.Name]; ok {
				ta[t] = v[0]
			}
		}
		in.TrunkAhead[c.name] = ta
	}
	results, contain := infer.Infer(in)
	b.contain = contain
	for name, r := range results {
		br := b.byName[name]
		br.Parent, br.ParentSource, br.ParentConfidence = r.Parent, r.Source, r.Confidence
	}
}

// resolveBase maps a PR base ref name to a branch in the map.
func (b *builder) resolveBase(base string, names map[string]bool) string {
	if names[base] {
		return base
	}
	if b.source == SourceAll && names[b.s.Config.Remote+"/"+base] {
		return b.s.Config.Remote + "/" + base
	}
	return ""
}

// buildTree links children and orders branches depth-first from the roots.
func (b *builder) buildTree() {
	children := map[string][]*model.Branch{}
	for _, br := range b.m.Branches {
		if br.Parent != "" {
			children[br.Parent] = append(children[br.Parent], br)
		}
	}
	less := b.sortLess()
	for _, kids := range children {
		sort.SliceStable(kids, func(i, j int) bool { return less(kids[i], kids[j]) })
	}
	var ordered []*model.Branch
	visited := map[string]bool{}
	var visit func(br *model.Branch, depth int)
	visit = func(br *model.Branch, depth int) {
		if visited[br.Name] {
			return
		}
		visited[br.Name] = true
		br.Depth = depth
		br.Children = br.Children[:0]
		for _, k := range children[br.Name] {
			br.Children = append(br.Children, k.Name)
		}
		ordered = append(ordered, br)
		for _, k := range children[br.Name] {
			visit(k, depth+1)
		}
	}
	var roots []*model.Branch
	for _, br := range b.m.Branches {
		if br.Parent == "" || b.byName[br.Parent] == nil {
			br.Parent = ""
			roots = append(roots, br)
		}
	}
	trunkRank := map[string]int{}
	for i, t := range b.m.Repo.Trunks {
		trunkRank[t] = i
	}
	sort.SliceStable(roots, func(i, j int) bool {
		ri, rj := roots[i], roots[j]
		if ri.Trunk != rj.Trunk {
			return ri.Trunk
		}
		if ri.Trunk {
			return trunkRank[ri.Name] < trunkRank[rj.Name]
		}
		return less(ri, rj)
	})
	for _, r := range roots {
		visit(r, 0)
	}
	// Anything unvisited is part of a cycle that inference could not break.
	for _, br := range b.m.Branches {
		if !visited[br.Name] {
			br.Parent = ""
			visit(br, 0)
		}
	}
	b.m.Branches = ordered
}

func (b *builder) sortLess() func(x, y *model.Branch) bool {
	switch b.s.Config.Display.Sort {
	case "name":
		return func(x, y *model.Branch) bool { return x.Name < y.Name }
	case "oldest":
		return func(x, y *model.Branch) bool {
			if !x.CommittedAt.Equal(y.CommittedAt) {
				return x.CommittedAt.Before(y.CommittedAt)
			}
			return x.Name < y.Name
		}
	}
	return func(x, y *model.Branch) bool {
		if !x.CommittedAt.Equal(y.CommittedAt) {
			return x.CommittedAt.After(y.CommittedAt)
		}
		return x.Name < y.Name
	}
}

// rootTrunk returns the trunk at the root of a branch's lineage.
func (b *builder) rootTrunk(br *model.Branch) string {
	seen := map[string]bool{}
	cur := br
	for cur != nil && !seen[cur.Name] {
		if cur.Trunk {
			return cur.Name
		}
		seen[cur.Name] = true
		cur = b.byName[cur.Parent]
	}
	if len(b.m.Repo.Trunks) > 0 {
		return b.m.Repo.Trunks[0]
	}
	return ""
}

func (b *builder) computeAheadBehind(ctx context.Context) {
	byParent := map[string][]*model.Branch{}
	for _, br := range b.m.Branches {
		if tr := b.rootTrunk(br); tr != "" && !br.Trunk {
			if ab, ok := b.trunkAB[tr][br.Ref]; ok {
				br.TrunkAhead, br.TrunkBehind = ab[0], ab[1]
			}
		}
		if br.Parent == "" {
			continue
		}
		parent := b.byName[br.Parent]
		if parent.Trunk {
			if ab, ok := b.trunkAB[parent.Name][br.Ref]; ok {
				br.Ahead, br.Behind = ab[0], ab[1]
			}
			continue
		}
		byParent[parent.Name] = append(byParent[parent.Name], br)
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for pname, kids := range byParent {
		wg.Add(1)
		sem <- struct{}{}
		go func(pname string, kids []*model.Branch) {
			defer wg.Done()
			defer func() { <-sem }()
			var refs []string
			for _, k := range kids {
				refs = append(refs, k.Ref)
			}
			ab, err := b.s.Repo.AheadBehind(ctx, b.refOf[pname], refs)
			if err != nil {
				b.warn("comparing with %s: %v", pname, err)
				return
			}
			for _, k := range kids {
				if v, ok := ab[k.Ref]; ok {
					k.Ahead, k.Behind = v[0], v[1]
				}
			}
		}(pname, kids)
	}
	wg.Wait()
}

func (b *builder) attachLocalState() {
	root := b.s.Repo.Root
	for name, wt := range b.worktrees {
		br := b.byName[name]
		if br == nil || br.RemoteOnly {
			continue
		}
		br.Worktree = &model.Worktree{
			Path:       filepath.ToSlash(wt.path),
			Display:    displayPath(root, wt.path),
			Main:       wt.main,
			Staged:     wt.status.Staged,
			Unstaged:   wt.status.Unstaged,
			Untracked:  wt.status.Untracked,
			Conflicted: wt.status.Conflicted,
		}
	}
	for _, br := range b.m.Branches {
		br.PR = b.prs[br.Name]
		if !br.RemoteOnly {
			br.Stashes = b.stashes[br.Name]
		}
	}
}

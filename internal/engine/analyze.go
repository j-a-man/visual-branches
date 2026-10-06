package engine

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/j-a-man/visual-branches/internal/git"
	"github.com/j-a-man/visual-branches/internal/model"
)

// tipOf returns the commit a branch is compared at (trunks use their
// remote-tracking ref when present).
func (b *builder) tipOf(br *model.Branch) string {
	if br.Trunk {
		if r, ok := b.refs[b.refOf[br.Name]]; ok {
			return r.OID
		}
	}
	return br.Tip
}

// hintRef is the ref name to use for a branch in suggested commands.
func (b *builder) hintRef(br *model.Branch) string {
	if br.Trunk {
		return git.ShortName(b.refOf[br.Name])
	}
	if br.RemoteOnly {
		return br.Name
	}
	return br.Name
}

func (b *builder) isTrunkTip(oid string) bool {
	for _, t := range b.trunks {
		for _, ref := range t.exclude {
			if b.refs[ref].OID == oid {
				return true
			}
		}
	}
	return false
}

func (b *builder) detectMerged(ctx context.Context) {
	var squashCands []*model.Branch
	for _, br := range b.m.Branches {
		if br.Trunk {
			continue
		}
		trunk := b.rootTrunk(br)
		if !b.graph.Contains(br.Tip) {
			// The tip is reachable from a trunk.
			cr, ok := b.creations[br.Name]
			tracking := ok && (cr.From == "refs/remotes/"+b.s.Config.Remote+"/"+br.Name || cr.From == b.s.Config.Remote+"/"+br.Name)
			if (ok && !cr.Moved && !tracking) || (!ok && b.isTrunkTip(br.Tip)) {
				br.Empty = true
				continue
			}
			br.Merged = &model.Merge{How: model.MergedAncestry, Into: trunk}
			continue
		}
		if pr := br.PR; pr != nil && pr.State == model.PRMerged {
			if pr.HeadOID == br.Tip || (b.s.Repo.HasObject(ctx, pr.HeadOID) && b.s.Repo.IsAncestor(ctx, br.Tip, pr.HeadOID)) {
				br.Merged = &model.Merge{How: model.MergedPR, Into: pr.Base}
				continue
			}
		}
		// Squash and rebase detection serves cleanup, which is about local
		// branches; remote branches rely on PR state and ancestry. This
		// keeps --all and CI runs over hundreds of remote branches fast.
		local := strings.HasPrefix(br.Ref, "refs/heads/")
		if local && br.TrunkAhead > 0 && br.TrunkBehind > 0 {
			squashCands = append(squashCands, br)
		}
	}
	if b.s.Opts.Light || !b.s.Config.Analysis.SquashDetection || len(squashCands) == 0 {
		return
	}
	b.detectSquash(ctx, squashCands)
}

type trunkPatchIDs struct {
	Tip string        `json:"tip"`
	IDs []git.PatchID `json:"ids"` // newest first
}

// trunkPatchSet returns the patch ids of the trunk's recent commits. The
// list is cached and extended incrementally as the trunk moves forward, so
// only new trunk commits are diffed.
func (b *builder) trunkPatchSet(ctx context.Context, trunk string) map[string]bool {
	t, ok := b.trunkOf[trunk]
	if !ok {
		return nil
	}
	tip := b.refs[t.compare].OID
	lookback := b.s.Config.Analysis.SquashLookback
	key := fmt.Sprintf("trunkpids:%s:%d", trunk, lookback)
	var cached trunkPatchIDs
	have := b.kv.Get(key, &cached)
	var ids []git.PatchID
	switch {
	case have && cached.Tip == tip:
		ids = cached.IDs
	case have && cached.Tip != "" && b.s.Repo.IsAncestor(ctx, cached.Tip, tip):
		fresh, err := b.s.Repo.CommitPatchIDs(ctx, lookback, tip, "^"+cached.Tip)
		if err != nil {
			b.skipped(err, "squash detection on "+trunk)
			return nil
		}
		ids = append(fresh, cached.IDs...) //nolint:gocritic // fresh is newly allocated; newest first
	default:
		fresh, err := b.s.Repo.CommitPatchIDs(ctx, lookback, tip)
		if err != nil {
			b.skipped(err, "squash detection on "+trunk)
			return nil
		}
		ids = fresh
	}
	if lookback > 0 && len(ids) > lookback {
		ids = ids[:lookback]
	}
	if !have || cached.Tip != tip {
		b.kv.Put(key, trunkPatchIDs{Tip: tip, IDs: ids})
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id.ID] = true
	}
	return set
}

// branchPatchIDs is cached by the branch tip: the merge base with a trunk
// that only moves forward does not change, so neither do these ids.
type branchPatchIDs struct {
	Combined string   `json:"combined"`
	Commits  []string `json:"commits"`
}

func (b *builder) detectSquash(ctx context.Context, cands []*model.Branch) {
	sets := map[string]map[string]bool{}
	for _, br := range cands {
		tr := b.rootTrunk(br)
		if _, ok := sets[tr]; !ok {
			sets[tr] = b.trunkPatchSet(ctx, tr)
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, br := range cands {
		tr := b.rootTrunk(br)
		set := sets[tr]
		if len(set) == 0 {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(br *model.Branch, tr string, set map[string]bool) {
			defer wg.Done()
			defer func() { <-sem }()
			trunkTip := b.refs[b.trunkOf[tr].compare].OID
			key := "bpids:" + tr + ":" + br.Tip
			var ids branchPatchIDs
			if !b.kv.Get(key, &ids) {
				// trunk...tip diffs from the merge base without computing it
				// separately; trunk..tip lists the branch's own commits.
				combined, err := b.s.Repo.DiffPatchID(ctx, trunkTip+"..."+br.Tip)
				if err != nil {
					b.skipped(err, "squash detection for "+br.Name)
					return
				}
				ids.Combined = combined
				if br.TrunkAhead <= 50 {
					ids.Commits, err = b.s.Repo.PatchIDs(ctx, 0, trunkTip+".."+br.Tip)
					if err != nil {
						b.skipped(err, "squash detection for "+br.Name)
						return
					}
				}
				b.kv.Put(key, ids)
			}
			switch {
			case ids.Combined != "" && set[ids.Combined]:
				br.Merged = &model.Merge{How: model.MergedSquash, Into: tr}
			case len(ids.Commits) > 0 && allIn(ids.Commits, set):
				br.Merged = &model.Merge{How: model.MergedRebase, Into: tr}
			}
		}(br, tr, set)
	}
	wg.Wait()
}

func allIn(ids []string, set map[string]bool) bool {
	for _, id := range ids {
		if !set[id] {
			return false
		}
	}
	return true
}

// active reports whether a branch has unmerged work worth analyzing.
func active(br *model.Branch) bool {
	return !br.Trunk && br.Merged == nil && !br.Empty && br.Ahead > 0
}

type mergeCache struct {
	Clean bool     `json:"clean"`
	Files []string `json:"files,omitempty"`
}

func (b *builder) predict(ctx context.Context, ours, theirs string) (mergeCache, error) {
	key := "mt:" + ours + ":" + theirs
	var mc mergeCache
	if b.kv.Get(key, &mc) {
		return mc, nil
	}
	res, err := b.s.Repo.PredictMerge(ctx, ours, theirs, false)
	if err != nil {
		return mc, err
	}
	if !res.Clean {
		if full, err := b.s.Repo.PredictMerge(ctx, ours, theirs, true); err == nil {
			res = full
		}
	}
	mc = mergeCache{Clean: res.Clean, Files: res.Files}
	b.kv.Put(key, mc)
	return mc, nil
}

// dormant reports whether a branch looks abandoned: no commits for
// analysis.stale_after, or a pull request that was closed without merging.
// Dormant branches are not analyzed for conflicts, overlaps, or restacks,
// which keeps large repositories fast and the attention list actionable.
func (b *builder) dormant(br *model.Branch) bool {
	if br.PR != nil && br.PR.State == model.PRClosed {
		return true
	}
	stale := b.s.Config.Analysis.StaleAfter.Duration
	return stale > 0 && b.now.Sub(br.CommittedAt) > stale && !br.PR.Open()
}

// analyzable reports whether a branch gets conflict and overlap analysis.
func (b *builder) analyzable(br *model.Branch) bool {
	return active(br) && br.Parent != "" && !b.dormant(br)
}

// skipped records an analysis that could not run because objects are missing
// locally (partial clones); a single warning summarizes them.
func (b *builder) skipped(err error, what string) {
	if git.IsMissingObject(err) {
		b.mu.Lock()
		b.missing++
		b.mu.Unlock()
		return
	}
	b.warn("%s: %v", what, err)
}

func (b *builder) forecast(ctx context.Context) {
	if !b.s.Config.Analysis.Conflicts || !b.s.Repo.HasMergeTree() {
		return
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, br := range b.m.Branches {
		if !b.analyzable(br) {
			continue
		}
		parent := b.byName[br.Parent]
		if br.Behind == 0 {
			br.Forecast = &model.Forecast{Target: parent.Name, Clean: true}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(br, parent *model.Branch) {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := b.predict(ctx, b.tipOf(parent), br.Tip)
			if err != nil {
				b.skipped(err, "conflict forecast for "+br.Name)
				return
			}
			br.Forecast = &model.Forecast{Target: parent.Name, Clean: res.Clean, Files: res.Files}
			if !parent.Trunk && parent.Merged == nil {
				// When the parent was rewritten (amended or rebased), rebasing
				// onto it must start from the old parent tip, not the merge base.
				if fp := b.forkPoint(ctx, parent, br); fp != "" {
					b.mu.Lock()
					b.restackFrom[br.Name] = fp
					b.mu.Unlock()
				}
			}
		}(br, parent)
	}
	wg.Wait()
}

// forkPoint returns the parent's old tip that a restack must start from when
// the parent was rewritten, or "" when a plain rebase onto the parent works.
// Results are cached by both tips.
func (b *builder) forkPoint(ctx context.Context, parent, br *model.Branch) string {
	key := "fp:" + parent.Tip + ":" + br.Tip
	var fp string
	if b.kv.Get(key, &fp) {
		return fp
	}
	var forkPt, mergeBase string
	done := make(chan struct{})
	go func() {
		forkPt = b.s.Repo.ForkPoint(ctx, b.refOf[parent.Name], br.Tip)
		close(done)
	}()
	mergeBase = b.s.Repo.MergeBase(ctx, b.refOf[parent.Name], br.Tip)
	<-done
	if forkPt != "" && forkPt != mergeBase {
		fp = forkPt
	}
	b.kv.Put(key, fp)
	return fp
}

func (b *builder) ignored(file string) bool {
	base := path.Base(file)
	for _, pat := range b.s.Config.Overlap.Ignore {
		if ok, _ := path.Match(pat, file); ok {
			return true
		}
		if ok, _ := path.Match(pat, base); ok {
			return true
		}
	}
	return false
}

func (b *builder) overlaps(ctx context.Context) {
	if !b.s.Config.Analysis.Overlap {
		return
	}
	var cands []*model.Branch
	for _, br := range b.m.Branches {
		if b.analyzable(br) {
			cands = append(cands, br)
		}
	}
	// Pairwise comparison is quadratic; keep the most recently active branches.
	if limit := b.s.Config.Overlap.MaxBranches; limit > 0 && len(cands) > limit {
		sort.SliceStable(cands, func(i, j int) bool { return cands[i].CommittedAt.After(cands[j].CommittedAt) })
		cands = cands[:limit]
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, br := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func(br *model.Branch) {
			defer wg.Done()
			defer func() { <-sem }()
			base := b.tipOf(b.byName[br.Parent])
			key := "files:" + base + ":" + br.Tip
			var files []string
			if !b.kv.Get(key, &files) {
				var err error
				files, err = b.s.Repo.ChangedFiles(ctx, base+"..."+br.Tip)
				if err != nil {
					b.skipped(err, "changed files of "+br.Name)
					return
				}
				b.kv.Put(key, files)
			}
			br.Files = files
		}(br)
	}
	wg.Wait()

	b.m.Index()
	filtered := map[string]map[string]bool{}
	for _, br := range cands {
		set := map[string]bool{}
		for _, f := range br.Files {
			if !b.ignored(f) {
				set[f] = true
			}
		}
		filtered[br.Name] = set
	}
	var pairs []model.Overlap
	for i := 0; i < len(cands); i++ {
		for j := i + 1; j < len(cands); j++ {
			x, y := cands[i], cands[j]
			if b.m.Related(x.Name, y.Name) {
				continue
			}
			var shared []string
			for f := range filtered[x.Name] {
				if filtered[y.Name][f] {
					shared = append(shared, f)
				}
			}
			if len(shared) == 0 {
				continue
			}
			sort.Strings(shared)
			pairs = append(pairs, model.Overlap{A: x.Name, B: y.Name, Files: shared})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i].Files) > len(pairs[j].Files) })
	if b.s.Config.Overlap.CheckConflicts && b.s.Repo.HasMergeTree() {
		limit := min(b.s.Config.Overlap.MaxPairs, len(pairs))
		for k := 0; k < limit; k++ {
			wg.Add(1)
			sem <- struct{}{}
			go func(k int) {
				defer wg.Done()
				defer func() { <-sem }()
				x, y := b.byName[pairs[k].A], b.byName[pairs[k].B]
				ours, theirs := x.Tip, y.Tip
				if ours > theirs {
					ours, theirs = theirs, ours
				}
				res, err := b.predict(ctx, ours, theirs)
				if err != nil {
					return
				}
				pairs[k].ConflictChecked = true
				pairs[k].Conflict = !res.Clean
			}(k)
		}
		wg.Wait()
	}
	b.m.Overlaps = pairs
}

// flag order: most urgent first.
var flagOrder = []string{
	model.FlagConflict, model.FlagCollision, model.FlagDiverged, model.FlagRestack,
	model.FlagParentMerged, model.FlagDirty, model.FlagOverlap, model.FlagUnpushed,
	model.FlagBehindRemote, model.FlagGone, model.FlagLocal, model.FlagOldBase,
	model.FlagStale, model.FlagEmpty, model.FlagDeletable,
}

func (b *builder) flagsAndIssues() {
	var issues []model.Issue
	add := func(is model.Issue) { issues = append(issues, is) }
	cfg := b.s.Config
	remote := cfg.Remote
	staleAfter := cfg.Analysis.StaleAfter.Duration
	flags := map[string]map[string]bool{}
	flag := func(br *model.Branch, f string) {
		if flags[br.Name] == nil {
			flags[br.Name] = map[string]bool{}
		}
		flags[br.Name][f] = true
	}

	for _, br := range b.m.Branches {
		if br.Worktree.Dirty() {
			flag(br, model.FlagDirty)
		}
		if up := br.Upstream; up != nil {
			switch {
			case up.Gone:
			case up.Ahead > 0 && up.Behind > 0:
				flag(br, model.FlagDiverged)
			case up.Ahead > 0:
				flag(br, model.FlagUnpushed)
			case up.Behind > 0:
				flag(br, model.FlagBehindRemote)
			}
		}
		if br.Trunk {
			if up := br.Upstream; up != nil && up.Ahead > 0 && up.Behind > 0 {
				add(model.Issue{Kind: model.IssueDiverged, Severity: model.SevHigh, Branch: br.Name,
					Message: fmt.Sprintf("%s diverged from %s (%d local, %d remote)", br.Name, up.Name, up.Ahead, up.Behind),
					Hint:    fmt.Sprintf("git switch %s && git pull --rebase", br.Name)})
			}
			continue
		}

		if br.Merged != nil {
			switch {
			case br.Worktree == nil && !br.Head:
				flag(br, model.FlagDeletable)
				hint := fmt.Sprintf("git branch -D %s", br.Name)
				if br.RemoteOnly {
					hint = fmt.Sprintf("git push %s --delete %s", remote, strings.TrimPrefix(br.Name, remote+"/"))
				}
				add(model.Issue{Kind: model.IssueDeletable, Severity: model.SevLow, Branch: br.Name,
					Message: fmt.Sprintf("%s is merged (%s)", br.Name, mergeWord(br.Merged.How)), Hint: hint})
			case br.Worktree != nil && !br.Worktree.Main && !br.Worktree.Dirty():
				add(model.Issue{Kind: model.IssueMergedCheckedOut, Severity: model.SevLow, Branch: br.Name,
					Message: fmt.Sprintf("%s is merged but still checked out at %s", br.Name, br.Worktree.Display),
					Hint:    fmt.Sprintf("git worktree remove %s", br.Worktree.Display)})
			}
			continue
		}

		if br.Empty {
			flag(br, model.FlagEmpty)
		}
		if up := br.Upstream; up != nil {
			switch {
			case up.Gone:
				flag(br, model.FlagGone)
				if br.Ahead > 0 {
					add(model.Issue{Kind: model.IssueUpstreamGone, Severity: model.SevMedium, Branch: br.Name,
						Message: fmt.Sprintf("%s was deleted on the remote but %s has unmerged work", up.Name, br.Name),
						Hint:    fmt.Sprintf("git push -u %s %s", remote, br.Name)})
				}
			case up.Ahead > 0 && up.Behind > 0:
				add(model.Issue{Kind: model.IssueDiverged, Severity: model.SevHigh, Branch: br.Name,
					Message: fmt.Sprintf("%s diverged from %s (%d local, %d remote)", br.Name, up.Name, up.Ahead, up.Behind),
					Hint:    fmt.Sprintf("git switch %s && git pull --rebase", br.Name)})
			case up.Ahead > 0:
				add(model.Issue{Kind: model.IssueUnpushed, Severity: model.SevLow, Branch: br.Name,
					Message: fmt.Sprintf("%s has %d unpushed %s", br.Name, up.Ahead, plural(up.Ahead, "commit", "commits")),
					Hint:    fmt.Sprintf("git push %s %s", remote, br.Name)})
			}
		} else if !br.RemoteOnly && !br.Empty && br.Ahead > 0 {
			flag(br, model.FlagLocal)
		}

		// Abandoned branches only report staleness; restack and base advice
		// would be noise.
		dormant := b.dormant(br)
		parent := b.byName[br.Parent]
		if parent != nil && !parent.Trunk && !dormant {
			switch {
			case parent.Merged != nil:
				flag(br, model.FlagParentMerged)
				onto := b.rootTrunk(br)
				ontoRef := onto
				if t, ok := b.trunkOf[onto]; ok {
					ontoRef = git.ShortName(t.compare)
				}
				add(model.Issue{Kind: model.IssueParentMerged, Severity: model.SevMedium, Branch: br.Name, Other: parent.Name,
					Message: fmt.Sprintf("%s's parent %s was merged (%s)", br.Name, parent.Name, mergeWord(parent.Merged.How)),
					Hint:    fmt.Sprintf("git rebase --onto %s %s %s", ontoRef, parent.Name, br.Name)})
			case br.Behind > 0:
				flag(br, model.FlagRestack)
				hint := fmt.Sprintf("git rebase %s %s", parent.Name, br.Name)
				if fp := b.restackFrom[br.Name]; fp != "" {
					hint = fmt.Sprintf("git rebase --onto %s %s %s", parent.Name, short(fp), br.Name)
				}
				add(model.Issue{Kind: model.IssueRestack, Severity: model.SevMedium, Branch: br.Name, Other: parent.Name,
					Message: fmt.Sprintf("%s is %d behind its parent %s", br.Name, br.Behind, parent.Name),
					Hint:    hint})
			}
		}
		if parent != nil && parent.Trunk && !dormant && cfg.Analysis.OldBaseThreshold > 0 && br.Behind >= cfg.Analysis.OldBaseThreshold && br.Ahead > 0 {
			flag(br, model.FlagOldBase)
			add(model.Issue{Kind: model.IssueOldBase, Severity: model.SevLow, Branch: br.Name, Other: parent.Name,
				Message: fmt.Sprintf("%s is %d commits behind %s", br.Name, br.Behind, parent.Name),
				Hint:    fmt.Sprintf("git rebase %s %s", b.hintRef(parent), br.Name)})
		}

		localConflict := false
		if fc := br.Forecast; fc != nil && !fc.Clean {
			localConflict = true
			flag(br, model.FlagConflict)
			target := b.byName[fc.Target]
			msg := fmt.Sprintf("%s will conflict with %s", br.Name, fc.Target)
			if len(fc.Files) > 0 {
				msg += " in " + listFiles(fc.Files, 3)
			}
			add(model.Issue{Kind: model.IssueConflict, Severity: model.SevHigh, Branch: br.Name, Other: fc.Target,
				Message: msg, Hint: fmt.Sprintf("git rebase %s %s", b.hintRef(target), br.Name)})
		}

		if pr := br.PR; pr != nil {
			switch pr.State {
			case model.PROpen:
				high := false
				if pr.CI == model.CIFail {
					high = true
					msg := fmt.Sprintf("#%d CI failing", pr.Number)
					if names := pr.FailingChecks(); len(names) > 0 {
						msg += ": " + listNames(names, 3)
					}
					add(model.Issue{Kind: model.IssueCIFailing, Severity: model.SevHigh, Branch: br.Name,
						Message: msg, Hint: fmt.Sprintf("gh pr checks %d", pr.Number)})
				}
				if pr.Review == model.ReviewChanges {
					high = true
					add(model.Issue{Kind: model.IssueChanges, Severity: model.SevHigh, Branch: br.Name,
						Message: fmt.Sprintf("#%d has changes requested", pr.Number),
						Hint:    fmt.Sprintf("gh pr view %d --comments", pr.Number)})
				}
				if pr.Mergeable == model.MergeConflicting && !localConflict {
					high = true
					add(model.Issue{Kind: model.IssuePRConflict, Severity: model.SevHigh, Branch: br.Name, Other: pr.Base,
						Message: fmt.Sprintf("#%d has conflicts with %s on GitHub", pr.Number, pr.Base),
						Hint:    fmt.Sprintf("git fetch && git rebase %s/%s %s", remote, pr.Base, br.Name)})
				}
				if parent != nil && !parent.Trunk && parent.Merged == nil && b.trunkOf[pr.Base].name != "" {
					add(model.Issue{Kind: model.IssuePRBaseMismatch, Severity: model.SevMedium, Branch: br.Name, Other: parent.Name,
						Message: fmt.Sprintf("#%d targets %s but %s is built on %s", pr.Number, pr.Base, br.Name, parent.Name),
						Hint:    fmt.Sprintf("gh pr edit %d --base %s", pr.Number, parent.Name)})
				}
				ciOK := pr.CI == model.CIPass || (pr.CI == model.CINone && pr.Review == model.ReviewApproved)
				reviewOK := pr.Review == model.ReviewApproved || (pr.Review == "" && pr.CI == model.CIPass)
				if !pr.Draft && !high && !localConflict && ciOK && reviewOK && !flags[br.Name][model.FlagRestack] && !flags[br.Name][model.FlagParentMerged] {
					add(model.Issue{Kind: model.IssueReady, Severity: model.SevReady, Branch: br.Name,
						Message: fmt.Sprintf("#%d is ready to merge", pr.Number),
						Hint:    fmt.Sprintf("gh pr merge %d", pr.Number)})
				}
			case model.PRMerged:
				add(model.Issue{Kind: model.IssueMergedNewCommits, Severity: model.SevMedium, Branch: br.Name,
					Message: fmt.Sprintf("#%d was merged but %s has newer commits", pr.Number, br.Name),
					Hint:    fmt.Sprintf("vb show %s", br.Name)})
			}
		}

		if staleAfter > 0 && !br.Empty && b.now.Sub(br.CommittedAt) > staleAfter && !br.PR.Open() {
			flag(br, model.FlagStale)
			add(model.Issue{Kind: model.IssueStale, Severity: model.SevLow, Branch: br.Name,
				Message: fmt.Sprintf("%s has had no commits for %s", br.Name, humanDays(b.now.Sub(br.CommittedAt)))})
		}
	}

	for _, ov := range b.m.Overlaps {
		x, y := b.byName[ov.A], b.byName[ov.B]
		if ov.Conflict {
			flag(x, model.FlagCollision)
			flag(y, model.FlagCollision)
			add(model.Issue{Kind: model.IssueCollision, Severity: model.SevHigh, Branch: ov.A, Other: ov.B,
				Message: fmt.Sprintf("%s and %s both edit %s and will conflict", ov.A, ov.B, listFiles(ov.Files, 2)),
				Hint:    fmt.Sprintf("vb show %s", ov.A)})
		} else {
			flag(x, model.FlagOverlap)
			flag(y, model.FlagOverlap)
			add(model.Issue{Kind: model.IssueOverlap, Severity: model.SevMedium, Branch: ov.A, Other: ov.B,
				Message: fmt.Sprintf("%s and %s both edit %s", ov.A, ov.B, listFiles(ov.Files, 2)),
				Hint:    fmt.Sprintf("vb show %s", ov.A)})
		}
	}

	for _, br := range b.m.Branches {
		br.Flags = br.Flags[:0]
		for _, f := range flagOrder {
			if flags[br.Name][f] {
				br.Flags = append(br.Flags, f)
			}
		}
	}

	// Rank: severity, then the current branch's lineage, then tree order.
	order := map[string]int{}
	for i, br := range b.m.Branches {
		order[br.Name] = i
	}
	lineage := map[string]bool{}
	if h := b.m.Repo.Head; h != "" {
		b.m.Index()
		for _, x := range b.m.Lineage(h) {
			lineage[x.Name] = true
		}
	}
	sort.SliceStable(issues, func(i, j int) bool {
		a, c := issues[i], issues[j]
		if ra, rc := model.SeverityRank(a.Severity), model.SeverityRank(c.Severity); ra != rc {
			return ra < rc
		}
		if lineage[a.Branch] != lineage[c.Branch] {
			return lineage[a.Branch]
		}
		return order[a.Branch] < order[c.Branch]
	})
	b.m.Attention = issues
}

func mergeWord(how string) string {
	switch how {
	case model.MergedAncestry:
		return "merged"
	case model.MergedPR:
		return "PR merged"
	case model.MergedSquash:
		return "squash"
	case model.MergedRebase:
		return "rebase"
	}
	return how
}

func listFiles(files []string, n int) string {
	return listNames(files, n)
}

func listNames(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:n], ", ") + fmt.Sprintf(" +%d more", len(names)-n)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func short(oid string) string {
	if len(oid) > 8 {
		return oid[:8]
	}
	return oid
}

func humanDays(d time.Duration) string {
	days := int(d.Hours() / 24)
	switch {
	case days >= 365:
		return fmt.Sprintf("%dy", days/365)
	case days >= 60:
		return fmt.Sprintf("%dmo", days/30)
	}
	return fmt.Sprintf("%dd", days)
}

// Package infer works out which branch each branch was created from.
//
// Git does not record a branch's parent, so vb combines evidence in priority
// order: explicit configuration and stack-tool metadata, the base of the
// branch's open pull request, commit ancestry, the reflog, and finally the
// closest trunk. Every decision records its source and a confidence level.
package infer

import (
	"math/bits"
	"sort"
	"time"

	"github.com/j-a-man/visual-branches/internal/git"
	"github.com/j-a-man/visual-branches/internal/model"
)

// Node is a branch taking part in inference.
type Node struct {
	Name  string
	Tip   string
	Trunk bool
}

// Explicit is a parent declared by configuration or a stack tool.
type Explicit struct {
	Parent string
	Source string
}

// Input holds everything inference needs.
type Input struct {
	Nodes []Node
	// Trunks lists trunk branch names in priority order.
	Trunks []string
	// Graph contains the commits not reachable from any trunk.
	Graph *git.Graph
	// Explicit parents from config, git config, or stack tools.
	Explicit map[string]Explicit
	// PRBase maps a branch to the base branch of its open pull request.
	PRBase map[string]string
	// CreatedFrom maps a branch to the start point in its reflog, already
	// resolved to a branch name when possible.
	CreatedFrom map[string]string
	// CreatedAt maps a branch to the time it was created.
	CreatedAt map[string]time.Time
	// TrunkAhead maps branch -> trunk -> commits on branch not on trunk.
	TrunkAhead map[string]map[string]int
}

// Result is the inferred parent of one branch.
type Result struct {
	Parent     string
	Source     string
	Confidence string
}

type inferrer struct {
	in      Input
	idx     map[string]int // non-trunk branch name -> bit index
	names   []string       // bit index -> name
	tips    map[string][]int
	tipOf   []string // bit index -> tip
	words   int
	contain map[string][]uint64
	isTrunk map[string]bool
	exists  map[string]bool
	nodeOf  map[string]Node
}

// Containment answers which non-trunk branches contain an unmerged commit.
type Containment struct {
	x *inferrer
}

// Contains reports whether branch contains the unmerged commit oid.
func (c *Containment) Contains(branch, oid string) bool {
	if c == nil {
		return false
	}
	i, ok := c.x.idx[branch]
	return ok && has(c.x.contain[oid], i)
}

// Branches lists the branches containing the unmerged commit oid.
func (c *Containment) Branches(oid string) []string {
	if c == nil {
		return nil
	}
	var out []string
	for _, i := range members(c.x.contain[oid]) {
		out = append(out, c.x.names[i])
	}
	return out
}

// Infer returns a parent for every non-trunk node, plus a containment index.
// The result is always a forest rooted at trunks.
func Infer(in Input) (map[string]Result, *Containment) {
	x := &inferrer{
		in:      in,
		idx:     map[string]int{},
		tips:    map[string][]int{},
		isTrunk: map[string]bool{},
		exists:  map[string]bool{},
		contain: map[string][]uint64{},
	}
	for _, t := range in.Trunks {
		x.isTrunk[t] = true
	}
	x.nodeOf = map[string]Node{}
	for _, n := range in.Nodes {
		x.exists[n.Name] = true
		x.nodeOf[n.Name] = n
		if n.Trunk || x.isTrunk[n.Name] {
			x.isTrunk[n.Name] = true
			continue
		}
		x.idx[n.Name] = len(x.names)
		x.names = append(x.names, n.Name)
	}
	x.words = (len(x.names) + 63) / 64
	x.tipOf = make([]string, len(x.names))
	for _, n := range in.Nodes {
		if i, ok := x.idx[n.Name]; ok {
			x.tips[n.Tip] = append(x.tips[n.Tip], i)
			x.tipOf[i] = n.Tip
		}
	}
	x.buildContainment()

	results := map[string]Result{}
	for _, n := range in.Nodes {
		if x.isTrunk[n.Name] {
			continue
		}
		results[n.Name] = x.inferOne(n)
	}
	x.resolveEqualTips(results)
	x.breakCycles(results)
	return results, &Containment{x: x}
}

func (x *inferrer) buildContainment() {
	g := x.in.Graph
	if g == nil {
		return
	}
	for _, c := range g.Order {
		set := x.contain[c]
		if set == nil {
			set = make([]uint64, x.words)
			x.contain[c] = set
		}
		for _, i := range x.tips[c] {
			set[i/64] |= 1 << (uint(i) % 64)
		}
		for _, p := range g.Parents[c] {
			if !g.Contains(p) {
				continue
			}
			ps := x.contain[p]
			if ps == nil {
				ps = make([]uint64, x.words)
				x.contain[p] = ps
			}
			for w := range set {
				ps[w] |= set[w]
			}
		}
	}
}

func has(set []uint64, i int) bool {
	return set != nil && set[i/64]&(1<<(uint(i)%64)) != 0
}

func members(set []uint64) []int {
	var out []int
	for w, word := range set {
		for word != 0 {
			b := bits.TrailingZeros64(word)
			out = append(out, w*64+b)
			word &^= 1 << uint(b)
		}
	}
	return out
}

func (x *inferrer) inferOne(n Node) Result {
	if e, ok := x.in.Explicit[n.Name]; ok && e.Parent != n.Name && x.exists[e.Parent] {
		return Result{Parent: e.Parent, Source: e.Source, Confidence: model.High}
	}
	if base, ok := x.in.PRBase[n.Name]; ok && base != n.Name && x.exists[base] && !x.isTrunk[base] {
		return Result{Parent: base, Source: model.SourcePR, Confidence: model.High}
	}
	if r, ok := x.ancestry(n, nil); ok {
		return r
	}
	return x.trunkFor(n)
}

// strong reports whether a parent came from explicit evidence rather than
// commit history.
func strong(r Result) bool {
	switch r.Source {
	case model.SourceConfig, model.SourceGitTown, model.SourceGraphite, model.SourceMachete, model.SourcePR:
		return true
	}
	return false
}

// ancestry walks the branch's first-parent chain through unmerged commits and
// returns the first other branch that contains a commit on that chain,
// skipping branches in exclude.
func (x *inferrer) ancestry(n Node, exclude map[string]bool) (Result, bool) {
	g := x.in.Graph
	self, ok := x.idx[n.Name]
	if !ok || g == nil || !g.Contains(n.Tip) {
		return Result{}, false
	}
	tipSet := x.contain[n.Tip]
	for c := n.Tip; g.Contains(c); {
		set := x.contain[c]
		var cands []int
		for _, i := range members(set) {
			if i == self || has(tipSet, i) || exclude[x.names[i]] {
				// Itself, a branch that contains this branch's tip (a
				// descendant or an identical branch), or a branch ruled out
				// while resolving a cycle.
				continue
			}
			cands = append(cands, i)
		}
		if len(cands) > 0 {
			return x.pick(n, c, cands), true
		}
		parents := g.Parents[c]
		if len(parents) == 0 {
			break
		}
		c = parents[0]
	}
	return Result{}, false
}

// pick chooses among branches that contain fork commit c.
func (x *inferrer) pick(n Node, c string, cands []int) Result {
	var atTip []int
	for _, i := range cands {
		for _, t := range x.tips[c] {
			if t == i {
				atTip = append(atTip, i)
			}
		}
	}
	pool := cands
	conf := model.Medium // forked from the middle of another branch
	if len(atTip) > 0 {
		pool = atTip
		conf = model.High // forked exactly at another branch's tip
	}
	from := x.in.CreatedFrom[n.Name]
	if len(pool) > 1 {
		for _, i := range pool {
			if x.names[i] == from {
				return Result{Parent: from, Source: model.SourceReflog, Confidence: model.Medium}
			}
		}
	}
	x.sortByCreation(pool)
	// Branches with identical tips are interchangeable; the earliest created
	// is the root of their chain. Only distinct tips make the choice ambiguous.
	distinct := map[string]bool{}
	for _, i := range pool {
		distinct[x.tipOf[i]] = true
	}
	if len(distinct) > 1 {
		conf = model.Low
	}
	return Result{Parent: x.names[pool[0]], Source: model.SourceAncestry, Confidence: conf}
}

// sortByCreation orders branch indices by creation time, then name.
func (x *inferrer) sortByCreation(pool []int) {
	sort.Slice(pool, func(a, b int) bool {
		ta, tb := x.in.CreatedAt[x.names[pool[a]]], x.in.CreatedAt[x.names[pool[b]]]
		if !ta.Equal(tb) {
			if ta.IsZero() {
				return false
			}
			if tb.IsZero() {
				return true
			}
			return ta.Before(tb)
		}
		return x.names[pool[a]] < x.names[pool[b]]
	})
}

func (x *inferrer) trunkFor(n Node) Result {
	if base, ok := x.in.PRBase[n.Name]; ok && x.isTrunk[base] && x.exists[base] {
		return Result{Parent: base, Source: model.SourcePR, Confidence: model.High}
	}
	if from := x.in.CreatedFrom[n.Name]; from != "" && x.isTrunk[from] && x.exists[from] && len(x.trunks()) > 1 {
		return Result{Parent: from, Source: model.SourceReflog, Confidence: model.Medium}
	}
	trunks := x.trunks()
	if len(trunks) == 0 {
		return Result{}
	}
	best := trunks[0]
	if len(trunks) > 1 {
		bestAhead := -1
		for _, t := range trunks {
			a, ok := x.in.TrunkAhead[n.Name][t]
			if !ok {
				continue
			}
			if bestAhead < 0 || a < bestAhead {
				best, bestAhead = t, a
			}
		}
	}
	return Result{Parent: best, Source: model.SourceTrunk, Confidence: model.High}
}

func (x *inferrer) trunks() []string {
	var out []string
	for _, t := range x.in.Trunks {
		if x.exists[t] {
			out = append(out, t)
		}
	}
	return out
}

// resolveEqualTips turns groups of branches pointing at the same unmerged
// commit into a chain ordered by creation, unless explicit evidence exists.
func (x *inferrer) resolveEqualTips(results map[string]Result) {
	for tip, group := range x.tips {
		if len(group) < 2 || x.in.Graph == nil || !x.in.Graph.Contains(tip) {
			continue
		}
		names := make([]string, 0, len(group))
		for _, i := range group {
			names = append(names, x.names[i])
		}
		sort.Slice(names, func(a, b int) bool {
			ta, tb := x.in.CreatedAt[names[a]], x.in.CreatedAt[names[b]]
			if !ta.Equal(tb) && !ta.IsZero() && !tb.IsZero() {
				return ta.Before(tb)
			}
			if ta.IsZero() != tb.IsZero() {
				return !ta.IsZero()
			}
			return names[a] < names[b]
		})
		inGroup := map[string]bool{}
		for _, nm := range names {
			inGroup[nm] = true
		}
		for k, nm := range names {
			r := results[nm]
			if r.Source == model.SourceConfig || r.Source == model.SourceGitTown || r.Source == model.SourceGraphite || r.Source == model.SourceMachete || r.Source == model.SourcePR {
				continue
			}
			if from := x.in.CreatedFrom[nm]; inGroup[from] && from != nm {
				results[nm] = Result{Parent: from, Source: model.SourceReflog, Confidence: model.Medium}
				continue
			}
			if k > 0 && !x.in.CreatedAt[nm].IsZero() && !x.in.CreatedAt[names[k-1]].IsZero() {
				results[nm] = Result{Parent: names[k-1], Source: model.SourceReflog, Confidence: model.Low}
			}
		}
	}
}

// breakCycles resolves parent cycles. A cycle appears when two branches share
// unmerged commits and each has commits of its own (a parent that moved on
// after a child branched off it): from history alone each looks forked from
// the other. The cycle is cut at the weakest link: history-based links go
// before explicit ones, and among those the branch created first is taken to
// be the parent, so its own link is recomputed without the cycle's members.
func (x *inferrer) breakCycles(results map[string]Result) {
	names := make([]string, 0, len(results))
	for n := range results {
		names = append(names, n)
	}
	sort.Strings(names)
	for pass := 0; pass <= len(names); pass++ {
		cycle := x.findCycle(results, names)
		if cycle == nil {
			return
		}
		members := map[string]bool{}
		for _, n := range cycle {
			members[n] = true
		}
		victim, guessed := x.weakestLink(results, cycle)
		r := x.trunkFor(x.nodeOf[victim])
		if a, ok := x.ancestry(x.nodeOf[victim], members); ok {
			r = a
		}
		if guessed {
			r.Confidence = model.Low
		}
		results[victim] = r
	}
	// Give up on anything still cyclic: attach to a trunk.
	for {
		cycle := x.findCycle(results, names)
		if cycle == nil {
			return
		}
		r := x.trunkFor(x.nodeOf[cycle[0]])
		r.Confidence = model.Low
		results[cycle[0]] = r
	}
}

// findCycle returns the members of one parent cycle, or nil.
func (x *inferrer) findCycle(results map[string]Result, names []string) []string {
	for _, start := range names {
		pos := map[string]int{}
		var path []string
		for cur := start; !x.isTrunk[cur]; {
			if i, ok := pos[cur]; ok {
				return path[i:]
			}
			pos[cur] = len(path)
			path = append(path, cur)
			r, ok := results[cur]
			if !ok || r.Parent == "" {
				break
			}
			cur = r.Parent
		}
	}
	return nil
}

// weakestLink picks the cycle member whose parent link should be dropped:
// prefer history-based links, then the member created first (the real
// parent), then name order for determinism. guessed is true when several
// history-based links competed and creation order was unknown.
func (x *inferrer) weakestLink(results map[string]Result, cycle []string) (victim string, guessed bool) {
	cands := make([]string, 0, len(cycle))
	for _, n := range cycle {
		if !strong(results[n]) {
			cands = append(cands, n)
		}
	}
	if len(cands) == 0 {
		cands = append(cands, cycle...)
	}
	sort.Slice(cands, func(i, j int) bool {
		ti, tj := x.in.CreatedAt[cands[i]], x.in.CreatedAt[cands[j]]
		if !ti.Equal(tj) && !ti.IsZero() && !tj.IsZero() {
			return ti.Before(tj)
		}
		if ti.IsZero() != tj.IsZero() {
			return !ti.IsZero()
		}
		return cands[i] < cands[j]
	})
	return cands[0], len(cands) > 1 && !x.createdOrderKnown(cands)
}

func (x *inferrer) createdOrderKnown(names []string) bool {
	for _, n := range names {
		if x.in.CreatedAt[n].IsZero() {
			return false
		}
	}
	return true
}

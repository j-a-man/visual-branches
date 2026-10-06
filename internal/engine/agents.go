package engine

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/j-a-man/visual-branches/internal/config"
	"github.com/j-a-man/visual-branches/internal/model"
)

type agentHit struct {
	oid      string
	agent    string
	evidence string
}

type compiledRule struct {
	name     string
	branches []string
	paths    []string
	authors  []*regexp.Regexp
	trailers []*regexp.Regexp
}

func compileRules(rules map[string]config.AgentRule) []compiledRule {
	names := make([]string, 0, len(rules))
	for n := range rules {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []compiledRule
	for _, n := range names {
		r := rules[n]
		if r.Disabled {
			continue
		}
		cr := compiledRule{name: n, branches: r.Branches, paths: r.Paths}
		for _, a := range r.Authors {
			if re, err := regexp.Compile(a); err == nil {
				cr.authors = append(cr.authors, re)
			}
		}
		for _, t := range r.Trailers {
			if re, err := regexp.Compile(t); err == nil {
				cr.trailers = append(cr.trailers, re)
			}
		}
		out = append(out, cr)
	}
	return out
}

// collectAgentCommits scans unmerged commits for agent authors and
// co-author trailers.
func (b *builder) collectAgentCommits(ctx context.Context) {
	rules := compileRules(b.s.Config.Agents)
	hasCommitRules := false
	for _, r := range rules {
		if len(r.authors) > 0 || len(r.trailers) > 0 {
			hasCommitRules = true
		}
	}
	if !hasCommitRules {
		return
	}
	var revs []string
	seen := map[string]bool{}
	for _, c := range b.cands {
		if !c.trunk && !seen[c.ref.OID] {
			seen[c.ref.OID] = true
			revs = append(revs, c.ref.OID)
		}
	}
	if len(revs) == 0 {
		return
	}
	for _, t := range b.trunks {
		for _, ref := range t.exclude {
			revs = append(revs, "^"+ref)
		}
	}
	limit := min(b.s.Config.Analysis.MaxCommits, 5000)
	commits, err := b.s.Repo.LogStdin(ctx, limit, revs)
	if err != nil {
		b.warn("scanning commits for agents: %v", err)
		return
	}
	for _, c := range commits {
		for _, r := range rules {
			if ev := r.matchCommit(c.AuthorName, c.AuthorEmail, c.CoAuthors); ev != "" {
				b.agentHits = append(b.agentHits, agentHit{oid: c.OID, agent: r.name, evidence: ev})
				break
			}
		}
	}
}

func (r compiledRule) matchCommit(name, email string, coAuthors []string) string {
	for _, re := range r.authors {
		if re.MatchString(name) || re.MatchString(email) {
			return "author"
		}
	}
	for _, re := range r.trailers {
		for _, co := range coAuthors {
			if re.MatchString(co) {
				return "co-author"
			}
		}
	}
	return ""
}

// detectAgents tags branches by name, worktree path, and own commits.
func (b *builder) detectAgents() {
	rules := compileRules(b.s.Config.Agents)
	remotePrefix := b.s.Config.Remote + "/"
	for _, br := range b.m.Branches {
		if br.Trunk {
			continue
		}
		name := br.Name
		if br.RemoteOnly {
			name = strings.TrimPrefix(name, remotePrefix)
		}
		for _, r := range rules {
			if globAny(r.branches, name) {
				br.Agent, br.AgentBy = r.name, "branch name"
				break
			}
			if br.Worktree != nil && globAny(r.paths, br.Worktree.Path) {
				br.Agent, br.AgentBy = r.name, "worktree path"
				break
			}
		}
	}
	// Commits are attributed to the branch that introduced them: a branch
	// that contains the commit while its parent does not. A branch counts as
	// agent-made only when most of its own commits carry the evidence, so a
	// single AI-assisted commit on a human branch does not tag it.
	type key struct{ branch, agent string }
	counts := map[key]int{}
	evidence := map[key]string{}
	for _, hit := range b.agentHits {
		for _, name := range b.contain.Branches(hit.oid) {
			br := b.byName[name]
			if br == nil || br.Agent != "" {
				continue
			}
			if parent := b.byName[br.Parent]; parent != nil && !parent.Trunk && b.contain.Contains(parent.Name, hit.oid) {
				continue
			}
			k := key{name, hit.agent}
			counts[k]++
			evidence[k] = hit.evidence
		}
	}
	keys := make([]key, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i].agent < keys[j].agent
	})
	for _, k := range keys {
		br := b.byName[k.branch]
		if br.Agent != "" {
			continue
		}
		own := br.Ahead
		if own <= 0 {
			own = br.TrunkAhead
		}
		if own > 0 && counts[k]*2 > own {
			br.Agent, br.AgentBy = k.agent, evidence[k]
		}
	}
}

func globAny(patterns []string, s string) bool {
	for _, p := range patterns {
		if Glob(p, s) {
			return true
		}
	}
	return false
}

// Glob matches s against a pattern where * matches any run of characters
// (including /) and ? matches one character. Matching is case-sensitive.
func Glob(pattern, s string) bool {
	px, sx := 0, 0
	nextPx, nextSx := -1, -1
	for px < len(pattern) || sx < len(s) {
		if px < len(pattern) {
			switch c := pattern[px]; c {
			case '*':
				nextPx, nextSx = px, sx+1
				px++
				continue
			case '?':
				if sx < len(s) {
					px++
					sx++
					continue
				}
			default:
				if sx < len(s) && s[sx] == c {
					px++
					sx++
					continue
				}
			}
		}
		if nextSx > 0 && nextSx <= len(s) {
			px, sx = nextPx, nextSx
			continue
		}
		return false
	}
	return true
}

// Hidden reports whether a branch matches any display.hide pattern.
func Hidden(patterns []string, br *model.Branch) bool {
	return globAny(patterns, br.Name)
}

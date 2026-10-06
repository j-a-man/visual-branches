// Package model defines the branch map that every vb surface renders.
//
// The JSON encoding of Map is a public, versioned interface (`vb --json`,
// the MCP server, and the web API). Additive changes keep SchemaVersion;
// renames or removals bump it.
package model

import (
	"sort"
	"time"
)

// SchemaVersion is the version of the JSON encoding of Map.
const SchemaVersion = 1

// Map is the complete picture of a repository's branches.
type Map struct {
	SchemaVersion int          `json:"schemaVersion"`
	Tool          string       `json:"tool"`
	GeneratedAt   time.Time    `json:"generatedAt"`
	State         string       `json:"state"`
	Repo          Repo         `json:"repo"`
	GitHub        GitHubStatus `json:"github"`
	Branches      []*Branch    `json:"branches"`
	Overlaps      []Overlap    `json:"overlaps,omitempty"`
	Attention     []Issue      `json:"attention,omitempty"`
	Warnings      []string     `json:"warnings,omitempty"`

	index map[string]*Branch
}

// Repo describes the repository.
type Repo struct {
	Name         string   `json:"name"`
	Root         string   `json:"root"`
	Head         string   `json:"head,omitempty"`
	HeadDetached bool     `json:"headDetached,omitempty"`
	HeadOID      string   `json:"headOid,omitempty"`
	Trunks       []string `json:"trunks"`
	Remote       *Remote  `json:"remote,omitempty"`
	GitVersion   string   `json:"gitVersion"`
	Source       string   `json:"source"`
}

// Remote describes the hosting remote.
type Remote struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Host  string `json:"host,omitempty"`
	Owner string `json:"owner,omitempty"`
	Repo  string `json:"repo,omitempty"`
}

// WebURL returns the https URL of the repository on its host, if known.
func (r *Remote) WebURL() string {
	if r == nil || r.Host == "" || r.Owner == "" || r.Repo == "" {
		return ""
	}
	return "https://" + r.Host + "/" + r.Owner + "/" + r.Repo
}

// GitHub data availability.
const (
	GitHubOK       = "ok"
	GitHubCached   = "cached"
	GitHubOffline  = "offline"
	GitHubNoAuth   = "unauthenticated"
	GitHubError    = "error"
	GitHubDisabled = "disabled"
	GitHubNotHub   = "not-github"
)

// GitHubStatus reports whether pull request data is present and how fresh it is.
type GitHubStatus struct {
	Status    string     `json:"status"`
	Message   string     `json:"message,omitempty"`
	FetchedAt *time.Time `json:"fetchedAt,omitempty"`
}

// Available reports whether PR data was loaded.
func (g GitHubStatus) Available() bool {
	return g.Status == GitHubOK || g.Status == GitHubCached
}

// Parent inference sources.
const (
	SourceConfig   = "config"
	SourceGitTown  = "git-town"
	SourceGraphite = "graphite"
	SourceMachete  = "git-machete"
	SourcePR       = "pr"
	SourceAncestry = "ancestry"
	SourceReflog   = "reflog"
	SourceTrunk    = "trunk"
)

// Confidence levels.
const (
	High   = "high"
	Medium = "medium"
	Low    = "low"
)

// Branch is one node of the map.
type Branch struct {
	Name        string    `json:"name"`
	Ref         string    `json:"ref"`
	RemoteOnly  bool      `json:"remoteOnly,omitempty"`
	Tip         string    `json:"tip"`
	Subject     string    `json:"subject"`
	Author      string    `json:"author"`
	AuthorEmail string    `json:"authorEmail,omitempty"`
	CommittedAt time.Time `json:"committedAt"`

	Trunk bool `json:"trunk,omitempty"`
	Head  bool `json:"head,omitempty"`

	Parent           string   `json:"parent,omitempty"`
	ParentSource     string   `json:"parentSource,omitempty"`
	ParentConfidence string   `json:"parentConfidence,omitempty"`
	Children         []string `json:"children,omitempty"`
	Depth            int      `json:"depth"`

	// Ahead is the number of commits on this branch that are not on its parent.
	Ahead int `json:"ahead"`
	// Behind is the number of commits on the parent that are not on this branch.
	Behind int `json:"behind"`
	// TrunkAhead and TrunkBehind compare with the closest trunk.
	TrunkAhead  int `json:"trunkAhead"`
	TrunkBehind int `json:"trunkBehind"`

	Upstream *Upstream `json:"upstream,omitempty"`
	Worktree *Worktree `json:"worktree,omitempty"`
	Stashes  int       `json:"stashes,omitempty"`
	PR       *PR       `json:"pr,omitempty"`
	Merged   *Merge    `json:"merged,omitempty"`
	Empty    bool      `json:"empty,omitempty"`
	Agent    string    `json:"agent,omitempty"`
	AgentBy  string    `json:"agentEvidence,omitempty"`
	Forecast *Forecast `json:"forecast,omitempty"`
	Files    []string  `json:"files,omitempty"`
	Flags    []string  `json:"flags,omitempty"`
}

// Upstream is the tracking branch state.
type Upstream struct {
	Name   string `json:"name"`
	Ahead  int    `json:"ahead"`
	Behind int    `json:"behind"`
	Gone   bool   `json:"gone,omitempty"`
}

// Worktree is a checkout of the branch.
type Worktree struct {
	Path       string `json:"path"`
	Display    string `json:"display"`
	Main       bool   `json:"main,omitempty"`
	Staged     int    `json:"staged,omitempty"`
	Unstaged   int    `json:"unstaged,omitempty"`
	Untracked  int    `json:"untracked,omitempty"`
	Conflicted int    `json:"conflicted,omitempty"`
}

// Dirty reports whether the work tree has uncommitted changes.
func (w *Worktree) Dirty() bool {
	return w != nil && w.Staged+w.Unstaged+w.Untracked+w.Conflicted > 0
}

// Changes is the total number of changed paths.
func (w *Worktree) Changes() int {
	if w == nil {
		return 0
	}
	return w.Staged + w.Unstaged + w.Untracked + w.Conflicted
}

// PR states.
const (
	PROpen   = "open"
	PRMerged = "merged"
	PRClosed = "closed"
)

// Review states.
const (
	ReviewApproved = "approved"
	ReviewChanges  = "changes_requested"
	ReviewRequired = "review_required"
)

// CI states.
const (
	CIPass    = "pass"
	CIFail    = "fail"
	CIPending = "pending"
	CINone    = "none"
)

// Mergeability.
const (
	MergeClean       = "clean"
	MergeConflicting = "conflicting"
	MergeUnknown     = "unknown"
)

// PR is the pull request whose head is the branch.
type PR struct {
	Number       int        `json:"number"`
	Title        string     `json:"title"`
	URL          string     `json:"url"`
	State        string     `json:"state"`
	Draft        bool       `json:"draft,omitempty"`
	Base         string     `json:"base"`
	HeadOID      string     `json:"headOid"`
	Review       string     `json:"review,omitempty"`
	CI           string     `json:"ci"`
	Checks       []Check    `json:"checks,omitempty"`
	Mergeable    string     `json:"mergeable,omitempty"`
	Additions    int        `json:"additions"`
	Deletions    int        `json:"deletions"`
	ChangedFiles int        `json:"changedFiles"`
	Author       string     `json:"author,omitempty"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	MergedAt     *time.Time `json:"mergedAt,omitempty"`
}

// Open reports whether the PR is open.
func (p *PR) Open() bool { return p != nil && p.State == PROpen }

// FailingChecks returns the names of failing checks.
func (p *PR) FailingChecks() []string {
	if p == nil {
		return nil
	}
	var names []string
	for _, c := range p.Checks {
		if c.Status == CIFail {
			names = append(names, c.Name)
		}
	}
	return names
}

// Check is a single CI check.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // pass, fail, pending, skipped
	URL    string `json:"url,omitempty"`
}

// Merge methods.
const (
	MergedAncestry = "ancestry"
	MergedPR       = "pr"
	MergedSquash   = "squash"
	MergedRebase   = "rebase"
)

// Merge records how and where a branch was merged.
type Merge struct {
	How  string `json:"how"`
	Into string `json:"into"`
}

// Forecast is the predicted result of merging the branch into Target.
type Forecast struct {
	Target string   `json:"target"`
	Clean  bool     `json:"clean"`
	Files  []string `json:"files,omitempty"`
}

// Overlap is a pair of unrelated branches that change the same files.
type Overlap struct {
	A               string   `json:"a"`
	B               string   `json:"b"`
	Files           []string `json:"files"`
	Conflict        bool     `json:"conflict"`
	ConflictChecked bool     `json:"conflictChecked"`
}

// Severity levels, ordered by urgency.
const (
	SevHigh   = "high"
	SevMedium = "medium"
	SevReady  = "ready"
	SevLow    = "low"
)

// SeverityRank orders severities; lower is more urgent.
func SeverityRank(s string) int {
	switch s {
	case SevHigh:
		return 0
	case SevMedium:
		return 1
	case SevReady:
		return 2
	case SevLow:
		return 3
	}
	return 4
}

// Issue kinds.
const (
	IssueCIFailing        = "ci_failing"
	IssueChanges          = "changes_requested"
	IssueConflict         = "conflict"
	IssuePRConflict       = "pr_conflict"
	IssueCollision        = "collision"
	IssueDiverged         = "diverged"
	IssueParentMerged     = "parent_merged"
	IssueRestack          = "restack"
	IssuePRBaseMismatch   = "pr_base_mismatch"
	IssueOverlap          = "overlap"
	IssueUpstreamGone     = "upstream_gone"
	IssueMergedNewCommits = "commits_after_merge"
	IssueReady            = "ready"
	IssueOldBase          = "old_base"
	IssueUnpushed         = "unpushed"
	IssueStale            = "stale"
	IssueDeletable        = "deletable"
	IssueMergedCheckedOut = "merged_checked_out"
)

// Issue is something that needs attention, with a suggested next step.
type Issue struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Branch   string `json:"branch"`
	Other    string `json:"other,omitempty"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
}

// Flags shown next to branches.
const (
	FlagConflict     = "conflict"
	FlagCollision    = "collision"
	FlagDiverged     = "diverged"
	FlagRestack      = "restack"
	FlagParentMerged = "parent-merged"
	FlagOverlap      = "overlap"
	FlagDirty        = "dirty"
	FlagUnpushed     = "unpushed"
	FlagBehindRemote = "pull"
	FlagLocal        = "local"
	FlagGone         = "gone"
	FlagStale        = "stale"
	FlagEmpty        = "empty"
	FlagDeletable    = "deletable"
	FlagOldBase      = "old-base"
)

// Index builds the name lookup. Call after mutating Branches.
func (m *Map) Index() {
	m.index = make(map[string]*Branch, len(m.Branches))
	for _, b := range m.Branches {
		m.index[b.Name] = b
	}
}

// Branch returns the branch with the given name or nil.
func (m *Map) Branch(name string) *Branch {
	if m.index == nil {
		m.Index()
	}
	return m.index[name]
}

// HeadBranch returns the currently checked out branch or nil.
func (m *Map) HeadBranch() *Branch {
	if m.Repo.Head == "" {
		return nil
	}
	return m.Branch(m.Repo.Head)
}

// Roots returns the branches without a parent, trunks first.
func (m *Map) Roots() []*Branch {
	var roots []*Branch
	for _, b := range m.Branches {
		if b.Parent == "" || m.Branch(b.Parent) == nil {
			roots = append(roots, b)
		}
	}
	sort.SliceStable(roots, func(i, j int) bool {
		if roots[i].Trunk != roots[j].Trunk {
			return roots[i].Trunk
		}
		return false
	})
	return roots
}

// Lineage returns the branch and all of its ancestors, nearest first.
func (m *Map) Lineage(name string) []*Branch {
	var out []*Branch
	seen := map[string]bool{}
	for b := m.Branch(name); b != nil && !seen[b.Name]; b = m.Branch(b.Parent) {
		seen[b.Name] = true
		out = append(out, b)
	}
	return out
}

// Descendants returns all branches below name.
func (m *Map) Descendants(name string) []*Branch {
	var out []*Branch
	var walk func(n string)
	seen := map[string]bool{}
	walk = func(n string) {
		b := m.Branch(n)
		if b == nil {
			return
		}
		for _, c := range b.Children {
			if seen[c] {
				continue
			}
			seen[c] = true
			if cb := m.Branch(c); cb != nil {
				out = append(out, cb)
				walk(c)
			}
		}
	}
	walk(name)
	return out
}

// IssuesFor returns the attention items concerning a branch.
func (m *Map) IssuesFor(name string) []Issue {
	var out []Issue
	for _, is := range m.Attention {
		if is.Branch == name || is.Other == name {
			out = append(out, is)
		}
	}
	return out
}

// HasFlag reports whether the branch carries a flag.
func (b *Branch) HasFlag(flag string) bool {
	for _, f := range b.Flags {
		if f == flag {
			return true
		}
	}
	return false
}

// IsMerged reports whether the branch has been merged.
func (b *Branch) IsMerged() bool { return b.Merged != nil }

// Related reports whether a and b are in an ancestor/descendant relation.
func (m *Map) Related(a, b string) bool {
	for _, x := range m.Lineage(a) {
		if x.Name == b {
			return true
		}
	}
	for _, x := range m.Lineage(b) {
		if x.Name == a {
			return true
		}
	}
	return false
}

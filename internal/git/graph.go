package git

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Graph holds the commits reachable from a set of tips but not from a set of
// excluded refs (typically the trunks). These are the "unmerged" commits and
// are all vb needs to relate branches to each other.
type Graph struct {
	// Order lists commits in topological order, children before parents.
	Order []string
	// Parents maps each commit to its parents. Parents outside the graph are
	// included; use Contains to test membership.
	Parents map[string][]string
	// Truncated is true when the walk hit the commit limit.
	Truncated bool
}

// Contains reports whether oid is part of the graph.
func (g *Graph) Contains(oid string) bool {
	_, ok := g.Parents[oid]
	return ok
}

// UnmergedGraph walks commits reachable from tips and not from exclude.
func (r *Repo) UnmergedGraph(ctx context.Context, tips []string, exclude []string, limit int) (*Graph, error) {
	g := &Graph{Parents: map[string][]string{}}
	if len(tips) == 0 {
		return g, nil
	}
	args := []string{"rev-list", "--parents", "--topo-order"}
	if limit > 0 {
		args = append(args, "--max-count="+strconv.Itoa(limit+1))
	}
	args = append(args, "--stdin")
	var in strings.Builder
	for _, t := range tips {
		in.WriteString(t)
		in.WriteByte('\n')
	}
	for _, e := range exclude {
		in.WriteString("^")
		in.WriteString(e)
		in.WriteByte('\n')
	}
	out, err := r.RunInput(ctx, strings.NewReader(in.String()), args...)
	if err != nil {
		return nil, err
	}
	for _, line := range splitLines(out) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if limit > 0 && len(g.Order) >= limit {
			g.Truncated = true
			break
		}
		g.Order = append(g.Order, fields[0])
		g.Parents[fields[0]] = fields[1:]
	}
	return g, nil
}

// Worktree describes a git work tree.
type Worktree struct {
	Path     string
	HEAD     string
	Branch   string // full ref name, empty when detached
	Bare     bool
	Detached bool
	Locked   bool
	Prunable bool
	Main     bool
}

// Worktrees lists all work trees of the repository.
func (r *Repo) Worktrees(ctx context.Context) ([]Worktree, error) {
	out, err := r.Run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var wts []Worktree
	var cur *Worktree
	for _, line := range splitLines(out) {
		if line == "" {
			cur = nil
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			wts = append(wts, Worktree{Path: filepath.Clean(value), Main: len(wts) == 0})
			cur = &wts[len(wts)-1]
		case "HEAD":
			if cur != nil {
				cur.HEAD = value
			}
		case "branch":
			if cur != nil {
				cur.Branch = value
			}
		case "bare":
			if cur != nil {
				cur.Bare = true
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		case "locked":
			if cur != nil {
				cur.Locked = true
			}
		case "prunable":
			if cur != nil {
				cur.Prunable = true
			}
		}
	}
	return wts, nil
}

// Status summarizes the dirty state of a work tree.
type Status struct {
	Staged     int
	Unstaged   int
	Untracked  int
	Conflicted int
}

// Dirty reports whether the work tree has any changes.
func (s Status) Dirty() bool {
	return s.Staged+s.Unstaged+s.Untracked+s.Conflicted > 0
}

// WorktreeStatus reads the status of the work tree at path.
func (r *Repo) WorktreeStatus(ctx context.Context, path string) (Status, error) {
	out, err := r.RunIn(ctx, path, "status", "--porcelain=v2", "-z", "--untracked-files=normal", "--ignore-submodules=dirty")
	if err != nil {
		return Status{}, err
	}
	return parseStatus(out), nil
}

func parseStatus(out string) Status {
	var s Status
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if e == "" {
			continue
		}
		switch e[0] {
		case '1', '2':
			if len(e) < 4 {
				continue
			}
			x, y := e[2], e[3]
			if x != '.' {
				s.Staged++
			}
			if y != '.' {
				s.Unstaged++
			}
			if e[0] == '2' {
				i++ // rename entries carry the original path as a separate field
			}
		case 'u':
			s.Conflicted++
		case '?':
			s.Untracked++
		}
	}
	return s
}

// StashCounts returns the number of stash entries per branch short name. It
// reads the stash reflog directly and only falls back to git when needed.
func (r *Repo) StashCounts(ctx context.Context) map[string]int {
	counts := map[string]int{}
	var messages []string
	if data, err := os.ReadFile(filepath.Join(r.CommonDir, "logs", "refs", "stash")); err == nil {
		for _, line := range splitLines(string(data)) {
			if _, msg, ok := strings.Cut(line, "\t"); ok {
				messages = append(messages, msg)
			}
		}
	} else if _, statErr := os.Stat(filepath.Join(r.CommonDir, "reftable")); statErr == nil {
		out, code, err := r.RunCode(ctx, "stash", "list", "--format=%gs")
		if err != nil || code != 0 {
			return counts
		}
		messages = splitLines(out)
	}
	for _, line := range messages {
		// "WIP on feat/x: abc123 msg" or "On feat/x: msg"
		var rest string
		switch {
		case strings.HasPrefix(line, "WIP on "):
			rest = strings.TrimPrefix(line, "WIP on ")
		case strings.HasPrefix(line, "On "):
			rest = strings.TrimPrefix(line, "On ")
		default:
			continue
		}
		name, _, ok := strings.Cut(rest, ":")
		if !ok || name == "(no branch)" {
			continue
		}
		counts[name]++
	}
	return counts
}

// Creation describes how a branch was created, read from its reflog.
type Creation struct {
	From    string // the start point named in "branch: Created from X"
	At      time.Time
	Entries int // number of reflog entries
	// Moved is true when the branch tip changed after creation.
	Moved bool
}

// BranchCreation reads the reflog of a local branch. ok is false when no
// reflog is available.
func (r *Repo) BranchCreation(ctx context.Context, branch string) (Creation, bool) {
	path := filepath.Join(r.CommonDir, "logs", "refs", "heads", filepath.FromSlash(branch))
	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		return parseReflogFile(f)
	}
	// Fall back to git for reftable repositories or packed reflogs.
	if _, statErr := os.Stat(filepath.Join(r.CommonDir, "reftable")); statErr != nil {
		return Creation{}, false
	}
	out, code, rerr := r.RunCode(ctx, "reflog", "show", "--format=%H%x00%ct%x00%gs", "refs/heads/"+branch)
	if rerr != nil || code != 0 {
		return Creation{}, false
	}
	lines := splitLines(out)
	if len(lines) == 0 {
		return Creation{}, false
	}
	c := Creation{Entries: len(lines)}
	first := strings.Split(lines[len(lines)-1], "\x00")
	if len(first) == 3 {
		if ts, err := strconv.ParseInt(first[1], 10, 64); err == nil {
			c.At = time.Unix(ts, 0)
		}
		c.From = createdFrom(first[2])
		for _, l := range lines[:len(lines)-1] {
			if parts := strings.Split(l, "\x00"); len(parts) == 3 && parts[0] != first[0] {
				c.Moved = true
				break
			}
		}
	}
	return c, true
}

func parseReflogFile(f *os.File) (Creation, bool) {
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var c Creation
	var firstNew string
	for sc.Scan() {
		line := sc.Text()
		head, msg, _ := strings.Cut(line, "\t")
		fields := strings.Fields(head)
		if len(fields) < 4 {
			continue
		}
		newOID := fields[1]
		c.Entries++
		if c.Entries == 1 {
			firstNew = newOID
			c.From = createdFrom(msg)
			if ts, err := strconv.ParseInt(fields[len(fields)-2], 10, 64); err == nil {
				c.At = time.Unix(ts, 0)
			}
		} else if newOID != firstNew {
			c.Moved = true
		}
	}
	return c, c.Entries > 0
}

func createdFrom(msg string) string {
	const prefix = "branch: Created from "
	if strings.HasPrefix(msg, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(msg, prefix))
	}
	return ""
}

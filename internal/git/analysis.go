package git

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MergeResult is the predicted outcome of merging two commits.
type MergeResult struct {
	Clean bool
	Files []string
}

// PredictMerge predicts whether merging theirs into ours would conflict,
// using `git merge-tree --write-tree`, which never touches a work tree.
// When withFiles is false and git supports --quiet, no objects are written.
func (r *Repo) PredictMerge(ctx context.Context, ours, theirs string, withFiles bool) (MergeResult, error) {
	if !r.HasMergeTree() {
		return MergeResult{}, fmt.Errorf("git %s does not support merge-tree --write-tree (needs 2.38)", r.Version)
	}
	// Exit status 1 means "conflicts"; anything else is a real error whose
	// stderr callers inspect (for example, missing objects in partial clones).
	mergeTree := func(args ...string) (string, int, error) {
		out, code, err := r.run(ctx, r.Root, nil, args...)
		var gerr *Error
		if errors.As(err, &gerr) && gerr.ExitCode == 1 {
			return out, 1, nil
		}
		return out, code, err
	}
	if !withFiles && r.supportsQuietMergeTree(ctx) {
		_, code, err := mergeTree("merge-tree", "--write-tree", "--quiet", ours, theirs)
		if err != nil {
			return MergeResult{}, err
		}
		return MergeResult{Clean: code == 0}, nil
	}
	out, code, err := mergeTree("merge-tree", "--write-tree", "--name-only", "--no-messages", ours, theirs)
	if err != nil {
		return MergeResult{}, err
	}
	switch code {
	case 0:
		return MergeResult{Clean: true}, nil
	case 1:
		lines := splitLines(out)
		var files []string
		seen := map[string]bool{}
		for _, l := range lines[min(1, len(lines)):] {
			if l == "" {
				break
			}
			if !seen[l] {
				seen[l] = true
				files = append(files, l)
			}
		}
		return MergeResult{Clean: false, Files: files}, nil
	default:
		return MergeResult{}, fmt.Errorf("git merge-tree exited %d", code)
	}
}

func (r *Repo) supportsQuietMergeTree(ctx context.Context) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.quietTree != nil {
		return *r.quietTree
	}
	ok := r.Version.AtLeast(2, 44)
	if ok {
		// Confirm with a no-op merge of HEAD with itself.
		_, code, err := r.RunCode(ctx, "merge-tree", "--write-tree", "--quiet", "HEAD", "HEAD")
		ok = err == nil && code == 0
	}
	r.quietTree = &ok
	return ok
}

// ChangedFiles lists files changed in a diff range such as "a...b" or "a b".
func (r *Repo) ChangedFiles(ctx context.Context, revs ...string) ([]string, error) {
	args := append([]string{"diff", "--name-only", "-z", "--no-renames"}, revs...)
	out, err := r.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// FileStat is a changed file with line counts.
type FileStat struct {
	Path      string
	Status    string // A, M, D, R, ...
	Additions int
	Deletions int
	Binary    bool
}

// DiffStat returns per-file changes for a diff range. revs is a diff range such as "a...b" or "a b".
func (r *Repo) DiffStat(ctx context.Context, revs ...string) ([]FileStat, error) {
	statusOut, err := r.Run(ctx, append([]string{"diff", "--name-status", "-z", "--no-renames"}, revs...)...)
	if err != nil {
		return nil, err
	}
	status := map[string]string{}
	parts := strings.Split(statusOut, "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		status[parts[i+1]] = parts[i]
	}
	numOut, err := r.Run(ctx, append([]string{"diff", "--numstat", "-z", "--no-renames"}, revs...)...)
	if err != nil {
		return nil, err
	}
	var stats []FileStat
	for _, entry := range strings.Split(numOut, "\x00") {
		if entry == "" {
			continue
		}
		fields := strings.SplitN(entry, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		fs := FileStat{Path: fields[2], Status: status[fields[2]]}
		if fields[0] == "-" {
			fs.Binary = true
		} else {
			fs.Additions, _ = strconv.Atoi(fields[0])
			fs.Deletions, _ = strconv.Atoi(fields[1])
		}
		stats = append(stats, fs)
	}
	return stats, nil
}

// Commit is a commit summary.
type Commit struct {
	OID         string
	Short       string
	Subject     string
	AuthorName  string
	AuthorEmail string
	CommittedAt time.Time
	CoAuthors   []string
}

const logFormat = "--format=%H%x1f%h%x1f%s%x1f%an%x1f%ae%x1f%ct%x1f%(trailers:key=Co-authored-by,valueonly,separator=%x1d)%x1e"

// Log returns commits in rev range order, newest first.
func (r *Repo) Log(ctx context.Context, limit int, revs ...string) ([]Commit, error) {
	args := []string{"log", logFormat}
	if limit > 0 {
		args = append(args, "--max-count="+strconv.Itoa(limit))
	}
	args = append(args, revs...)
	args = append(args, "--")
	out, err := r.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseLog(out), nil
}

// LogStdin is like Log but reads revisions from stdin, which avoids command
// line length limits when many refs are involved.
func (r *Repo) LogStdin(ctx context.Context, limit int, revs []string) ([]Commit, error) {
	args := []string{"log", logFormat, "--stdin"}
	if limit > 0 {
		args = append(args, "--max-count="+strconv.Itoa(limit))
	}
	out, err := r.RunInput(ctx, strings.NewReader(strings.Join(revs, "\n")+"\n"), args...)
	if err != nil {
		return nil, err
	}
	return parseLog(out), nil
}

func parseLog(out string) []Commit {
	var commits []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.Trim(rec, "\r\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 7 {
			continue
		}
		c := Commit{OID: f[0], Short: f[1], Subject: f[2], AuthorName: f[3], AuthorEmail: f[4]}
		if ts, err := strconv.ParseInt(f[5], 10, 64); err == nil {
			c.CommittedAt = time.Unix(ts, 0)
		}
		for _, co := range strings.Split(f[6], "\x1d") {
			if co = strings.TrimSpace(co); co != "" {
				c.CoAuthors = append(c.CoAuthors, co)
			}
		}
		commits = append(commits, c)
	}
	return commits
}

// PatchID pairs a commit with the stable patch id of its change.
type PatchID struct {
	ID     string `json:"id"`
	Commit string `json:"commit"`
}

// CommitPatchIDs returns patch ids with their commits, newest first.
func (r *Repo) CommitPatchIDs(ctx context.Context, limit int, revs ...string) ([]PatchID, error) {
	args := []string{"log", "-p", "--no-merges", "--no-color", "--no-ext-diff", "--format=commit %H"}
	if limit > 0 {
		args = append(args, "--max-count="+strconv.Itoa(limit))
	}
	args = append(args, revs...)
	args = append(args, "--")
	out, err := r.Pipe(ctx, args, []string{"patch-id", "--stable"})
	if err != nil {
		return nil, err
	}
	var ids []PatchID
	for _, line := range splitLines(out) {
		if id, commit, ok := strings.Cut(line, " "); ok {
			ids = append(ids, PatchID{ID: id, Commit: commit})
		}
	}
	return ids, nil
}

// PatchIDs returns the stable patch ids of the non-merge commits in revs.
func (r *Repo) PatchIDs(ctx context.Context, limit int, revs ...string) ([]string, error) {
	args := []string{"log", "-p", "--no-merges", "--no-color", "--no-ext-diff", "--format=commit %H"}
	if limit > 0 {
		args = append(args, "--max-count="+strconv.Itoa(limit))
	}
	args = append(args, revs...)
	args = append(args, "--")
	out, err := r.Pipe(ctx, args, []string{"patch-id", "--stable"})
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range splitLines(out) {
		if id, _, ok := strings.Cut(line, " "); ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// DiffPatchID returns the stable patch id of a diff range such as
// "trunk...tip", or "" when the diff is empty.
func (r *Repo) DiffPatchID(ctx context.Context, revs ...string) (string, error) {
	args := append([]string{"diff", "--no-color", "--no-ext-diff"}, revs...)
	out, err := r.Pipe(ctx, args, []string{"patch-id", "--stable"})
	if err != nil {
		return "", err
	}
	id, _, _ := strings.Cut(strings.TrimSpace(out), " ")
	return id, nil
}

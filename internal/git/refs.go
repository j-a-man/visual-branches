package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Ref is a branch-like ref read from for-each-ref.
type Ref struct {
	// Name is the full ref name, for example refs/heads/main.
	Name string
	// OID is the commit the ref points to.
	OID string
	// Upstream is the full ref name of the configured upstream, if any.
	Upstream string
	// UpstreamAhead and UpstreamBehind compare the ref with its upstream.
	UpstreamAhead  int
	UpstreamBehind int
	// UpstreamGone is true when an upstream is configured but no longer exists.
	UpstreamGone bool
	CommittedAt  time.Time
	AuthorName   string
	AuthorEmail  string
	Subject      string
	// WorktreePath is set when the branch is checked out in a work tree.
	WorktreePath string
	// Symref is set for symbolic refs such as refs/remotes/origin/HEAD.
	Symref string
	// Head is true for the branch checked out in the current work tree.
	Head bool
}

// Short returns the ref name without the refs/heads/ or refs/remotes/ prefix.
func (r Ref) Short() string { return ShortName(r.Name) }

// IsRemote reports whether the ref is a remote-tracking ref.
func (r Ref) IsRemote() bool { return strings.HasPrefix(r.Name, "refs/remotes/") }

// ShortName strips the standard prefixes from a full ref name.
func ShortName(ref string) string {
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		return strings.TrimPrefix(ref, "refs/heads/")
	case strings.HasPrefix(ref, "refs/remotes/"):
		return strings.TrimPrefix(ref, "refs/remotes/")
	case strings.HasPrefix(ref, "refs/tags/"):
		return strings.TrimPrefix(ref, "refs/tags/")
	}
	return ref
}

const refFormat = "%(refname)%00%(objectname)%00%(upstream)%00%(upstream:track,nobracket)%00%(committerdate:unix)%00%(authorname)%00%(authoremail:trim)%00%(contents:subject)%00%(worktreepath)%00%(symref)%00%(HEAD)"

// Refs lists local branches, remote-tracking branches, and Graphite's
// refs/branch-metadata refs (read in the same call to save a process).
func (r *Repo) Refs(ctx context.Context) ([]Ref, error) {
	out, err := r.Run(ctx, "for-each-ref", "--format="+refFormat, "refs/heads", "refs/remotes", "refs/branch-metadata")
	if err != nil {
		return nil, err
	}
	return parseRefs(out)
}

func parseRefs(out string) ([]Ref, error) {
	var refs []Ref
	for _, line := range splitLines(out) {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x00")
		if len(f) < 11 {
			return nil, fmt.Errorf("unexpected for-each-ref line: %q", line)
		}
		ref := Ref{
			Name:         f[0],
			OID:          f[1],
			Upstream:     f[2],
			AuthorName:   f[5],
			AuthorEmail:  f[6],
			Subject:      f[7],
			WorktreePath: f[8],
			Symref:       f[9],
			Head:         f[10] == "*",
		}
		if ts, err := strconv.ParseInt(f[4], 10, 64); err == nil {
			ref.CommittedAt = time.Unix(ts, 0)
		}
		ref.UpstreamAhead, ref.UpstreamBehind, ref.UpstreamGone = parseTrack(f[3])
		refs = append(refs, ref)
	}
	return refs, nil
}

// parseTrack parses "ahead 1, behind 2", "gone", or "".
func parseTrack(s string) (ahead, behind int, gone bool) {
	s = strings.TrimSpace(s)
	if s == "gone" {
		return 0, 0, true
	}
	for _, part := range strings.Split(s, ",") {
		fields := strings.Fields(part)
		if len(fields) != 2 {
			continue
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		switch fields[0] {
		case "ahead":
			ahead = n
		case "behind":
			behind = n
		}
	}
	return ahead, behind, false
}

// AheadBehind returns, for each of refs, how many commits it has that base
// does not (ahead) and how many commits base has that it does not (behind).
func (r *Repo) AheadBehind(ctx context.Context, base string, refs []string) (map[string][2]int, error) {
	result := make(map[string][2]int, len(refs))
	if len(refs) == 0 {
		return result, nil
	}
	if r.HasAheadBehind() {
		args := []string{"for-each-ref", "--format=%(refname)%00%(ahead-behind:" + base + ")"}
		args = append(args, refs...)
		out, err := r.Run(ctx, args...)
		if err != nil {
			return nil, err
		}
		for _, line := range splitLines(out) {
			name, counts, ok := strings.Cut(line, "\x00")
			if !ok {
				continue
			}
			fields := strings.Fields(counts)
			if len(fields) != 2 {
				continue
			}
			a, _ := strconv.Atoi(fields[0])
			b, _ := strconv.Atoi(fields[1])
			result[name] = [2]int{a, b}
		}
		return result, nil
	}
	for _, ref := range refs {
		out, err := r.Run(ctx, "rev-list", "--left-right", "--count", ref+"..."+base)
		if err != nil {
			return nil, err
		}
		fields := strings.Fields(out)
		if len(fields) != 2 {
			continue
		}
		a, _ := strconv.Atoi(fields[0])
		b, _ := strconv.Atoi(fields[1])
		result[ref] = [2]int{a, b}
	}
	return result, nil
}

// SymbolicRef resolves a symbolic ref, returning "" when it does not exist.
func (r *Repo) SymbolicRef(ctx context.Context, name string) string {
	out, code, err := r.RunCode(ctx, "symbolic-ref", "-q", name)
	if err != nil || code != 0 {
		return ""
	}
	return strings.TrimSpace(out)
}

// Head returns the current branch ref (refs/heads/x) or "" when detached,
// along with the HEAD commit.
func (r *Repo) Head(ctx context.Context) (ref string, oid string) {
	ref = r.SymbolicRef(ctx, "HEAD")
	out, err := r.Run(ctx, "rev-parse", "-q", "--verify", "HEAD")
	if err == nil {
		oid = strings.TrimSpace(out)
	}
	return ref, oid
}

// ConfigList reads every config entry in one call. Keys keep git's casing
// rules: section and variable names are lower case, subsections as written.
func (r *Repo) ConfigList(ctx context.Context) map[string]string {
	out, code, err := r.RunCode(ctx, "config", "--list", "--null")
	result := map[string]string{}
	if err != nil || code != 0 {
		return result
	}
	for _, entry := range strings.Split(out, "\x00") {
		if entry == "" {
			continue
		}
		key, value, _ := strings.Cut(entry, "\n")
		result[key] = value
	}
	return result
}

// ConfigGetRegexp returns all config entries whose keys match pattern.
func (r *Repo) ConfigGetRegexp(ctx context.Context, pattern string) map[string]string {
	out, code, err := r.RunCode(ctx, "config", "--null", "--get-regexp", pattern)
	result := map[string]string{}
	if err != nil || code != 0 {
		return result
	}
	for _, entry := range strings.Split(out, "\x00") {
		if entry == "" {
			continue
		}
		key, value, _ := strings.Cut(entry, "\n")
		result[key] = value
	}
	return result
}

// ConfigGet returns a single config value or "".
func (r *Repo) ConfigGet(ctx context.Context, key string) string {
	out, code, err := r.RunCode(ctx, "config", "--get", key)
	if err != nil || code != 0 {
		return ""
	}
	return strings.TrimSpace(out)
}

// ConfigSet writes a config value in the repository's local config.
func (r *Repo) ConfigSet(ctx context.Context, key, value string) error {
	_, err := r.Run(ctx, "config", "--local", key, value)
	return err
}

// ConfigUnset removes a config value from the repository's local config.
func (r *Repo) ConfigUnset(ctx context.Context, key string) error {
	_, code, err := r.RunCode(ctx, "config", "--local", "--unset", key)
	if err != nil {
		return err
	}
	if code != 0 && code != 5 {
		return fmt.Errorf("git config --unset %s: exit %d", key, code)
	}
	return nil
}

// RemoteURL returns the fetch URL of a remote or "".
func (r *Repo) RemoteURL(ctx context.Context, remote string) string {
	return r.ConfigGet(ctx, "remote."+remote+".url")
}

// Remotes lists configured remote names.
func (r *Repo) Remotes(ctx context.Context) []string {
	out, err := r.Run(ctx, "remote")
	if err != nil {
		return nil
	}
	return splitLines(out)
}

// IsAncestor reports whether a is an ancestor of b.
func (r *Repo) IsAncestor(ctx context.Context, a, b string) bool {
	_, code, err := r.RunCode(ctx, "merge-base", "--is-ancestor", a, b)
	return err == nil && code == 0
}

// MergeBase returns the best common ancestor of a and b or "".
func (r *Repo) MergeBase(ctx context.Context, a, b string) string {
	out, code, err := r.RunCode(ctx, "merge-base", a, b)
	if err != nil || code != 0 {
		return ""
	}
	return strings.TrimSpace(out)
}

// ForkPoint returns the fork point of branch relative to parent using the
// parent's reflog, falling back to the merge base.
func (r *Repo) ForkPoint(ctx context.Context, parent, branch string) string {
	out, code, err := r.RunCode(ctx, "merge-base", "--fork-point", parent, branch)
	if err == nil && code == 0 {
		if s := strings.TrimSpace(out); s != "" {
			return s
		}
	}
	return r.MergeBase(ctx, parent, branch)
}

// HasObject reports whether the object exists locally.
func (r *Repo) HasObject(ctx context.Context, oid string) bool {
	_, code, err := r.RunCode(ctx, "cat-file", "-e", oid+"^{commit}")
	return err == nil && code == 0
}

// UserEmail returns the configured user.email.
func (r *Repo) UserEmail(ctx context.Context) string {
	return r.ConfigGet(ctx, "user.email")
}

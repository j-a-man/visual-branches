package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/j-a-man/visual-branches/internal/git"
	"github.com/j-a-man/visual-branches/internal/infer"
	"github.com/j-a-man/visual-branches/internal/model"
)

// Branch sources.
const (
	SourceLocal  = "local"
	SourceAll    = "all"
	SourceRemote = "remote"
)

// candidate is a branch before it becomes a model.Branch.
type candidate struct {
	ref    git.Ref
	name   string
	remote bool // remote-only branch in "all" mode
	trunk  bool
}

// trunkInfo describes a trunk branch.
type trunkInfo struct {
	name string
	// ref is the node ref (local branch if present, else remote-tracking).
	ref string
	// compare is the ref other branches are compared with: the remote
	// tracking ref when present, since it reflects what is actually merged.
	compare string
	// exclude lists every ref of this trunk, local and remote.
	exclude []string
}

// detectTrunks resolves configured or conventional trunk branches.
func (s *Session) detectTrunks(refs map[string]git.Ref, source string) []trunkInfo {
	remote := s.Config.Remote
	names := append([]string(nil), s.Config.Trunks...)
	if len(names) == 0 {
		if head := refs["refs/remotes/"+remote+"/HEAD"].Symref; head != "" {
			names = append(names, strings.TrimPrefix(head, "refs/remotes/"+remote+"/"))
		}
		for _, n := range []string{"main", "master", "trunk", "develop"} {
			if len(names) > 0 {
				break
			}
			if _, ok := refs["refs/heads/"+n]; ok {
				names = append(names, n)
			} else if _, ok := refs["refs/remotes/"+remote+"/"+n]; ok {
				names = append(names, n)
			}
		}
	}
	var out []trunkInfo
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		local := "refs/heads/" + n
		rem := "refs/remotes/" + remote + "/" + n
		_, hasLocal := refs[local]
		_, hasRemote := refs[rem]
		t := trunkInfo{name: n}
		switch {
		case source == SourceRemote && hasRemote:
			t.ref, t.compare = rem, rem
			t.exclude = []string{rem}
		case hasLocal && hasRemote:
			t.ref, t.compare = local, rem
			t.exclude = []string{local, rem}
		case hasLocal:
			t.ref, t.compare = local, local
			t.exclude = []string{local}
		case hasRemote:
			t.ref, t.compare = rem, rem
			t.exclude = []string{rem}
		default:
			continue
		}
		out = append(out, t)
	}
	return out
}

// selectBranches picks the branch set for the requested source.
func (s *Session) selectBranches(all []git.Ref, trunks []trunkInfo, source string) []candidate {
	remotePrefix := "refs/remotes/" + s.Config.Remote + "/"
	trunkByRef := map[string]string{}
	trunkNames := map[string]bool{}
	for _, t := range trunks {
		trunkByRef[t.ref] = t.name
		trunkNames[t.name] = true
	}
	locals := map[string]bool{}
	for _, r := range all {
		if strings.HasPrefix(r.Name, "refs/heads/") {
			locals[r.Short()] = true
		}
	}
	var out []candidate
	for _, r := range all {
		if r.Symref != "" {
			continue
		}
		isLocal := strings.HasPrefix(r.Name, "refs/heads/")
		isRemote := strings.HasPrefix(r.Name, remotePrefix)
		short := strings.TrimPrefix(r.Name, remotePrefix)
		if isLocal {
			short = r.Short()
		}
		if name, ok := trunkByRef[r.Name]; ok {
			out = append(out, candidate{ref: r, name: name, trunk: true, remote: !isLocal && source != SourceRemote})
			continue
		}
		switch source {
		case SourceRemote:
			if isRemote && !trunkNames[short] {
				out = append(out, candidate{ref: r, name: short})
			}
		case SourceAll:
			if isLocal {
				out = append(out, candidate{ref: r, name: short})
			} else if isRemote && !locals[short] && !trunkNames[short] {
				out = append(out, candidate{ref: r, name: s.Config.Remote + "/" + short, remote: true})
			}
		default:
			if isLocal {
				out = append(out, candidate{ref: r, name: short})
			}
		}
	}
	return out
}

// headName maps a branch to the head ref name used on GitHub.
func (s *Session) headName(c candidate, source string) string {
	if source == SourceAll && c.remote {
		return strings.TrimPrefix(c.name, s.Config.Remote+"/")
	}
	return c.name
}

// explicitParents gathers parents from vb config, git config, and stack tools.
func (s *Session) explicitParents(ctx context.Context, refs map[string]git.Ref, gitConfig map[string]string, names map[string]bool) map[string]infer.Explicit {
	out := map[string]infer.Explicit{}
	set := func(branch, parent, source string) {
		if branch == "" || parent == "" || !names[branch] {
			return
		}
		if _, exists := out[branch]; exists {
			return // higher priority source already set it
		}
		out[branch] = infer.Explicit{Parent: parent, Source: source}
	}
	for b, p := range s.Config.Parents {
		set(b, p, model.SourceConfig)
	}
	for key, value := range gitConfig {
		if strings.HasPrefix(key, "branch.") && strings.HasSuffix(key, ".vbparent") {
			set(strings.TrimSuffix(strings.TrimPrefix(key, "branch."), ".vbparent"), value, model.SourceConfig)
		}
	}
	if !s.Config.Analysis.ReadStackMetadata {
		return out
	}
	for key, value := range gitConfig {
		if strings.HasPrefix(key, "git-town-branch.") && strings.HasSuffix(key, ".parent") {
			set(strings.TrimSuffix(strings.TrimPrefix(key, "git-town-branch."), ".parent"), value, model.SourceGitTown)
		}
	}
	for b, p := range s.graphiteParents(ctx, refs) {
		set(b, p, model.SourceGraphite)
	}
	for b, p := range readMachete(filepath.Join(s.Repo.CommonDir, "machete")) {
		set(b, p, model.SourceMachete)
	}
	return out
}

// graphiteParents reads Graphite's refs/branch-metadata/<branch> blobs. The
// refs come from the main for-each-ref call, so repositories that do not use
// Graphite pay nothing.
func (s *Session) graphiteParents(ctx context.Context, refs map[string]git.Ref) map[string]string {
	out := map[string]string{}
	var oids []string
	branchOf := map[string]string{}
	for name, r := range refs {
		if strings.HasPrefix(name, "refs/branch-metadata/") {
			branchOf[r.OID] = strings.TrimPrefix(name, "refs/branch-metadata/")
			oids = append(oids, r.OID)
		}
	}
	if len(oids) == 0 {
		return out
	}
	batch, err := s.Repo.RunInput(ctx, strings.NewReader(strings.Join(oids, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return out
	}
	rd := bufio.NewReader(strings.NewReader(batch))
	for {
		header, err := rd.ReadString('\n')
		if err != nil {
			break
		}
		fields := strings.Fields(header)
		if len(fields) < 3 {
			continue
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			break
		}
		body := make([]byte, size+1) // content plus the trailing newline
		if _, err := io.ReadFull(rd, body); err != nil {
			break
		}
		var meta struct {
			ParentBranchName string `json:"parentBranchName"`
		}
		if json.Unmarshal(body[:size], &meta) == nil && meta.ParentBranchName != "" {
			out[branchOf[fields[0]]] = meta.ParentBranchName
		}
	}
	return out
}

// readMachete parses git-machete's indentation-based branch layout file.
func readMachete(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	type level struct {
		indent int
		name   string
	}
	var stack []level
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		raw := sc.Text()
		trimmed := strings.TrimLeft(raw, " \t")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		indent := len(raw) - len(trimmed)
		name := strings.Fields(trimmed)[0]
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			out[name] = stack[len(stack)-1].name
		}
		stack = append(stack, level{indent, name})
	}
	return out
}

// resolveStart maps a reflog start point to a branch name in the map.
func (s *Session) resolveStart(from string, names map[string]bool, source string) string {
	if from == "" || from == "HEAD" {
		return ""
	}
	from = strings.TrimPrefix(from, "refs/heads/")
	from = strings.TrimPrefix(from, "refs/remotes/")
	if names[from] {
		return from
	}
	remote := s.Config.Remote + "/"
	if strings.HasPrefix(from, remote) {
		short := strings.TrimPrefix(from, remote)
		if names[short] {
			return short
		}
	} else if source == SourceAll && names[remote+from] {
		return remote + from
	}
	return ""
}

// displayPath renders a worktree path relative to the repository.
func displayPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, "../../../") {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if r, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(r, "..") {
			return "~/" + filepath.ToSlash(r)
		}
	}
	return filepath.ToSlash(path)
}

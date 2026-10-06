package engine

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/j-a-man/visual-branches/internal/model"
)

// Detail is everything `vb show` displays about one branch.
type Detail struct {
	Branch    *model.Branch   `json:"branch"`
	Lineage   []string        `json:"lineage"`
	Commits   []DetailCommit  `json:"commits"`
	MoreCount int             `json:"moreCommits,omitempty"`
	Files     []DetailFile    `json:"files"`
	Additions int             `json:"additions"`
	Deletions int             `json:"deletions"`
	Issues    []model.Issue   `json:"issues,omitempty"`
	Overlaps  []model.Overlap `json:"overlaps,omitempty"`
	CompareTo string          `json:"compareTo,omitempty"`
}

// DetailCommit is a commit on the branch.
type DetailCommit struct {
	OID         string    `json:"oid"`
	Short       string    `json:"short"`
	Subject     string    `json:"subject"`
	Author      string    `json:"author"`
	CommittedAt time.Time `json:"committedAt"`
	CoAuthors   []string  `json:"coAuthors,omitempty"`
}

// DetailFile is a file changed on the branch.
type DetailFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary,omitempty"`
}

const detailCommitLimit = 30

// CompareRef returns the ref a parent is compared at: a trunk's
// remote-tracking ref when present, otherwise the branch ref.
func (s *Session) CompareRef(ctx context.Context, b *model.Branch) string {
	if b.Trunk && !strings.HasPrefix(b.Ref, "refs/remotes/") {
		remote := "refs/remotes/" + s.Config.Remote + "/" + b.Name
		if _, code, err := s.Repo.RunCode(ctx, "rev-parse", "-q", "--verify", remote); err == nil && code == 0 {
			return remote
		}
	}
	return b.Ref
}

// Detail loads commits, files, and related issues for a branch.
func (s *Session) Detail(ctx context.Context, m *model.Map, name string) (*Detail, error) {
	b := m.Branch(name)
	if b == nil {
		return nil, fmt.Errorf("branch %q not found (see `vb` for the list)", name)
	}
	d := &Detail{Branch: b}
	for _, a := range m.Lineage(name)[1:] {
		d.Lineage = append(d.Lineage, a.Name)
	}
	parent := m.Branch(b.Parent)
	var rng []string
	if parent != nil {
		base := s.CompareRef(ctx, parent)
		d.CompareTo = parent.Name
		rng = []string{base + ".." + b.Tip}
		if files, err := s.Repo.DiffStat(ctx, base+"..."+b.Tip); err == nil {
			for _, f := range files {
				d.Files = append(d.Files, DetailFile{Path: f.Path, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions, Binary: f.Binary})
				d.Additions += f.Additions
				d.Deletions += f.Deletions
			}
			sort.SliceStable(d.Files, func(i, j int) bool {
				return d.Files[i].Additions+d.Files[i].Deletions > d.Files[j].Additions+d.Files[j].Deletions
			})
		}
	} else {
		rng = []string{b.Tip}
	}
	limit := detailCommitLimit
	if parent == nil {
		limit = 10
	}
	commits, err := s.Repo.Log(ctx, limit+1, rng...)
	if err != nil {
		return nil, err
	}
	if len(commits) > limit {
		commits = commits[:limit]
		if parent != nil {
			if out, err := s.Repo.Run(ctx, "rev-list", "--count", rng[0]); err == nil {
				if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
					d.MoreCount = n - limit
				}
			}
		}
	}
	for _, c := range commits {
		d.Commits = append(d.Commits, DetailCommit{OID: c.OID, Short: c.Short, Subject: c.Subject, Author: c.AuthorName, CommittedAt: c.CommittedAt, CoAuthors: c.CoAuthors})
	}
	d.Issues = m.IssuesFor(name)
	for _, ov := range m.Overlaps {
		if ov.A == name || ov.B == name {
			d.Overlaps = append(d.Overlaps, ov)
		}
	}
	return d, nil
}

// Touching lists branches that change any of the given paths, excluding
// exclude. Paths may be files or directory prefixes.
func Touching(m *model.Map, paths []string, exclude string) map[string][]string {
	out := map[string][]string{}
	norm := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimPrefix(strings.ReplaceAll(p, "\\", "/"), "./")
		if p != "" {
			norm = append(norm, p)
		}
	}
	for _, b := range m.Branches {
		if b.Name == exclude || b.Trunk || b.Merged != nil {
			continue
		}
		for _, f := range b.Files {
			for _, p := range norm {
				if f == p || strings.HasPrefix(f, strings.TrimSuffix(p, "/")+"/") {
					out[b.Name] = append(out[b.Name], f)
					break
				}
			}
		}
	}
	return out
}

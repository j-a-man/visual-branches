package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/j-a-man/visual-branches/internal/cache"
	"github.com/j-a-man/visual-branches/internal/model"
)

// BranchState is the part of a branch that matters for change detection.
type BranchState struct {
	Tip      string   `json:"tip"`
	Parent   string   `json:"parent,omitempty"`
	Ahead    int      `json:"ahead"`
	Behind   int      `json:"behind"`
	Upstream string   `json:"upstream,omitempty"`
	PR       string   `json:"pr,omitempty"`
	Dirty    int      `json:"dirty,omitempty"`
	Merged   string   `json:"merged,omitempty"`
	Flags    []string `json:"flags,omitempty"`
}

// Snapshot is a compact record of a map used to compute deltas.
type Snapshot struct {
	State     string                 `json:"state"`
	Branches  map[string]BranchState `json:"branches"`
	Attention []string               `json:"attention"`
}

// TakeSnapshot extracts the change-relevant state of a map.
func TakeSnapshot(m *model.Map) Snapshot {
	s := Snapshot{Branches: map[string]BranchState{}}
	for _, br := range m.Branches {
		st := BranchState{
			Tip:    br.Tip,
			Parent: br.Parent,
			Ahead:  br.Ahead,
			Behind: br.Behind,
			Dirty:  br.Worktree.Changes(),
			Flags:  br.Flags,
		}
		if up := br.Upstream; up != nil {
			switch {
			case up.Gone:
				st.Upstream = "gone"
			default:
				st.Upstream = fmt.Sprintf("+%d/-%d", up.Ahead, up.Behind)
			}
		}
		if pr := br.PR; pr != nil {
			st.PR = fmt.Sprintf("#%d %s %s ci:%s", pr.Number, prWord(pr), reviewWord(pr.Review), pr.CI)
		}
		if br.Merged != nil {
			st.Merged = br.Merged.How
		}
		s.Branches[br.Name] = st
	}
	for _, is := range m.Attention {
		s.Attention = append(s.Attention, is.Severity+" "+is.Message)
	}
	return s
}

func prWord(pr *model.PR) string {
	if pr.Draft && pr.State == model.PROpen {
		return "draft"
	}
	return pr.State
}

func reviewWord(r string) string {
	if r == "" {
		return "-"
	}
	return r
}

// StateToken is a short hash of the change-relevant state of a map.
func StateToken(m *model.Map) string {
	s := TakeSnapshot(m)
	s.State = ""
	data, _ := json.Marshal(s)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:10]
}

const snapshotDir = "snapshots"

// SaveSnapshot stores the map's snapshot so later runs can diff against it.
func SaveSnapshot(store *cache.Store, m *model.Map) {
	s := TakeSnapshot(m)
	s.State = m.State
	store.Save(snapshotDir+"/"+m.State+".json", s)
	store.Prune(snapshotDir, 50)
}

// LoadSnapshot loads a stored snapshot by state token.
func LoadSnapshot(store *cache.Store, token string) (Snapshot, bool) {
	var s Snapshot
	if token == "" || strings.ContainsAny(token, `/\.`) {
		return s, false
	}
	_, ok := store.Load(snapshotDir+"/"+token+".json", &s)
	return s, ok
}

// Change is one difference between two snapshots.
type Change struct {
	Kind   string `json:"kind"` // added, removed, changed, attention
	Branch string `json:"branch,omitempty"`
	Detail string `json:"detail"`
}

// Diff lists what changed from old to cur.
func Diff(old, cur Snapshot) []Change {
	var out []Change
	names := map[string]bool{}
	for n := range old.Branches {
		names[n] = true
	}
	for n := range cur.Branches {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		o, inOld := old.Branches[n]
		c, inCur := cur.Branches[n]
		switch {
		case !inOld:
			d := "new branch"
			if c.Parent != "" {
				d += " on " + c.Parent
			}
			if c.Ahead > 0 {
				d += fmt.Sprintf(" +%d", c.Ahead)
			}
			out = append(out, Change{Kind: "added", Branch: n, Detail: d})
		case !inCur:
			out = append(out, Change{Kind: "removed", Branch: n, Detail: "branch deleted"})
		default:
			var parts []string
			if o.Tip != c.Tip {
				parts = append(parts, fmt.Sprintf("tip %s->%s", short(o.Tip), short(c.Tip)))
			}
			if o.Parent != c.Parent {
				parts = append(parts, fmt.Sprintf("parent %s->%s", dash(o.Parent), dash(c.Parent)))
			}
			if o.Ahead != c.Ahead || o.Behind != c.Behind {
				parts = append(parts, fmt.Sprintf("ahead/behind +%d/-%d -> +%d/-%d", o.Ahead, o.Behind, c.Ahead, c.Behind))
			}
			if o.Upstream != c.Upstream {
				parts = append(parts, fmt.Sprintf("upstream %s -> %s", dash(o.Upstream), dash(c.Upstream)))
			}
			if o.PR != c.PR {
				parts = append(parts, fmt.Sprintf("pr %s -> %s", dash(o.PR), dash(c.PR)))
			}
			if o.Dirty != c.Dirty {
				parts = append(parts, fmt.Sprintf("dirty %d -> %d", o.Dirty, c.Dirty))
			}
			if o.Merged != c.Merged {
				parts = append(parts, fmt.Sprintf("merged %s -> %s", dash(o.Merged), dash(c.Merged)))
			}
			if strings.Join(o.Flags, ",") != strings.Join(c.Flags, ",") {
				parts = append(parts, fmt.Sprintf("flags [%s] -> [%s]", strings.Join(o.Flags, " "), strings.Join(c.Flags, " ")))
			}
			if len(parts) > 0 {
				out = append(out, Change{Kind: "changed", Branch: n, Detail: strings.Join(parts, "; ")})
			}
		}
	}
	oldAtt := map[string]bool{}
	for _, a := range old.Attention {
		oldAtt[a] = true
	}
	for _, a := range cur.Attention {
		if !oldAtt[a] {
			out = append(out, Change{Kind: "attention", Detail: "new: " + a})
		}
	}
	curAtt := map[string]bool{}
	for _, a := range cur.Attention {
		curAtt[a] = true
	}
	for _, a := range old.Attention {
		if !curAtt[a] {
			out = append(out, Change{Kind: "attention", Detail: "resolved: " + a})
		}
	}
	return out
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

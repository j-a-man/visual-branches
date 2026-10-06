package infer

import (
	"testing"
	"time"

	"github.com/j-a-man/visual-branches/internal/git"
	"github.com/j-a-man/visual-branches/internal/model"
)

// graph builds a Graph from "child:parent parent" specs, listed children
// before parents. Parents not listed are outside the graph (on a trunk).
func graph(specs ...[]string) *git.Graph {
	g := &git.Graph{Parents: map[string][]string{}}
	for _, s := range specs {
		g.Order = append(g.Order, s[0])
		g.Parents[s[0]] = s[1:]
	}
	return g
}

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func check(t *testing.T, got map[string]Result, name, parent, source, conf string) {
	t.Helper()
	r := got[name]
	if r.Parent != parent || (source != "" && r.Source != source) || (conf != "" && r.Confidence != conf) {
		t.Errorf("%s: got parent=%s source=%s confidence=%s, want %s %s %s", name, r.Parent, r.Source, r.Confidence, parent, source, conf)
	}
}

func TestStack(t *testing.T) {
	g := graph(
		[]string{"c1", "b2"},
		[]string{"b2", "b1"},
		[]string{"b1", "a1"},
		[]string{"a1", "m0"},
	)
	got, _ := Infer(Input{
		Nodes:  []Node{{Name: "main", Tip: "m0", Trunk: true}, {Name: "a", Tip: "a1"}, {Name: "b", Tip: "b2"}, {Name: "c", Tip: "c1"}},
		Trunks: []string{"main"},
		Graph:  g,
	})
	check(t, got, "a", "main", model.SourceTrunk, model.High)
	check(t, got, "b", "a", model.SourceAncestry, model.High)
	check(t, got, "c", "b", model.SourceAncestry, model.High)
}

// A parent that gained commits after its child branched off looks, from
// history alone, like it forked from the child. Creation order decides,
// regardless of how the names sort.
func TestMovedParentIsNotTheChildsChild(t *testing.T) {
	g := graph(
		[]string{"z3", "z2"}, // zeta moved on after alpha branched
		[]string{"b1", "z2"}, // alpha branched from zeta at z2
		[]string{"z2", "z1"},
		[]string{"z1", "m0"},
	)
	got, _ := Infer(Input{
		Nodes:     []Node{{Name: "main", Tip: "m0", Trunk: true}, {Name: "zeta", Tip: "z3"}, {Name: "alpha", Tip: "b1"}},
		Trunks:    []string{"main"},
		Graph:     g,
		CreatedAt: map[string]time.Time{"zeta": t0, "alpha": t0.Add(time.Hour)},
	})
	check(t, got, "zeta", "main", model.SourceTrunk, model.High)
	check(t, got, "alpha", "zeta", model.SourceAncestry, model.Medium)

	// Without creation times the answer is a guess, and says so.
	got, _ = Infer(Input{
		Nodes:  []Node{{Name: "main", Tip: "m0", Trunk: true}, {Name: "zeta", Tip: "z3"}, {Name: "alpha", Tip: "b1"}},
		Trunks: []string{"main"},
		Graph:  g,
	})
	for _, n := range []string{"zeta", "alpha"} {
		if got[n].Parent == "main" && got[n].Confidence != model.Low {
			t.Errorf("%s detached without creation order should be low confidence: %+v", n, got[n])
		}
	}
}

func TestPRBaseBeatsHistory(t *testing.T) {
	g := graph(
		[]string{"z3", "z2"},
		[]string{"b1", "z2"},
		[]string{"z2", "z1"},
		[]string{"z1", "m0"},
	)
	got, _ := Infer(Input{
		Nodes:  []Node{{Name: "main", Tip: "m0", Trunk: true}, {Name: "zeta", Tip: "z3"}, {Name: "alpha", Tip: "b1"}},
		Trunks: []string{"main"},
		Graph:  g,
		PRBase: map[string]string{"alpha": "zeta"},
	})
	check(t, got, "alpha", "zeta", model.SourcePR, model.High)
	check(t, got, "zeta", "main", model.SourceTrunk, model.High)
}

func TestExplicitAndMissingParent(t *testing.T) {
	g := graph([]string{"a1", "m0"}, []string{"b1", "m0"})
	got, _ := Infer(Input{
		Nodes:    []Node{{Name: "main", Tip: "m0", Trunk: true}, {Name: "a", Tip: "a1"}, {Name: "b", Tip: "b1"}},
		Trunks:   []string{"main"},
		Graph:    g,
		Explicit: map[string]Explicit{"b": {Parent: "a", Source: model.SourceConfig}, "a": {Parent: "gone", Source: model.SourceConfig}},
	})
	check(t, got, "b", "a", model.SourceConfig, model.High)
	check(t, got, "a", "main", model.SourceTrunk, "")
}

func TestExplicitCycleIsBroken(t *testing.T) {
	g := graph([]string{"a1", "m0"}, []string{"b1", "m0"})
	got, _ := Infer(Input{
		Nodes:    []Node{{Name: "main", Tip: "m0", Trunk: true}, {Name: "a", Tip: "a1"}, {Name: "b", Tip: "b1"}},
		Trunks:   []string{"main"},
		Graph:    g,
		Explicit: map[string]Explicit{"b": {Parent: "a", Source: model.SourceConfig}, "a": {Parent: "b", Source: model.SourceConfig}},
	})
	for _, n := range []string{"a", "b"} {
		seen := map[string]bool{}
		for cur := n; cur != "" && cur != "main"; cur = got[cur].Parent {
			if seen[cur] {
				t.Fatalf("cycle remains through %s: %+v", cur, got)
			}
			seen[cur] = true
		}
	}
}

func TestMultipleTrunks(t *testing.T) {
	// develop is ahead of main; feature forked from develop.
	g := graph([]string{"f1", "d1"})
	got, _ := Infer(Input{
		Nodes:      []Node{{Name: "main", Tip: "m0", Trunk: true}, {Name: "develop", Tip: "d1", Trunk: true}, {Name: "feat", Tip: "f1"}},
		Trunks:     []string{"main", "develop"},
		Graph:      g,
		TrunkAhead: map[string]map[string]int{"feat": {"main": 5, "develop": 1}},
	})
	check(t, got, "feat", "develop", model.SourceTrunk, model.High)
}

func TestContainment(t *testing.T) {
	g := graph([]string{"b1", "a1"}, []string{"a1", "m0"})
	_, c := Infer(Input{
		Nodes:  []Node{{Name: "main", Tip: "m0", Trunk: true}, {Name: "a", Tip: "a1"}, {Name: "b", Tip: "b1"}},
		Trunks: []string{"main"},
		Graph:  g,
	})
	if !c.Contains("b", "a1") || c.Contains("a", "b1") {
		t.Error("containment wrong")
	}
	if got := c.Branches("a1"); len(got) != 2 {
		t.Errorf("Branches(a1) = %v", got)
	}
}

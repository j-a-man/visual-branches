package render

import (
	"strings"

	"github.com/j-a-man/visual-branches/internal/engine"
	"github.com/j-a-man/visual-branches/internal/model"
)

// Filter selects which branches a view shows.
type Filter struct {
	// Focus limits the view to a branch's lineage and descendants.
	Focus string
	// ShowMerged includes merged branches.
	ShowMerged bool
	// Hide lists branch globs to hide.
	Hide []string
	// Mine limits the view to branches whose tip was authored by this email.
	Mine string
	// Match, when set, keeps only matching branches and their ancestors.
	Match func(*model.Branch) bool
}

// Row is a visible branch with its position in the visible tree.
type Row struct {
	B      *model.Branch
	Depth  int
	Parent string // nearest visible ancestor
	// Guides holds, for each ancestor level, whether that ancestor was the
	// last child at its level (used to draw tree lines).
	Guides []bool
	Last   bool
}

// Rows returns the visible branches in tree order.
func Rows(m *model.Map, f Filter) []Row {
	visible := map[string]bool{}
	for _, b := range m.Branches {
		visible[b.Name] = true
	}
	keepAncestors := func(keep map[string]bool) {
		for name := range keep {
			for _, a := range m.Lineage(name) {
				keep[a.Name] = true
			}
		}
	}
	if f.Focus != "" && m.Branch(f.Focus) != nil {
		keep := map[string]bool{f.Focus: true}
		for _, d := range m.Descendants(f.Focus) {
			keep[d.Name] = true
		}
		keepAncestors(keep)
		for n := range visible {
			if !keep[n] {
				visible[n] = false
			}
		}
	}
	if f.Mine != "" {
		keep := map[string]bool{}
		for _, b := range m.Branches {
			if b.Trunk || b.Head || strings.EqualFold(b.AuthorEmail, f.Mine) {
				keep[b.Name] = true
			}
		}
		keepAncestors(keep)
		for n := range visible {
			if !keep[n] {
				visible[n] = false
			}
		}
	}
	if f.Match != nil {
		keep := map[string]bool{}
		for _, b := range m.Branches {
			if f.Match(b) {
				keep[b.Name] = true
			}
		}
		keepAncestors(keep)
		for n := range visible {
			if !keep[n] {
				visible[n] = false
			}
		}
	}
	for _, b := range m.Branches {
		if b.Trunk || b.Head || b.Name == f.Focus {
			continue
		}
		if len(f.Hide) > 0 && engine.Hidden(f.Hide, b) {
			visible[b.Name] = false
		}
	}
	if !f.ShowMerged {
		for _, b := range m.Branches {
			if b.Merged == nil || b.Head || b.Name == f.Focus {
				continue
			}
			// Keep merged branches that still have visible unmerged children,
			// so the structure (and the parent-merged flag) stays readable.
			hasLive := false
			for _, d := range m.Descendants(b.Name) {
				if visible[d.Name] && d.Merged == nil {
					hasLive = true
					break
				}
			}
			if !hasLive {
				visible[b.Name] = false
			}
		}
	}

	// Build the visible forest in the map's order.
	parentOf := map[string]string{}
	children := map[string][]string{}
	var roots []string
	for _, b := range m.Branches {
		if !visible[b.Name] {
			continue
		}
		p := ""
		for a := m.Branch(b.Parent); a != nil; a = m.Branch(a.Parent) {
			if visible[a.Name] {
				p = a.Name
				break
			}
			if a.Parent == a.Name {
				break
			}
		}
		parentOf[b.Name] = p
		if p == "" {
			roots = append(roots, b.Name)
		} else {
			children[p] = append(children[p], b.Name)
		}
	}
	var rows []Row
	var walk func(name string, depth int, guides []bool, last bool)
	walk = func(name string, depth int, guides []bool, last bool) {
		rows = append(rows, Row{B: m.Branch(name), Depth: depth, Parent: parentOf[name], Guides: guides, Last: last})
		kids := children[name]
		for i, k := range kids {
			g := append(append([]bool(nil), guides...), last)
			walk(k, depth+1, g, i == len(kids)-1)
		}
	}
	for i, r := range roots {
		walk(r, 0, nil, i == len(roots)-1)
	}
	return rows
}

// Prefix renders the tree guide for a row.
func (r Row) Prefix(ic Icons) string {
	if r.Depth == 0 {
		return ""
	}
	s := ""
	// Guides[0] belongs to the root level, which draws no line.
	for _, last := range r.Guides[1:] {
		if last {
			s += ic.Blank
		} else {
			s += ic.Pipe
		}
	}
	if r.Last {
		return s + ic.Last
	}
	return s + ic.Tee
}

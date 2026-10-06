package render

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/j-a-man/visual-branches/internal/model"
)

// Node statuses drive the accent color of a card.
const (
	StatusTrunk   = "trunk"
	StatusDanger  = "danger"
	StatusWarning = "warning"
	StatusReady   = "ready"
	StatusMerged  = "merged"
	StatusNormal  = "normal"
)

// LayoutNode is a positioned branch card.
type LayoutNode struct {
	Name   string  `json:"name"`
	Parent string  `json:"parent,omitempty"`
	Depth  int     `json:"depth"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	W      float64 `json:"w"`
	H      float64 `json:"h"`
	Status string  `json:"status"`
	Label  string  `json:"label"`
	Meta   string  `json:"meta"`
	Head   bool    `json:"head,omitempty"`
	Agent  string  `json:"agent,omitempty"`
}

// LayoutEdge connects a parent card to a child card.
type LayoutEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	D      string `json:"d"`
	Status string `json:"status"`
}

// Layout is a left-to-right tree of branch cards.
type Layout struct {
	Nodes  []LayoutNode `json:"nodes"`
	Edges  []LayoutEdge `json:"edges"`
	Width  float64      `json:"width"`
	Height float64      `json:"height"`
}

// Layout metrics, in pixels. Text uses a monospace font so widths can be
// computed without measuring.
const (
	cardH      = 54.0
	gapY       = 14.0
	gapX       = 64.0
	margin     = 24.0
	nameCharW  = 7.8  // 13px monospace
	metaCharW  = 6.6  // 11px monospace
	cardPadX   = 34.0 // left accent bar and padding on both sides
	minCardW   = 150.0
	maxCardW   = 420.0
	maxNameLen = 46
)

// NodeStatus summarizes a branch for coloring.
func NodeStatus(m *model.Map, b *model.Branch) string {
	switch {
	case b.Trunk:
		return StatusTrunk
	case b.Merged != nil:
		return StatusMerged
	}
	best := StatusNormal
	for _, is := range m.Attention {
		if is.Branch != b.Name && is.Other != b.Name {
			continue
		}
		switch is.Severity {
		case model.SevHigh:
			return StatusDanger
		case model.SevMedium:
			best = StatusWarning
		case model.SevReady:
			if best == StatusNormal {
				best = StatusReady
			}
		}
	}
	return best
}

// MetaLine is the second line of a card: PR, review, CI, ahead/behind, age.
func MetaLine(b *model.Branch, now time.Time) string {
	var parts []string
	if pr := b.PR; pr != nil {
		s := fmt.Sprintf("#%d", pr.Number)
		switch {
		case pr.State == model.PRMerged:
			s += " merged"
		case pr.State == model.PRClosed:
			s += " closed"
		case pr.Draft:
			s += " draft"
		case pr.Review == model.ReviewApproved:
			s += " approved"
		case pr.Review == model.ReviewChanges:
			s += " changes"
		case pr.Review == model.ReviewRequired:
			s += " review"
		}
		if pr.State == model.PROpen {
			switch pr.CI {
			case model.CIPass:
				s += " ✓"
			case model.CIFail:
				s += " ✗"
			case model.CIPending:
				s += " ●"
			}
		}
		parts = append(parts, s)
	} else if b.Merged != nil {
		parts = append(parts, "merged")
	}
	if !b.Trunk && b.Merged == nil && (b.Ahead > 0 || b.Behind > 0) {
		ab := ""
		if b.Ahead > 0 {
			ab = fmt.Sprintf("↑%d", b.Ahead)
		}
		if b.Behind > 0 {
			if ab != "" {
				ab += " "
			}
			ab += fmt.Sprintf("↓%d", b.Behind)
		}
		parts = append(parts, ab)
	}
	parts = append(parts, Age(now.Sub(b.CommittedAt)))
	if len(b.Flags) > 0 {
		f := b.Flags[0]
		if f != model.FlagLocal {
			parts = append(parts, f)
		}
	}
	return strings.Join(parts, " · ")
}

// ComputeLayout positions the visible rows as a left-to-right tree.
func ComputeLayout(m *model.Map, rows []Row, now time.Time) Layout {
	var lay Layout
	if len(rows) == 0 {
		return lay
	}
	idx := map[string]int{}
	children := map[string][]string{}
	for _, r := range rows {
		label := Truncate(r.B.Name, maxNameLen, "…")
		meta := MetaLine(r.B, now)
		w := max(float64(utf8.RuneCountInString(label))*nameCharW, float64(utf8.RuneCountInString(meta))*metaCharW) + cardPadX
		w = min(max(w, minCardW), maxCardW)
		idx[r.B.Name] = len(lay.Nodes)
		lay.Nodes = append(lay.Nodes, LayoutNode{
			Name: r.B.Name, Parent: r.Parent, Depth: r.Depth, W: w, H: cardH,
			Status: NodeStatus(m, r.B), Label: label, Meta: meta, Head: r.B.Head, Agent: r.B.Agent,
		})
		if r.Parent != "" {
			children[r.Parent] = append(children[r.Parent], r.B.Name)
		}
	}
	// Column x positions from the widest card at each depth.
	colW := map[int]float64{}
	maxDepth := 0
	for _, n := range lay.Nodes {
		colW[n.Depth] = max(colW[n.Depth], n.W)
		maxDepth = max(maxDepth, n.Depth)
	}
	colX := make([]float64, maxDepth+1)
	x := margin
	for d := 0; d <= maxDepth; d++ {
		colX[d] = x
		x += colW[d] + gapX
	}
	lay.Width = x - gapX + margin

	// Leaves take successive rows; parents center on their children.
	slot := 0
	var place func(name string) float64
	place = func(name string) float64 {
		n := &lay.Nodes[idx[name]]
		n.X = colX[n.Depth]
		kids := children[name]
		if len(kids) == 0 {
			n.Y = margin + float64(slot)*(cardH+gapY)
			slot++
			return n.Y
		}
		first := place(kids[0])
		last := first
		for _, k := range kids[1:] {
			last = place(k)
		}
		n.Y = (first + last) / 2
		return n.Y
	}
	for _, r := range rows {
		if r.Parent == "" {
			place(r.B.Name)
		}
	}
	lay.Height = margin*2 + float64(slot)*(cardH+gapY) - gapY

	for _, n := range lay.Nodes {
		if n.Parent == "" {
			continue
		}
		p := lay.Nodes[idx[n.Parent]]
		x1, y1 := p.X+p.W, p.Y+cardH/2
		x2, y2 := n.X, n.Y+cardH/2
		dx := (x2 - x1) / 2
		lay.Edges = append(lay.Edges, LayoutEdge{
			From: p.Name, To: n.Name, Status: n.Status,
			D: fmt.Sprintf("M%.1f %.1f C%.1f %.1f %.1f %.1f %.1f %.1f", x1, y1, x1+dx, y1, x2-dx, y2, x2, y2),
		})
	}
	return lay
}

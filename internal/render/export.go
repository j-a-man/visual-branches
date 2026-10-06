package render

import (
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/j-a-man/visual-branches/internal/model"
	"github.com/j-a-man/visual-branches/internal/theme"
)

// ExportOptions configure diagram exports.
type ExportOptions struct {
	Theme  theme.Theme
	Now    time.Time
	Filter Filter
	// Title is shown above SVG output; empty uses the repository name.
	Title string
}

func statusColor(t theme.Theme, status string) string {
	switch status {
	case StatusTrunk:
		return t.Hex("text")
	case StatusDanger:
		return t.Hex("danger")
	case StatusWarning:
		return t.Hex("warning")
	case StatusReady:
		return t.Hex("success")
	case StatusMerged:
		return t.Hex("merged")
	}
	return t.Hex("tree")
}

// Mermaid renders the map as a Mermaid flowchart, which GitHub renders
// natively in Markdown, issues, and pull request comments.
func Mermaid(m *model.Map, o ExportOptions) string {
	rows := Rows(m, o.Filter)
	t := o.Theme
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	id := map[string]string{}
	for i, r := range rows {
		id[r.B.Name] = fmt.Sprintf("n%d", i)
	}
	for _, r := range rows {
		label := mermaidEscape(r.B.Name)
		if meta := MetaLine(r.B, o.Now); meta != "" {
			label += "<br/>" + mermaidEscape(meta)
		}
		class := NodeStatus(m, r.B)
		if r.B.Head {
			class += "_head"
		}
		fmt.Fprintf(&b, "  %s[\"%s\"]:::%s\n", id[r.B.Name], label, class)
	}
	for _, r := range rows {
		if r.Parent != "" {
			fmt.Fprintf(&b, "  %s --> %s\n", id[r.Parent], id[r.B.Name])
		}
	}
	surface, text, border := t.Hex("surface"), t.Hex("text"), t.Hex("border")
	for _, st := range []string{StatusTrunk, StatusDanger, StatusWarning, StatusReady, StatusMerged, StatusNormal} {
		stroke := statusColor(t, st)
		if st == StatusNormal {
			stroke = border
		}
		weight := ""
		if st == StatusTrunk {
			weight = ",font-weight:bold"
		}
		fmt.Fprintf(&b, "  classDef %s fill:%s,stroke:%s,color:%s,stroke-width:1.5px%s\n", st, surface, stroke, text, weight)
		fmt.Fprintf(&b, "  classDef %s_head fill:%s,stroke:%s,color:%s,stroke-width:3px%s\n", st, surface, t.Hex("accent"), text, weight)
	}
	fmt.Fprintf(&b, "  linkStyle default stroke:%s,stroke-width:1.5px\n", t.Hex("tree"))
	return b.String()
}

func mermaidEscape(s string) string {
	r := strings.NewReplacer(`"`, "#quot;", "#", "#35;", "<", "#lt;", ">", "#gt;")
	return r.Replace(s)
}

// DOT renders the map as a Graphviz digraph.
func DOT(m *model.Map, o ExportOptions) string {
	rows := Rows(m, o.Filter)
	t := o.Theme
	var b strings.Builder
	b.WriteString("digraph vb {\n")
	fmt.Fprintf(&b, "  rankdir=LR;\n  bgcolor=%q;\n  pad=0.4;\n  nodesep=0.25;\n  ranksep=0.6;\n", t.Hex("bg"))
	fmt.Fprintf(&b, "  node [shape=box, style=\"rounded,filled\", fontname=\"monospace\", fontsize=11, fillcolor=%q, color=%q, fontcolor=%q, penwidth=1.5];\n",
		t.Hex("surface"), t.Hex("border"), t.Hex("text"))
	fmt.Fprintf(&b, "  edge [color=%q, arrowhead=none, penwidth=1.4];\n", t.Hex("tree"))
	for _, r := range rows {
		st := NodeStatus(m, r.B)
		color := statusColor(t, st)
		if st == StatusNormal {
			color = t.Hex("border")
		}
		if r.B.Head {
			color = t.Hex("accent")
		}
		label := dotEscape(r.B.Name) + `\n` + dotEscape(MetaLine(r.B, o.Now))
		fmt.Fprintf(&b, "  %q [label=\"%s\", color=%q];\n", r.B.Name, label, color)
	}
	for _, r := range rows {
		if r.Parent != "" {
			fmt.Fprintf(&b, "  %q -> %q;\n", r.Parent, r.B.Name)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// dotEscape escapes text for a double-quoted DOT string.
func dotEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// SVG renders the map as a standalone SVG image.
func SVG(m *model.Map, o ExportOptions) string {
	t := o.Theme
	rows := Rows(m, o.Filter)
	lay := ComputeLayout(m, rows, o.Now)
	title := o.Title
	if title == "" {
		title = m.Repo.Name
	}
	const titleH = 36.0
	w := max(lay.Width, 320)
	h := lay.Height + titleH
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f" font-family="ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace">`+"\n", w, h, w, h)
	fmt.Fprintf(&b, `<rect width="100%%" height="100%%" rx="12" fill="%s"/>`+"\n", t.Hex("bg"))
	fmt.Fprintf(&b, `<text x="%.0f" y="27" font-size="13" font-weight="700" fill="%s">%s</text>`+"\n", margin, t.Hex("accent"), html.EscapeString(title))
	sub := fmt.Sprintf("%d branches", len(rows))
	if len(m.Repo.Trunks) > 0 {
		sub = m.Repo.Trunks[0] + " · " + sub
	}
	fmt.Fprintf(&b, `<text x="%.0f" y="27" font-size="11" fill="%s">%s</text>`+"\n",
		margin+float64(len([]rune(title)))*7.8+14, t.Hex("muted"), html.EscapeString(sub))
	fmt.Fprintf(&b, `<g transform="translate(0 %.0f)">`+"\n", titleH-margin/2)
	b.WriteString(`<g fill="none" stroke-width="1.5" stroke-linecap="round">` + "\n")
	for _, e := range lay.Edges {
		fmt.Fprintf(&b, `<path d="%s" stroke="%s"/>`+"\n", e.D, t.Hex("tree"))
	}
	b.WriteString("</g>\n")
	for _, n := range lay.Nodes {
		stroke := t.Hex("border")
		if n.Head {
			stroke = t.Hex("accent")
		}
		fmt.Fprintf(&b, `<g transform="translate(%.1f %.1f)">`, n.X, n.Y)
		fmt.Fprintf(&b, `<rect width="%.1f" height="%.0f" rx="8" fill="%s" stroke="%s" stroke-width="%s"/>`,
			n.W, n.H, t.Hex("surface"), stroke, map[bool]string{true: "2", false: "1"}[n.Head])
		fmt.Fprintf(&b, `<rect x="6" y="10" width="3" height="%.0f" rx="1.5" fill="%s"/>`, n.H-20, statusColor(t, n.Status))
		nameColor := t.Hex("text")
		if n.Status == StatusMerged {
			nameColor = t.Hex("muted")
		}
		weight := "600"
		if n.Status == StatusTrunk {
			weight = "700"
		}
		fmt.Fprintf(&b, `<text x="20" y="23" font-size="13" font-weight="%s" fill="%s">%s</text>`, weight, nameColor, html.EscapeString(n.Label))
		fmt.Fprintf(&b, `<text x="20" y="41" font-size="11" fill="%s">%s</text>`, t.Hex("subtle"), html.EscapeString(n.Meta))
		b.WriteString("</g>\n")
	}
	b.WriteString("</g>\n</svg>\n")
	return b.String()
}

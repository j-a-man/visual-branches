package render

import (
	"fmt"
	"strings"

	"github.com/j-a-man/visual-branches/internal/engine"
	"github.com/j-a-man/visual-branches/internal/model"
)

// StatusLine renders the current branch with a user template. Placeholders:
// {branch} {parent} {ab} {pr} {ci} {review} {dirty} {attention}. Every
// placeholder except {branch} renders with a leading space or not at all.
func StatusLine(m *model.Map, format string, st Styles, ic Icons) string {
	b := m.HeadBranch()
	name := m.Repo.Head
	if b == nil {
		if m.Repo.HeadDetached {
			name = "detached@" + shortOID(m.Repo.HeadOID)
		}
	}
	vals := map[string]string{"branch": st.Current.Render(name)}
	if b != nil {
		if b.Parent != "" && !b.Trunk {
			vals["parent"] = st.Muted.Render(" on " + b.Parent)
		}
		if !b.Trunk && b.Merged == nil && (b.Ahead > 0 || b.Behind > 0) {
			ab := ""
			if b.Ahead > 0 {
				ab += st.Subtle.Render(fmt.Sprintf("%s%d", ic.Ahead, b.Ahead))
			}
			if b.Behind > 0 {
				ab += st.Warning.Render(fmt.Sprintf("%s%d", ic.Behind, b.Behind))
			}
			vals["ab"] = " " + ab
		}
		if pr := b.PR; pr != nil {
			vals["pr"] = " " + prCell(b, st).s
			if c := ciCell(b, st, ic); c.s != "" {
				vals["ci"] = " " + c.s
			}
			if r := reviewCell(b, st); r.s != "" && pr.State == model.PROpen {
				vals["review"] = " " + r.s
			}
		}
		if b.Worktree.Dirty() {
			vals["dirty"] = st.Warning.Render(fmt.Sprintf(" *%d", b.Worktree.Changes()))
		}
	}
	n := 0
	for _, is := range m.Attention {
		if is.Severity == model.SevHigh || is.Severity == model.SevMedium {
			n++
		}
	}
	if n > 0 {
		vals["attention"] = st.Muted.Render(ic.Sep) + st.Warning.Render(fmt.Sprintf("%d need you", n))
	}
	out := format
	for _, key := range []string{"branch", "parent", "ab", "pr", "ci", "review", "dirty", "attention"} {
		out = strings.ReplaceAll(out, "{"+key+"}", vals[key])
	}
	return out
}

// Delta renders changes since an earlier state for agents.
func Delta(since, state string, changes []engine.Change) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# vb delta since=%s state=%s\n", since, state)
	if len(changes) == 0 {
		b.WriteString("no changes\n")
		return b.String()
	}
	for _, c := range changes {
		switch c.Kind {
		case "added":
			fmt.Fprintf(&b, "+ %s: %s\n", c.Branch, c.Detail)
		case "removed":
			fmt.Fprintf(&b, "- %s: %s\n", c.Branch, c.Detail)
		case "changed":
			fmt.Fprintf(&b, "~ %s: %s\n", c.Branch, c.Detail)
		default:
			fmt.Fprintf(&b, "! %s\n", c.Detail)
		}
	}
	return b.String()
}

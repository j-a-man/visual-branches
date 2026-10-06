package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/j-a-man/visual-branches/internal/model"
)

// AgentOptions configure the compact agent format.
type AgentOptions struct {
	Now       time.Time
	Filter    Filter
	MaxTokens int
	Hints     bool
	Legend    bool
}

// EstimateTokens approximates the token count of text. It is deliberately
// conservative (about 3.5 characters per token) so budgets are respected.
func EstimateTokens(s string) int {
	n := len(s)
	return (n*10 + 34) / 35
}

type agentLevel struct {
	hints, details, legend bool
	quiet                  bool // collapse branches without issues
	attention              int  // max attention lines, 0 = all
	branches               bool
}

var agentLevels = []agentLevel{
	{hints: true, details: true, legend: true, branches: true},
	{hints: false, details: true, legend: true, branches: true},
	{hints: false, details: false, legend: true, branches: true},
	{hints: false, details: false, legend: false, quiet: true, branches: true, attention: 8},
	{hints: false, details: false, legend: false, quiet: true, branches: true, attention: 4},
	{attention: 3},
}

// Agent renders the map in a dense, token-efficient text format designed
// for coding agents. With a budget, it degrades gracefully until it fits.
func Agent(m *model.Map, o AgentOptions) string {
	var out string
	for _, lvl := range agentLevels {
		lvl.hints = lvl.hints && o.Hints
		lvl.legend = lvl.legend && o.Legend
		out = agentAt(m, o, lvl)
		if o.MaxTokens <= 0 || EstimateTokens(out) <= o.MaxTokens {
			return out
		}
	}
	return out
}

func agentAt(m *model.Map, o AgentOptions, lvl agentLevel) string {
	var b strings.Builder
	head := m.Repo.Head
	if head == "" && m.Repo.HeadDetached {
		head = "detached@" + shortOID(m.Repo.HeadOID)
	}
	fmt.Fprintf(&b, "# vb repo=%s trunk=%s", m.Repo.Name, strings.Join(m.Repo.Trunks, ","))
	if head != "" {
		fmt.Fprintf(&b, " head=%s", head)
	}
	fmt.Fprintf(&b, " github=%s state=%s\n", m.GitHub.Status, m.State)
	if lvl.legend {
		b.WriteString("# indent=child of branch above; +N/-N=commits ahead/behind parent; *=checked out here; pr#N:state,review,ci\n")
	}

	rows := Rows(m, o.Filter)
	visible := map[string]bool{}
	for _, r := range rows {
		visible[r.B.Name] = true
	}
	interesting := map[string]bool{}
	for _, is := range m.Attention {
		if is.Kind == model.IssueDeletable || is.Kind == model.IssueStale {
			continue
		}
		interesting[is.Branch] = true
		if is.Other != "" {
			interesting[is.Other] = true
		}
	}
	if m.Repo.Head != "" {
		for _, a := range m.Lineage(m.Repo.Head) {
			interesting[a.Name] = true
		}
	}
	// Keep ancestors of interesting branches so indentation stays truthful.
	for n := range interesting {
		for _, a := range m.Lineage(n) {
			interesting[a.Name] = true
		}
	}

	var merged []string
	quiet := 0
	if lvl.branches {
		for _, r := range rows {
			br := r.B
			// Merged leaves collapse into one line; merged branches that still
			// have children stay in the tree.
			if br.Merged != nil && len(visibleChildren(br, visible)) == 0 && !br.Head {
				merged = append(merged, br.Name)
				continue
			}
			if lvl.quiet && !br.Trunk && !interesting[br.Name] {
				quiet++
				continue
			}
			b.WriteString(strings.Repeat("  ", r.Depth))
			b.WriteString(agentLine(br, o.Now, lvl.details))
			b.WriteString("\n")
		}
	}
	if quiet > 0 {
		fmt.Fprintf(&b, "# +%d quiet %s omitted (use --focus <branch> or a larger --max-tokens)\n", quiet, plural(quiet, "branch", "branches"))
	}
	if len(merged) > 0 {
		fmt.Fprintf(&b, "merged: %s", strings.Join(merged, " "))
		if lvl.hints {
			b.WriteString(" -> vb cleanup")
		}
		b.WriteString("\n")
	}

	var items []model.Issue
	for _, is := range m.Attention {
		if !visible[is.Branch] || is.Kind == model.IssueDeletable {
			continue
		}
		items = append(items, is)
	}
	if len(items) > 0 {
		b.WriteString("attention:\n")
		shown := items
		if lvl.attention > 0 && len(shown) > lvl.attention {
			shown = shown[:lvl.attention]
		}
		for _, is := range shown {
			fmt.Fprintf(&b, "- %s: %s", sevWord(is.Severity), is.Message)
			if lvl.hints && is.Hint != "" {
				fmt.Fprintf(&b, " -> %s", is.Hint)
			}
			b.WriteString("\n")
		}
		if len(shown) < len(items) {
			fmt.Fprintf(&b, "- +%d more\n", len(items)-len(shown))
		}
	} else if lvl.branches {
		b.WriteString("attention: none\n")
	}
	for _, w := range m.Warnings {
		fmt.Fprintf(&b, "# note: %s\n", w)
	}
	return b.String()
}

func visibleChildren(br *model.Branch, visible map[string]bool) []string {
	var out []string
	for _, c := range br.Children {
		if visible[c] {
			out = append(out, c)
		}
	}
	return out
}

func sevWord(s string) string {
	switch s {
	case model.SevMedium:
		return "med"
	}
	return s
}

func agentLine(br *model.Branch, now time.Time, details bool) string {
	parts := []string{br.Name}
	if br.Head {
		parts[0] += "*"
	}
	if br.Merged != nil {
		parts = append(parts, "merged="+br.Merged.How)
	}
	if !br.Trunk && br.Merged == nil {
		if br.Ahead > 0 {
			parts = append(parts, fmt.Sprintf("+%d", br.Ahead))
		}
		if br.Behind > 0 {
			parts = append(parts, fmt.Sprintf("-%d", br.Behind))
		}
	}
	if pr := br.PR; pr != nil {
		state := pr.State
		if pr.Draft && pr.State == model.PROpen {
			state = "draft"
		}
		fields := []string{state}
		if pr.State == model.PROpen {
			if pr.Review != "" {
				fields = append(fields, strings.ReplaceAll(pr.Review, "_", "-"))
			}
			ci := pr.CI
			if ci == model.CIFail {
				if names := pr.FailingChecks(); len(names) > 0 {
					ci += "(" + strings.Join(limitList(names, 3), ",") + ")"
				}
			}
			if ci != model.CINone {
				fields = append(fields, ci)
			}
		}
		parts = append(parts, fmt.Sprintf("pr#%d:%s", pr.Number, strings.Join(fields, ",")))
	}
	if br.Agent != "" && !strings.HasPrefix(strings.TrimPrefix(br.Name, "origin/"), br.Agent+"/") {
		parts = append(parts, "agent="+br.Agent)
	}
	if br.ParentConfidence == model.Low {
		parts = append(parts, "parent?")
	}
	if details {
		parts = append(parts, Age(now.Sub(br.CommittedAt)))
		if wt := br.Worktree; wt != nil && !wt.Main {
			parts = append(parts, "wt="+wt.Display)
		}
	}
	for _, f := range br.Flags {
		switch f {
		case model.FlagUnpushed:
			parts = append(parts, fmt.Sprintf("unpushed=%d", br.Upstream.Ahead))
		case model.FlagBehindRemote:
			parts = append(parts, fmt.Sprintf("pull=%d", br.Upstream.Behind))
		case model.FlagDirty:
			parts = append(parts, fmt.Sprintf("dirty=%d", br.Worktree.Changes()))
		case model.FlagDeletable:
		default:
			parts = append(parts, f)
		}
	}
	return strings.Join(parts, " ")
}

func limitList(list []string, n int) []string {
	if len(list) <= n {
		return list
	}
	return append(append([]string(nil), list[:n]...), fmt.Sprintf("+%d", len(list)-n))
}

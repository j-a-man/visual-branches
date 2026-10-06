package render

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/j-a-man/visual-branches/internal/engine"
	"github.com/j-a-man/visual-branches/internal/model"
)

// ShowOptions configure the branch detail view.
type ShowOptions struct {
	Styles Styles
	Icons  Icons
	Now    time.Time
	Width  int
	Hints  bool
}

// Show renders the detail of one branch for humans.
func Show(m *model.Map, d *engine.Detail, o ShowOptions) string {
	st, ic := o.Styles, o.Icons
	b := d.Branch
	var out strings.Builder
	title := st.Header.Render(b.Name)
	var tags []string
	if b.Head {
		tags = append(tags, "current")
	}
	if b.Trunk {
		tags = append(tags, "trunk")
	}
	if b.RemoteOnly {
		tags = append(tags, "remote only")
	}
	if b.Agent != "" {
		tags = append(tags, "agent: "+b.Agent)
	}
	if len(tags) > 0 {
		title += "  " + st.Muted.Render(strings.Join(tags, ic.Sep))
	}
	out.WriteString(title + "\n")

	label := func(k string) string { return "  " + st.Muted.Render(pad(k, 9)) }
	line := func(k, v string) {
		if v != "" {
			out.WriteString(label(k) + v + "\n")
		}
	}

	if b.Parent != "" {
		v := st.Text.Render(b.Parent) + st.Muted.Render("  "+sourceWord(b.ParentSource))
		if b.ParentConfidence != model.High {
			v += st.Warning.Render(" (" + b.ParentConfidence + " confidence)")
		}
		line("parent", v)
	}
	if len(d.Lineage) > 1 {
		line("lineage", st.Muted.Render(strings.Join(d.Lineage, " ← ")))
	}
	line("tip", st.Subtle.Render(shortOID(b.Tip))+"  "+st.Text.Render(b.Subject)+st.Muted.Render(fmt.Sprintf("  %s, %s ago", b.Author, Age(o.Now.Sub(b.CommittedAt)))))

	if !b.Trunk && b.Parent != "" {
		var parts []string
		if b.Merged != nil {
			parts = append(parts, st.Merged.Render("merged ("+b.Merged.How+") into "+b.Merged.Into))
		} else {
			parts = append(parts, aheadBehind(ic, b.Ahead, b.Behind, b.Parent))
			if pb := m.Branch(b.Parent); pb != nil && !pb.Trunk && len(m.Repo.Trunks) > 0 {
				parts = append(parts, aheadBehind(ic, b.TrunkAhead, b.TrunkBehind, m.Repo.Trunks[0]))
			}
		}
		line("commits", strings.Join(parts, st.Muted.Render(ic.Sep)))
	}

	if up := b.Upstream; up != nil {
		v := st.Text.Render(up.Name)
		switch {
		case up.Gone:
			v += st.Muted.Render("  deleted on remote")
		case up.Ahead > 0 && up.Behind > 0:
			v += st.Danger.Render(fmt.Sprintf("  diverged: %d to push, %d to pull", up.Ahead, up.Behind))
		case up.Ahead > 0:
			v += st.Subtle.Render(fmt.Sprintf("  %d to push", up.Ahead))
		case up.Behind > 0:
			v += st.Subtle.Render(fmt.Sprintf("  %d to pull", up.Behind))
		default:
			v += st.Muted.Render("  in sync")
		}
		line("remote", v)
	} else if !b.RemoteOnly && !b.Trunk {
		line("remote", st.Muted.Render("not pushed"))
	}

	if wt := b.Worktree; wt != nil {
		where := wt.Display
		if where == "." {
			where = "here"
		}
		v := st.Text.Render(where)
		var dirty []string
		if wt.Staged > 0 {
			dirty = append(dirty, fmt.Sprintf("%d staged", wt.Staged))
		}
		if wt.Unstaged > 0 {
			dirty = append(dirty, fmt.Sprintf("%d modified", wt.Unstaged))
		}
		if wt.Untracked > 0 {
			dirty = append(dirty, fmt.Sprintf("%d untracked", wt.Untracked))
		}
		if wt.Conflicted > 0 {
			dirty = append(dirty, fmt.Sprintf("%d conflicted", wt.Conflicted))
		}
		if len(dirty) > 0 {
			v += st.Warning.Render("  " + strings.Join(dirty, ", "))
		} else {
			v += st.Muted.Render("  clean")
		}
		line("worktree", v)
	}
	if b.Stashes > 0 {
		line("stashes", st.Subtle.Render(fmt.Sprintf("%d", b.Stashes)))
	}

	if pr := b.PR; pr != nil {
		v := prCell(b, st).s + "  " + st.Text.Render(pr.Title)
		state := pr.State
		if pr.Draft && pr.State == model.PROpen {
			state = "draft"
		}
		meta := []string{state}
		if pr.State == model.PROpen {
			if pr.Review != "" {
				meta = append(meta, strings.ReplaceAll(pr.Review, "_", " "))
			}
			if pr.Mergeable == model.MergeConflicting {
				meta = append(meta, "conflicts with "+pr.Base)
			}
		}
		meta = append(meta, fmt.Sprintf("+%d -%d in %d %s", pr.Additions, pr.Deletions, pr.ChangedFiles, plural(pr.ChangedFiles, "file", "files")))
		line("pr", v)
		out.WriteString(label("") + st.Muted.Render(strings.Join(meta, ic.Sep)) + "\n")
		if len(pr.Checks) > 0 {
			var cs []string
			for _, c := range pr.Checks {
				switch c.Status {
				case model.CIPass:
					cs = append(cs, st.Success.Render(ic.Pass)+" "+st.Subtle.Render(c.Name))
				case model.CIFail:
					cs = append(cs, st.Danger.Render(ic.Fail)+" "+st.Text.Render(c.Name))
				case model.CIPending:
					cs = append(cs, st.Warning.Render(ic.Pending)+" "+st.Subtle.Render(c.Name))
				default:
					cs = append(cs, st.Muted.Render("- "+c.Name))
				}
			}
			out.WriteString(label("checks") + wrapJoin(cs, "   ", o.Width-13, "\n"+label("")) + "\n")
		}
		out.WriteString(label("") + st.Muted.Render(pr.URL) + "\n")
	}

	if fc := b.Forecast; fc != nil {
		if fc.Clean {
			line("merge", st.Success.Render(ic.Pass)+" "+st.Muted.Render("merges cleanly into "+fc.Target))
		} else {
			v := st.Danger.Render(ic.Fail) + " " + st.Text.Render("conflicts with "+fc.Target)
			if len(fc.Files) > 0 {
				v += st.Muted.Render(": " + strings.Join(fc.Files, ", "))
			}
			line("merge", v)
		}
	}
	for _, ov := range d.Overlaps {
		other := ov.A
		if other == b.Name {
			other = ov.B
		}
		style, word := st.Warning, "also edited by"
		if ov.Conflict {
			style, word = st.Danger, "conflicts with"
		}
		line("overlap", style.Render(word+" "+other)+st.Muted.Render(": "+strings.Join(limitList(ov.Files, 4), ", ")))
	}

	if len(d.Commits) > 0 {
		heading := "commits"
		if d.CompareTo != "" {
			heading = fmt.Sprintf("commits not on %s", d.CompareTo)
		}
		out.WriteString("\n" + st.Title.Render(heading) + "\n")
		for _, c := range d.Commits {
			subject := c.Subject
			if o.Width > 0 {
				subject = Truncate(subject, max(20, o.Width-22), ic.Ellipsis)
			}
			out.WriteString("  " + st.Subtle.Render(c.Short) + "  " + st.Text.Render(subject) + st.Muted.Render("  "+Age(o.Now.Sub(c.CommittedAt))) + "\n")
		}
		if d.MoreCount > 0 {
			out.WriteString("  " + st.Muted.Render(fmt.Sprintf("%s %d more", ic.Ellipsis, d.MoreCount)) + "\n")
		}
	}
	if len(d.Files) > 0 {
		out.WriteString("\n" + st.Title.Render(fmt.Sprintf("files (%d)", len(d.Files))) + "  " +
			st.Success.Render(fmt.Sprintf("+%d", d.Additions)) + " " + st.Danger.Render(fmt.Sprintf("-%d", d.Deletions)) + "\n")
		shown := d.Files
		if len(shown) > 20 {
			shown = shown[:20]
		}
		pw := 0
		for _, f := range shown {
			pw = max(pw, lipgloss.Width(f.Path))
		}
		pw = min(pw, 60)
		for _, f := range shown {
			stat := st.Muted.Render("binary")
			if !f.Binary {
				stat = st.Success.Render(fmt.Sprintf("+%d", f.Additions)) + " " + st.Danger.Render(fmt.Sprintf("-%d", f.Deletions))
			}
			out.WriteString("  " + st.Muted.Render(pad(f.Status, 1)) + " " + pad(st.Text.Render(Truncate(f.Path, pw, ic.Ellipsis)), pw) + "  " + stat + "\n")
		}
		if len(d.Files) > len(shown) {
			out.WriteString("  " + st.Muted.Render(fmt.Sprintf("%s %d more", ic.Ellipsis, len(d.Files)-len(shown))) + "\n")
		}
	}
	if len(d.Issues) > 0 {
		out.WriteString("\n" + st.Title.Render("needs you") + "\n")
		for _, is := range d.Issues {
			icon, style := ic.Low, st.Muted
			switch is.Severity {
			case model.SevHigh:
				icon, style = ic.High, st.Danger
			case model.SevMedium:
				icon, style = ic.Medium, st.Warning
			case model.SevReady:
				icon, style = ic.Ready, st.Success
			}
			out.WriteString("  " + style.Render(icon) + " " + st.Text.Render(is.Message) + "\n")
			if o.Hints && is.Hint != "" {
				out.WriteString("    " + st.Muted.Render(is.Hint) + "\n")
			}
		}
	}
	return out.String()
}

func sourceWord(s string) string {
	switch s {
	case model.SourceConfig:
		return "pinned"
	case model.SourcePR:
		return "PR base"
	case model.SourceAncestry:
		return "ancestry"
	case model.SourceReflog:
		return "reflog"
	case model.SourceTrunk:
		return "trunk"
	}
	return s
}

// aheadBehind renders "↑2 ↓1 vs main", omitting zero counts.
func aheadBehind(ic Icons, ahead, behind int, base string) string {
	var parts []string
	if ahead > 0 {
		parts = append(parts, fmt.Sprintf("%s%d", ic.Ahead, ahead))
	}
	if behind > 0 {
		parts = append(parts, fmt.Sprintf("%s%d", ic.Behind, behind))
	}
	if len(parts) == 0 {
		return "even with " + base
	}
	return strings.Join(parts, " ") + " vs " + base
}

// wrapJoin joins items, wrapping onto new lines when width is exceeded.
func wrapJoin(items []string, sep string, width int, nl string) string {
	if width <= 0 {
		return strings.Join(items, sep)
	}
	var out strings.Builder
	lineW := 0
	for i, it := range items {
		w := lipgloss.Width(it)
		if i > 0 {
			if lineW+len(sep)+w > width {
				out.WriteString(nl)
				lineW = 0
			} else {
				out.WriteString(sep)
				lineW += len(sep)
			}
		}
		out.WriteString(it)
		lineW += w
	}
	return out.String()
}

// ShowAgent renders branch detail compactly for coding agents.
func ShowAgent(m *model.Map, d *engine.Detail, now time.Time) string {
	b := d.Branch
	var out strings.Builder
	fmt.Fprintf(&out, "# vb show %s\n", agentLine(b, now, true))
	if b.Parent != "" {
		fmt.Fprintf(&out, "parent: %s (%s, %s)", b.Parent, b.ParentSource, b.ParentConfidence)
		if len(d.Lineage) > 1 {
			fmt.Fprintf(&out, " lineage: %s", strings.Join(d.Lineage, " < "))
		}
		out.WriteString("\n")
	}
	if up := b.Upstream; up != nil {
		switch {
		case up.Gone:
			fmt.Fprintf(&out, "upstream: %s gone\n", up.Name)
		default:
			fmt.Fprintf(&out, "upstream: %s +%d -%d\n", up.Name, up.Ahead, up.Behind)
		}
	}
	if wt := b.Worktree; wt != nil {
		fmt.Fprintf(&out, "worktree: %s staged=%d modified=%d untracked=%d conflicted=%d\n", wt.Path, wt.Staged, wt.Unstaged, wt.Untracked, wt.Conflicted)
	}
	if pr := b.PR; pr != nil {
		fmt.Fprintf(&out, "pr: #%d %q %s base=%s +%d -%d %s\n", pr.Number, pr.Title, pr.State, pr.Base, pr.Additions, pr.Deletions, pr.URL)
		if len(pr.Checks) > 0 {
			var cs []string
			for _, c := range pr.Checks {
				cs = append(cs, c.Name+"="+c.Status)
			}
			fmt.Fprintf(&out, "checks: %s\n", strings.Join(cs, " "))
		}
	}
	if fc := b.Forecast; fc != nil {
		if fc.Clean {
			fmt.Fprintf(&out, "merge: clean into %s\n", fc.Target)
		} else {
			fmt.Fprintf(&out, "merge: CONFLICT with %s in %s\n", fc.Target, strings.Join(fc.Files, ", "))
		}
	}
	for _, ov := range d.Overlaps {
		other := ov.A
		if other == b.Name {
			other = ov.B
		}
		word := "overlap"
		if ov.Conflict {
			word = "collision"
		}
		fmt.Fprintf(&out, "%s: %s on %s\n", word, other, strings.Join(ov.Files, ", "))
	}
	if len(d.Commits) > 0 {
		out.WriteString("commits:\n")
		for _, c := range d.Commits {
			fmt.Fprintf(&out, "  %s %s (%s)\n", c.Short, c.Subject, Age(now.Sub(c.CommittedAt)))
		}
		if d.MoreCount > 0 {
			fmt.Fprintf(&out, "  +%d more\n", d.MoreCount)
		}
	}
	if len(d.Files) > 0 {
		fmt.Fprintf(&out, "files (%d, +%d -%d):\n", len(d.Files), d.Additions, d.Deletions)
		shown := d.Files
		if len(shown) > 40 {
			shown = shown[:40]
		}
		for _, f := range shown {
			fmt.Fprintf(&out, "  %s %s +%d -%d\n", f.Status, f.Path, f.Additions, f.Deletions)
		}
		if len(d.Files) > len(shown) {
			fmt.Fprintf(&out, "  +%d more\n", len(d.Files)-len(shown))
		}
	}
	if len(d.Issues) > 0 {
		out.WriteString("attention:\n")
		for _, is := range d.Issues {
			fmt.Fprintf(&out, "- %s: %s", sevWord(is.Severity), is.Message)
			if is.Hint != "" {
				fmt.Fprintf(&out, " -> %s", is.Hint)
			}
			out.WriteString("\n")
		}
	}
	return out.String()
}

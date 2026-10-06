package render

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/j-a-man/visual-branches/internal/config"
	"github.com/j-a-man/visual-branches/internal/model"
)

// TreeOptions configure the terminal tree.
type TreeOptions struct {
	Config *config.Config
	Styles Styles
	Icons  Icons
	// Width is the terminal width; 0 disables fitting.
	Width int
	Now   time.Time
	// Plain marks the current branch with a * because color is off.
	Plain  bool
	Filter Filter
	// CurrentRoot is the work tree vb runs in; its path is not repeated.
	CurrentRoot string
}

type cell struct {
	s string // styled
	w int    // display width
}

func mk(s string) cell { return cell{s: s, w: lipgloss.Width(s)} }

// Tree renders the branch map as an aligned, colored tree.
func Tree(m *model.Map, o TreeOptions) string {
	var out strings.Builder
	rows := Rows(m, o.Filter)
	d := o.Config.Display
	if d.Header {
		out.WriteString(Header(m, o, rows))
		out.WriteString("\n\n")
	}
	for _, line := range TableLines(m, rows, o) {
		out.WriteString(line)
		out.WriteString("\n")
	}
	if d.Footer {
		if f := Footer(m, rows, o); f != "" {
			out.WriteString("\n")
			out.WriteString(f)
		}
	}
	for _, w := range m.Warnings {
		out.WriteString(o.Styles.Muted.Render("  note: "+w) + "\n")
	}
	return out.String()
}

// Header renders the one-line summary shown above the tree.
func Header(m *model.Map, o TreeOptions, rows []Row) string {
	st, ic := o.Styles, o.Icons
	parts := []string{}
	if len(m.Repo.Trunks) > 0 {
		parts = append(parts, strings.Join(m.Repo.Trunks, ", "))
	}
	n := 0
	prs := 0
	for _, r := range rows {
		if !r.B.Trunk {
			n++
		}
		if r.B.PR.Open() {
			prs++
		}
	}
	parts = append(parts, fmt.Sprintf("%d %s", n, plural(n, "branch", "branches")))
	if m.GitHub.Available() {
		parts = append(parts, fmt.Sprintf("%d open %s", prs, plural(prs, "PR", "PRs")))
	}
	line := st.Header.Render(m.Repo.Name) + "  " + st.Muted.Render(strings.Join(parts, ic.Sep))
	if gh := githubNote(m, o.Now); gh != "" {
		style := st.Muted
		if m.GitHub.Status == model.GitHubNoAuth || m.GitHub.Status == model.GitHubError {
			style = st.Warning
		}
		line += st.Muted.Render(ic.Sep) + style.Render(gh)
	}
	if m.Repo.HeadDetached && m.Repo.Source != "remote" {
		line += st.Muted.Render(ic.Sep) + st.Warning.Render("HEAD detached at "+shortOID(m.Repo.HeadOID))
	}
	return line
}

func githubNote(m *model.Map, now time.Time) string {
	g := m.GitHub
	ago := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		if d := now.Sub(*t); d >= time.Minute {
			return " " + Age(d) + " ago"
		}
		return " just now"
	}
	switch g.Status {
	case model.GitHubOK:
		if g.FetchedAt == nil {
			return "" // nothing to look up (no branches besides trunks)
		}
		return "github" + ago(g.FetchedAt)
	case model.GitHubCached:
		return "github cached" + ago(g.FetchedAt)
	case model.GitHubOffline:
		return "offline"
	case model.GitHubNoAuth:
		return "github: gh auth login to show PRs"
	case model.GitHubError:
		return "github: " + g.Message
	}
	return ""
}

func shortOID(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}

// columns in display order.
var columnOrder = []string{"pr", "review", "ci", "ahead_behind", "age", "worktree", "flags"}

// dropOrder lists columns removed first when the terminal is narrow.
var dropOrder = []string{"worktree", "age", "review", "flags", "ahead_behind"}

// TableLines renders one aligned line per row, fitted to o.Width.
func TableLines(m *model.Map, rows []Row, o TreeOptions) []string {
	st, ic := o.Styles, o.Icons
	cfg := o.Config
	enabled := map[string]bool{}
	for _, c := range cfg.Display.Columns {
		enabled[c] = true
	}
	nameCap := cfg.Display.MaxNameWidth
	if nameCap <= 0 {
		nameCap = 44
	}

	var cells []map[string]cell
	var aheadW, behindW int
	for _, r := range rows {
		b := r.B
		c := map[string]cell{}
		c["pr"] = prCell(b, st)
		c["review"] = reviewCell(b, st)
		c["ci"] = ciCell(b, st, ic)
		if !b.Trunk && b.Parent != "" {
			if b.Ahead > 0 {
				aheadW = max(aheadW, lipgloss.Width(fmt.Sprintf("%s%d", ic.Ahead, b.Ahead)))
			}
			if b.Behind > 0 {
				behindW = max(behindW, lipgloss.Width(fmt.Sprintf("%s%d", ic.Behind, b.Behind)))
			}
		}
		c["age"] = mk(st.Muted.Render(Age(o.Now.Sub(b.CommittedAt))))
		if wt := b.Worktree; wt != nil && !samePath(wt.Path, o.CurrentRoot) {
			c["worktree"] = mk(st.Muted.Render(ShortPath(wt.Display, 26, ic.Ellipsis)))
		}
		c["flags"] = flagsCell(b, st)
		cells = append(cells, c)
	}
	for i, r := range rows {
		cells[i]["ahead_behind"] = abCell(r.B, st, ic, aheadW, behindW)
	}

	// Choose columns that fit.
	active := []string{}
	for _, col := range columnOrder {
		if enabled[col] {
			active = append(active, col)
		}
	}
	nameW := func(capw int) int {
		w := 0
		for _, r := range rows {
			w = max(w, lipgloss.Width(r.Prefix(ic))+min(lipgloss.Width(r.B.Name), capw)+agentTagWidth(r.B, o))
		}
		return w
	}
	widths := func(cols []string) (map[string]int, int) {
		ws := map[string]int{}
		total := nameW(nameCap)
		for _, col := range cols {
			wmax := 0
			for _, c := range cells {
				wmax = max(wmax, c[col].w)
			}
			ws[col] = wmax
			if wmax > 0 {
				total += 2 + wmax
			}
		}
		return ws, total
	}
	ws, total := widths(active)
	if o.Width > 0 {
		for _, drop := range dropOrder {
			if total <= o.Width {
				break
			}
			active = remove(active, drop)
			ws, total = widths(active)
		}
		for total > o.Width && nameCap > 16 {
			nameCap -= 4
			ws, total = widths(active)
		}
	}
	nw := nameW(nameCap)

	lines := make([]string, 0, len(rows))
	for i, r := range rows {
		line := pad(nameCell(r, o, nameCap), nw)
		for _, col := range active {
			if ws[col] == 0 {
				continue
			}
			c := cells[i][col]
			if col == "age" {
				line += "  " + padLeft(c.s, ws[col])
			} else {
				line += "  " + pad(c.s, ws[col])
			}
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	return lines
}

func remove(list []string, x string) []string {
	out := list[:0:0]
	for _, v := range list {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}

func samePath(a, b string) bool {
	return b != "" && strings.EqualFold(strings.TrimRight(strings.ReplaceAll(a, "\\", "/"), "/"), strings.TrimRight(strings.ReplaceAll(b, "\\", "/"), "/"))
}

func agentTag(b *model.Branch) string {
	if b.Agent == "" {
		return ""
	}
	name := b.Name
	if i := strings.Index(name, "/"); i >= 0 && b.RemoteOnly {
		name = name[i+1:]
	}
	if strings.HasPrefix(name, b.Agent+"/") {
		return ""
	}
	return b.Agent
}

func agentTagWidth(b *model.Branch, o TreeOptions) int {
	w := 0
	if t := agentTag(b); t != "" {
		w += 1 + lipgloss.Width(t)
	}
	if o.Plain && b.Head {
		w += 2
	}
	return w
}

func nameCell(r Row, o TreeOptions, capw int) string {
	st, ic := o.Styles, o.Icons
	b := r.B
	name := Truncate(b.Name, capw, ic.Ellipsis)
	var s string
	switch {
	case b.Head:
		s = st.Current.Render(name)
	case b.Trunk:
		s = st.TrunkName.Render(name)
	case b.Merged != nil:
		s = st.Muted.Render(name)
	case b.RemoteOnly:
		if i := strings.Index(name, "/"); i > 0 {
			s = st.Muted.Render(name[:i+1]) + st.Text.Render(name[i+1:])
		} else {
			s = st.Text.Render(name)
		}
	default:
		s = st.Text.Render(name)
	}
	if o.Plain && b.Head {
		s += " " + ic.Current
	}
	if t := agentTag(b); t != "" {
		s += " " + st.Agent.Render(t)
	}
	if p := r.Prefix(ic); p != "" {
		return st.Tree.Render(p) + s
	}
	return s
}

func prCell(b *model.Branch, st Styles) cell {
	pr := b.PR
	if pr == nil {
		return cell{}
	}
	num := fmt.Sprintf("#%d", pr.Number)
	switch {
	case pr.State == model.PRMerged:
		return mk(st.Merged.Render(num))
	case pr.State == model.PRClosed:
		return mk(st.Muted.Render(num))
	case pr.Draft:
		return mk(st.Muted.Render(num))
	}
	return mk(st.Info.Render(num))
}

func reviewCell(b *model.Branch, st Styles) cell {
	if b.Merged != nil {
		return mk(st.Merged.Render("merged"))
	}
	pr := b.PR
	if pr == nil {
		return cell{}
	}
	switch pr.State {
	case model.PRMerged:
		return mk(st.Merged.Render("merged"))
	case model.PRClosed:
		return mk(st.Muted.Render("closed"))
	}
	if pr.Draft {
		return mk(st.Muted.Render("draft"))
	}
	switch pr.Review {
	case model.ReviewApproved:
		return mk(st.Success.Render("approved"))
	case model.ReviewChanges:
		return mk(st.Danger.Render("changes"))
	case model.ReviewRequired:
		return mk(st.Subtle.Render("review"))
	}
	return mk(st.Subtle.Render("open"))
}

func ciCell(b *model.Branch, st Styles, ic Icons) cell {
	pr := b.PR
	if pr == nil || pr.State != model.PROpen {
		return cell{}
	}
	switch pr.CI {
	case model.CIPass:
		return mk(st.Success.Render(ic.Pass))
	case model.CIFail:
		return mk(st.Danger.Render(ic.Fail))
	case model.CIPending:
		return mk(st.Warning.Render(ic.Pending))
	}
	return cell{}
}

func abCell(b *model.Branch, st Styles, ic Icons, aw, bw int) cell {
	if b.Trunk || b.Parent == "" || b.Merged != nil {
		return cell{}
	}
	a, be := "", ""
	if b.Ahead > 0 {
		a = st.Subtle.Render(fmt.Sprintf("%s%d", ic.Ahead, b.Ahead))
	}
	if b.Behind > 0 {
		style := st.Muted
		if b.HasFlag(model.FlagRestack) || b.HasFlag(model.FlagOldBase) {
			style = st.Warning
		}
		be = style.Render(fmt.Sprintf("%s%d", ic.Behind, b.Behind))
	}
	if a == "" && be == "" {
		return cell{}
	}
	s := pad(a, aw)
	if bw > 0 {
		if aw > 0 {
			s += " "
		}
		s += pad(be, bw)
	}
	return mk(strings.TrimRight(s, " "))
}

func flagStyle(f string, st Styles) lipgloss.Style {
	switch f {
	case model.FlagConflict, model.FlagCollision, model.FlagDiverged:
		return st.Danger
	case model.FlagRestack, model.FlagParentMerged, model.FlagDirty, model.FlagOverlap, model.FlagOldBase:
		return st.Warning
	case model.FlagUnpushed, model.FlagBehindRemote:
		return st.Subtle
	case model.FlagDeletable:
		return st.Merged
	}
	return st.Muted
}

func flagsCell(b *model.Branch, st Styles) cell {
	if len(b.Flags) == 0 {
		return cell{}
	}
	parts := make([]string, 0, len(b.Flags))
	for _, f := range b.Flags {
		parts = append(parts, flagStyle(f, st).Render(f))
	}
	return mk(strings.Join(parts, " "))
}

type footItem struct {
	icon, msg, hint string
	style           lipgloss.Style
}

// Footer renders the "needs you" list for the visible rows.
func Footer(m *model.Map, rows []Row, o TreeOptions) string {
	st, ic := o.Styles, o.Icons
	visible := map[string]bool{}
	for _, r := range rows {
		visible[r.B.Name] = true
	}
	var items []footItem
	deletable, stale := 0, 0
	for _, is := range m.Attention {
		if !visible[is.Branch] {
			continue
		}
		switch is.Kind {
		case model.IssueDeletable:
			deletable++
			continue
		case model.IssueStale:
			stale++
			continue
		}
		it := footItem{msg: is.Message, hint: is.Hint}
		switch is.Severity {
		case model.SevHigh:
			it.icon, it.style = ic.High, st.Danger
		case model.SevMedium:
			it.icon, it.style = ic.Medium, st.Warning
		case model.SevReady:
			it.icon, it.style = ic.Ready, st.Success
		default:
			it.icon, it.style = ic.Low, st.Muted
		}
		items = append(items, it)
	}
	limit := o.Config.Display.AttentionLimit
	more := 0
	if limit > 0 && len(items) > limit {
		more = len(items) - limit
		items = items[:limit]
	}
	if stale > 0 {
		items = append(items, footItem{icon: ic.Low, style: st.Muted,
			msg: fmt.Sprintf("%d %s had no commits in %s", stale, plural(stale, "branch has", "branches have"), config.FormatDuration(o.Config.Analysis.StaleAfter.Duration))})
	}
	if deletable > 0 {
		items = append(items, footItem{icon: ic.Low, style: st.Muted, hint: "vb cleanup",
			msg: fmt.Sprintf("%d merged %s can be deleted", deletable, plural(deletable, "branch", "branches"))})
	}
	var out strings.Builder
	if len(items) == 0 {
		out.WriteString("  " + st.Success.Render(ic.Ready) + " " + st.Muted.Render("nothing needs you") + "\n")
		return out.String()
	}
	out.WriteString(st.Title.Render("needs you") + "\n")
	showHints := o.Config.Display.Hints
	msgW := 0
	for _, it := range items {
		msgW = max(msgW, lipgloss.Width(it.msg))
	}
	msgW = min(msgW, 64)
	// Hints sit in a column to the right of the messages. If any would not
	// fit, every hint goes on its own line so the list stays even.
	inline := true
	for _, it := range items {
		if it.hint == "" {
			continue
		}
		if lipgloss.Width(it.msg) > msgW || (o.Width > 0 && 4+msgW+3+lipgloss.Width(it.hint) > o.Width) {
			inline = false
		}
	}
	for _, it := range items {
		line := "  " + it.style.Render(it.icon) + " " + st.Text.Render(it.msg)
		if showHints && it.hint != "" {
			if inline {
				line = pad(line, 4+msgW) + "   " + st.Muted.Render(it.hint)
			} else {
				line += "\n    " + st.Muted.Render(it.hint)
			}
		}
		out.WriteString(line + "\n")
	}
	if more > 0 {
		out.WriteString("  " + st.Muted.Render(fmt.Sprintf("%s %d more, see vb tui or vb --agent", ic.Ellipsis, more)) + "\n")
	}
	return out.String()
}

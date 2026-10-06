// Package tui is vb's interactive branch map.
//
// The left pane is the branch tree; the right pane shows the selected
// branch in detail. Actions are explicit single keys, and the only
// destructive one (deleting a merged branch) asks for confirmation.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/j-a-man/visual-branches/internal/engine"
	"github.com/j-a-man/visual-branches/internal/model"
	"github.com/j-a-man/visual-branches/internal/render"
)

// Options configure the interactive view.
type Options struct {
	Session *engine.Session
	// Open opens a URL in the user's browser.
	Open func(url string) error
}

// Run starts the interactive view and blocks until the user quits.
func Run(ctx context.Context, o Options) error {
	m := newModel(ctx, o)
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	return err
}

// Snapshot renders one frame of the interactive view with the given branch
// selected, for documentation and screenshots.
func Snapshot(ctx context.Context, o Options, width, height int, selected string) (string, error) {
	m := newModel(ctx, o)
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	msg := m.build()()
	if b, ok := msg.(builtMsg); ok && b.err != nil {
		return "", b.err
	}
	m.Update(msg)
	for i, r := range m.rows {
		if r.B.Name == selected {
			m.sel = i
		}
	}
	m.clamp()
	if cmd := m.loadDetail(); cmd != nil {
		m.Update(cmd())
	}
	return m.render(), nil
}

type builtMsg struct {
	m   *model.Map
	err error
}

type detailMsg struct {
	key string
	d   *engine.Detail
	err error
}

type tickMsg time.Time

type actionMsg struct {
	text    string
	err     error
	rebuild bool
}

type confirmation struct {
	prompt string
	run    tea.Cmd
}

type tuiModel struct {
	ctx  context.Context
	opts Options
	s    *engine.Session
	st   render.Styles
	ic   render.Icons

	m      *model.Map
	rows   []render.Row
	sel    int
	offset int
	width  int
	height int

	showMerged bool
	filter     string
	filtering  bool
	zoom       bool
	help       bool

	details      map[string]*engine.Detail
	detailScroll int

	status    string
	statusErr bool
	confirm   *confirmation
	loading   bool
	lastBuild time.Time
}

func newModel(ctx context.Context, o Options) *tuiModel {
	cfg := o.Session.Config
	return &tuiModel{
		ctx:        ctx,
		opts:       o,
		s:          o.Session,
		st:         render.NewStyles(cfg.Theme()),
		ic:         render.IconSet(cfg.Display.Icons),
		showMerged: cfg.Display.ShowMerged,
		details:    map[string]*engine.Detail{},
		loading:    true,
	}
}

func (t *tuiModel) Init() tea.Cmd {
	return tea.Batch(t.build(), t.tick())
}

func (t *tuiModel) tick() tea.Cmd {
	every := t.s.Config.TUI.Refresh.Duration
	if every <= 0 {
		return nil
	}
	return tea.Tick(every, func(now time.Time) tea.Msg { return tickMsg(now) })
}

func (t *tuiModel) build() tea.Cmd {
	s := t.s
	ctx := t.ctx
	return func() tea.Msg {
		m, err := s.Build(ctx)
		return builtMsg{m: m, err: err}
	}
}

func (t *tuiModel) selected() *model.Branch {
	if t.sel < 0 || t.sel >= len(t.rows) {
		return nil
	}
	return t.rows[t.sel].B
}

func detailKey(m *model.Map, name string) string { return m.State + "\x00" + name }

func (t *tuiModel) loadDetail() tea.Cmd {
	b := t.selected()
	if b == nil || t.m == nil {
		return nil
	}
	key := detailKey(t.m, b.Name)
	if _, ok := t.details[key]; ok {
		return nil
	}
	s, m, ctx, name := t.s, t.m, t.ctx, b.Name
	return func() tea.Msg {
		d, err := s.Detail(ctx, m, name)
		return detailMsg{key: key, d: d, err: err}
	}
}

func (t *tuiModel) refreshRows() {
	var prev string
	if b := t.selected(); b != nil {
		prev = b.Name
	}
	flt := render.Filter{ShowMerged: t.showMerged, Hide: t.s.Config.Display.Hide}
	if t.filter != "" {
		q := strings.ToLower(t.filter)
		flt.Match = func(b *model.Branch) bool { return strings.Contains(strings.ToLower(b.Name), q) }
	}
	t.rows = render.Rows(t.m, flt)
	t.sel = 0
	for i, r := range t.rows {
		if (prev != "" && r.B.Name == prev) || (prev == "" && r.B.Head) {
			t.sel = i
			break
		}
	}
	t.clamp()
}

func (t *tuiModel) bodyHeight() int {
	return max(1, t.height-5)
}

func (t *tuiModel) clamp() {
	if t.sel >= len(t.rows) {
		t.sel = len(t.rows) - 1
	}
	if t.sel < 0 {
		t.sel = 0
	}
	h := t.bodyHeight()
	if t.sel < t.offset {
		t.offset = t.sel
	}
	if t.sel >= t.offset+h {
		t.offset = t.sel - h + 1
	}
	if t.offset < 0 {
		t.offset = 0
	}
}

func (t *tuiModel) setStatus(text string, isErr bool) {
	t.status, t.statusErr = text, isErr
}

func (t *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		t.width, t.height = msg.Width, msg.Height
		t.clamp()
		return t, nil
	case builtMsg:
		t.loading = false
		if msg.err != nil {
			t.setStatus("refresh failed: "+msg.err.Error(), true)
			return t, nil
		}
		t.m = msg.m
		t.lastBuild = time.Now()
		t.details = map[string]*engine.Detail{}
		t.refreshRows()
		return t, t.loadDetail()
	case detailMsg:
		if msg.err == nil {
			t.details[msg.key] = msg.d
		}
		return t, nil
	case tickMsg:
		return t, tea.Batch(t.build(), t.tick())
	case actionMsg:
		if msg.err != nil {
			t.setStatus(msg.err.Error(), true)
		} else {
			t.setStatus(msg.text, false)
		}
		if msg.rebuild {
			t.loading = true
			return t, t.build()
		}
		return t, nil
	case tea.KeyPressMsg:
		return t.key(msg)
	}
	return t, nil
}

func (t *tuiModel) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		return t, tea.Quit
	}
	if t.confirm != nil {
		c := t.confirm
		t.confirm = nil
		if k == "y" || k == "Y" {
			return t, c.run
		}
		t.setStatus("cancelled", false)
		return t, nil
	}
	if t.filtering {
		switch k {
		case "enter":
			t.filtering = false
		case "esc":
			t.filtering = false
			t.filter = ""
			t.refreshRows()
		case "backspace":
			if r := []rune(t.filter); len(r) > 0 {
				t.filter = string(r[:len(r)-1])
				t.refreshRows()
			}
		default:
			if txt := msg.Key().Text; txt != "" {
				t.filter += txt
				t.refreshRows()
			}
		}
		return t, t.loadDetail()
	}
	if t.help {
		t.help = false
		return t, nil
	}
	move := func(d int) (tea.Model, tea.Cmd) {
		t.sel += d
		t.clamp()
		t.detailScroll = 0
		return t, t.loadDetail()
	}
	switch k {
	case "q", "esc":
		if t.zoom {
			t.zoom = false
			return t, nil
		}
		if k == "esc" && t.filter != "" {
			t.filter = ""
			t.refreshRows()
			return t, t.loadDetail()
		}
		return t, tea.Quit
	case "up", "k":
		return move(-1)
	case "down", "j":
		return move(1)
	case "pgup":
		return move(-t.bodyHeight())
	case "pgdown":
		return move(t.bodyHeight())
	case "home", "g":
		return move(-len(t.rows))
	case "end", "G":
		return move(len(t.rows))
	case "ctrl+d", "J":
		t.detailScroll += max(1, t.bodyHeight()/2)
		return t, nil
	case "ctrl+u", "K":
		t.detailScroll = max(0, t.detailScroll-max(1, t.bodyHeight()/2))
		return t, nil
	case "enter", "space":
		t.zoom = !t.zoom
		t.detailScroll = 0
		return t, t.loadDetail()
	case "/":
		t.filtering = true
		return t, nil
	case "m":
		t.showMerged = !t.showMerged
		t.refreshRows()
		t.setStatus(map[bool]string{true: "showing merged branches", false: "hiding merged branches"}[t.showMerged], false)
		return t, t.loadDetail()
	case "a":
		if t.s.Opts.Source == engine.SourceAll {
			t.s.Opts.Source = engine.SourceLocal
			t.setStatus("local branches only", false)
		} else {
			t.s.Opts.Source = engine.SourceAll
			t.setStatus("including remote-only branches", false)
		}
		t.loading = true
		return t, t.build()
	case "r":
		t.loading = true
		t.setStatus("refreshing", false)
		return t, t.build()
	case "?":
		t.help = true
		return t, nil
	case "y":
		if b := t.selected(); b != nil {
			t.setStatus("copied "+b.Name, false)
			return t, tea.SetClipboard(b.Name)
		}
	case "o":
		return t, t.openSelected()
	case "s":
		return t, t.switchSelected()
	case "d":
		return t, t.deleteSelected()
	}
	return t, nil
}

func (t *tuiModel) openSelected() tea.Cmd {
	b := t.selected()
	if b == nil || t.opts.Open == nil {
		return nil
	}
	url := ""
	if b.PR != nil {
		url = b.PR.URL
	} else if web := t.m.Repo.Remote.WebURL(); web != "" && (b.Upstream != nil || b.RemoteOnly || b.Trunk) {
		url = web + "/tree/" + strings.TrimPrefix(b.Name, t.s.Config.Remote+"/")
	}
	if url == "" {
		t.setStatus(b.Name+" has no pull request or remote branch to open", true)
		return nil
	}
	open := t.opts.Open
	return func() tea.Msg {
		if err := open(url); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "opened " + url}
	}
}

func (t *tuiModel) switchSelected() tea.Cmd {
	b := t.selected()
	if b == nil {
		return nil
	}
	if b.Head {
		t.setStatus(b.Name+" is already checked out here", false)
		return nil
	}
	if wt := b.Worktree; wt != nil {
		t.setStatus(b.Name+" is checked out in "+wt.Display, true)
		return nil
	}
	name := strings.TrimPrefix(b.Name, t.s.Config.Remote+"/")
	s, ctx := t.s, t.ctx
	return func() tea.Msg {
		if _, err := s.Repo.Run(ctx, "switch", name); err != nil {
			return actionMsg{err: fmt.Errorf("switch failed: %s", firstLine(err.Error()))}
		}
		return actionMsg{text: "switched to " + name, rebuild: true}
	}
}

func (t *tuiModel) deleteSelected() tea.Cmd {
	b := t.selected()
	if b == nil {
		return nil
	}
	if !b.HasFlag(model.FlagDeletable) || b.RemoteOnly {
		t.setStatus("only merged local branches that are not checked out can be deleted here", true)
		return nil
	}
	name, tip := b.Name, b.Tip
	s, ctx := t.s, t.ctx
	run := func() tea.Msg {
		// vb verified the merge (git branch -d only checks HEAD), so -D is safe.
		if _, err := s.Repo.Run(ctx, "branch", "-D", name); err != nil {
			return actionMsg{err: fmt.Errorf("delete failed: %s", firstLine(err.Error()))}
		}
		return actionMsg{text: fmt.Sprintf("deleted %s (was %s; restore with git branch %s %s)", name, tip[:8], name, tip[:8]), rebuild: true}
	}
	if !t.s.Config.TUI.ConfirmDelete {
		return run
	}
	t.confirm = &confirmation{prompt: fmt.Sprintf("delete %s (%s)? y/n", name, b.Merged.How), run: run}
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// View renders the whole screen.
func (t *tuiModel) View() tea.View {
	v := tea.NewView(t.render())
	v.AltScreen = true
	v.WindowTitle = "vb"
	return v
}

func (t *tuiModel) render() string {
	if t.width == 0 {
		return ""
	}
	st, ic := t.st, t.ic
	if t.m == nil {
		return "\n  " + st.Muted.Render("mapping branches"+ic.Ellipsis)
	}
	if t.help {
		return t.helpView()
	}
	topts := render.TreeOptions{Config: t.s.Config, Styles: st, Icons: ic, Now: time.Now(), CurrentRoot: t.s.Repo.Root}
	var out strings.Builder
	head := render.Header(t.m, topts, t.rows)
	if t.loading {
		head += st.Muted.Render(ic.Sep + "refreshing" + ic.Ellipsis)
	}
	out.WriteString(" " + ansi.Truncate(head, t.width-2, ic.Ellipsis) + "\n\n")

	h := t.bodyHeight()
	wide := t.width >= 100 && !t.zoom
	var left, right []string
	switch {
	case t.zoom:
		right = t.detailLines(t.width - 2)
	case wide:
		leftW := min(max(t.width*55/100, 40), t.width-40)
		left = t.treeLines(leftW - 2)
		right = t.detailLines(t.width - leftW - 3)
		body := t.joinPanes(left, right, leftW, h)
		out.WriteString(body)
	default:
		left = t.treeLines(t.width - 2)
	}
	if !wide {
		lines := left
		if t.zoom {
			lines = right
		}
		for i := 0; i < h; i++ {
			if i < len(lines) {
				out.WriteString(" " + ansi.Truncate(lines[i], t.width-2, ic.Ellipsis))
			}
			out.WriteString("\n")
		}
	}
	out.WriteString("\n")
	out.WriteString(" " + ansi.Truncate(t.statusLine(), t.width-2, ic.Ellipsis) + "\n")
	out.WriteString(" " + ansi.Truncate(t.keysLine(), t.width-2, ic.Ellipsis))
	return out.String()
}

func (t *tuiModel) treeLines(width int) []string {
	topts := render.TreeOptions{Config: t.s.Config, Styles: t.st, Icons: t.ic, Now: time.Now(), CurrentRoot: t.s.Repo.Root, Width: width - 2}
	lines := render.TableLines(t.m, t.rows, topts)
	h := t.bodyHeight()
	var out []string
	for i := t.offset; i < len(lines) && i < t.offset+h; i++ {
		gutter := "  "
		if i == t.sel {
			gutter = t.st.Accent.Render("▌") + " "
			if t.ic.Tee == render.ASCIIIcons.Tee {
				gutter = t.st.Accent.Render(">") + " "
			}
		}
		out = append(out, gutter+lines[i])
	}
	if len(t.rows) == 0 {
		out = append(out, "  "+t.st.Muted.Render("no branches match "+fmt.Sprintf("%q", t.filter)))
	}
	return out
}

func (t *tuiModel) detailLines(width int) []string {
	b := t.selected()
	if b == nil {
		return nil
	}
	d, ok := t.details[detailKey(t.m, b.Name)]
	if !ok {
		return []string{t.st.Muted.Render("loading " + b.Name + t.ic.Ellipsis)}
	}
	text := render.Show(t.m, d, render.ShowOptions{Styles: t.st, Icons: t.ic, Now: time.Now(), Width: width, Hints: t.s.Config.Display.Hints})
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if t.detailScroll > 0 {
		t.detailScroll = min(t.detailScroll, max(0, len(lines)-1))
		lines = lines[t.detailScroll:]
	}
	return lines
}

func (t *tuiModel) joinPanes(left, right []string, leftW, h int) string {
	sep := t.st.Tree.Render("│")
	rightW := t.width - leftW - 3
	var out strings.Builder
	for i := 0; i < h; i++ {
		l, r := "", ""
		if i < len(left) {
			l = ansi.Truncate(left[i], leftW, t.ic.Ellipsis)
		}
		if i < len(right) {
			r = ansi.Truncate(right[i], rightW, t.ic.Ellipsis)
		}
		out.WriteString(" " + l + strings.Repeat(" ", max(0, leftW-lipgloss.Width(l))) + sep + " " + r + "\n")
	}
	return out.String()
}

func (t *tuiModel) statusLine() string {
	st := t.st
	switch {
	case t.confirm != nil:
		return st.Warning.Render(t.confirm.prompt)
	case t.filtering:
		return st.Accent.Render("/") + st.Text.Render(t.filter) + st.Accent.Render("▏")
	case t.status != "":
		if t.statusErr {
			return st.Danger.Render(t.status)
		}
		return st.Subtle.Render(t.status)
	}
	if b := t.selected(); b != nil {
		// Show the most urgent issue of the selected branch.
		if issues := t.m.IssuesFor(b.Name); len(issues) > 0 {
			is := issues[0]
			style := st.Muted
			switch is.Severity {
			case model.SevHigh:
				style = st.Danger
			case model.SevMedium:
				style = st.Warning
			case model.SevReady:
				style = st.Success
			}
			line := style.Render(is.Message)
			if is.Hint != "" {
				line += st.Muted.Render("   " + is.Hint)
			}
			return line
		}
	}
	if t.filter != "" {
		return st.Muted.Render("filter: " + t.filter + "  (esc to clear)")
	}
	return st.Muted.Render(fmt.Sprintf("%d branches%supdated %s ago", len(t.rows), t.ic.Sep, render.Age(time.Since(t.lastBuild))))
}

func (t *tuiModel) keysLine() string {
	keys := [][2]string{
		{"↑↓", "move"}, {"enter", "detail"}, {"s", "switch"}, {"o", "open"}, {"y", "copy"},
		{"d", "delete"}, {"/", "filter"}, {"m", "merged"}, {"a", "remote"}, {"r", "refresh"}, {"?", "help"}, {"q", "quit"},
	}
	if t.ic.Tee == render.ASCIIIcons.Tee {
		keys[0][0] = "j/k"
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, t.st.Subtle.Render(k[0])+" "+t.st.Muted.Render(k[1]))
	}
	return strings.Join(parts, "  ")
}

func (t *tuiModel) helpView() string {
	st := t.st
	rows := [][2]string{
		{"↑ ↓  j k", "move between branches"},
		{"pgup pgdn  g G", "page, top, bottom"},
		{"enter / space", "full-screen detail (esc to return)"},
		{"J K  ctrl+d ctrl+u", "scroll the detail pane"},
		{"s", "switch to the branch (git switch)"},
		{"o", "open the pull request, or the branch on GitHub"},
		{"y", "copy the branch name"},
		{"d", "delete a merged branch (asks first)"},
		{"/", "filter by name; esc clears"},
		{"m", "show or hide merged branches"},
		{"a", "include remote-only branches"},
		{"r", "refresh now (also every " + render.Age(t.s.Config.TUI.Refresh.Duration) + ")"},
		{"q  esc  ctrl+c", "quit"},
	}
	var out strings.Builder
	out.WriteString("\n " + st.Header.Render("vb") + "  " + st.Muted.Render("keys") + "\n\n")
	for _, r := range rows {
		out.WriteString("   " + st.Subtle.Render(fmt.Sprintf("%-20s", r[0])) + st.Text.Render(r[1]) + "\n")
	}
	out.WriteString("\n   " + st.Muted.Render("press any key to close"))
	return out.String()
}

// Package cli implements the vb command line.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/j-a-man/visual-branches/internal/config"
	"github.com/j-a-man/visual-branches/internal/engine"
	"github.com/j-a-man/visual-branches/internal/git"
	"github.com/j-a-man/visual-branches/internal/model"
	"github.com/j-a-man/visual-branches/internal/render"
	"github.com/j-a-man/visual-branches/internal/theme"
)

// version is set at build time with -ldflags "-X .../internal/cli.version=v1.2.3".
var version = ""

// Version returns the build version.
func Version() string {
	if version != "" {
		return version
	}
	// go install module@vX.Y.Z records the release version; local builds get a
	// VCS pseudo-version (v0.0.0-<date>-<commit>), which reads better as "dev".
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" && !strings.HasPrefix(info.Main.Version, "v0.0.0-") {
		return info.Main.Version
	}
	return "dev"
}

// ExitError carries a process exit code without printing anything more.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit %d", e.Code) }

// globals are flags shared by every command.
type globals struct {
	dir      string
	config   string
	theme    string
	ascii    bool
	noColor  bool
	offline  bool
	noGitHub bool
	all      bool
	source   string
}

type rootFlags struct {
	json        bool
	agent       bool
	pretty      bool
	focus       string
	maxTokens   int
	since       string
	mine        bool
	hideMerged  bool
	interactive bool
	exitCode    bool
}

type app struct {
	g   globals
	out io.Writer
	err io.Writer
}

// Execute runs the vb command line and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	a := &app{out: os.Stdout, err: os.Stderr}
	root := a.rootCmd()
	if err := root.ExecuteContext(ctx); err != nil {
		var ee ExitError
		if errors.As(err, &ee) {
			return ee.Code
		}
		fmt.Fprintln(os.Stderr, "vb: "+err.Error())
		return 1
	}
	return 0
}

func (a *app) rootCmd() *cobra.Command {
	var f rootFlags
	cmd := &cobra.Command{
		Use:   "vb",
		Short: "A map of your git branches, for humans and coding agents",
		Long: `vb shows every branch in a repository as a tree: how branches relate,
what state each one is in (PRs, reviews, CI, worktrees, merges), and what
needs your attention. It never changes anything unless you ask.

Run it with no arguments inside any git repository.`,
		Example: `  vb                         # the branch map
  vb --agent --max-tokens 400  # compact output for coding agents
  vb show feat/login           # everything about one branch
  vb tui                       # interactive view
  vb web                       # local web view
  vb export mermaid --markdown # diagram for a PR description`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		Version:       Version(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runMap(cmd.Context(), f)
		},
	}
	cmd.SetVersionTemplate("vb {{.Version}}\n")
	pf := cmd.PersistentFlags()
	pf.StringVarP(&a.g.dir, "dir", "C", "", "run as if vb was started in `path`")
	pf.StringVar(&a.g.config, "config", "", "load an extra config `file` after the repository config")
	pf.StringVar(&a.g.theme, "theme", "", "color theme (see vb themes)")
	pf.BoolVar(&a.g.ascii, "ascii", false, "use ASCII glyphs only")
	pf.BoolVar(&a.g.noColor, "no-color", false, "disable colors")
	pf.BoolVar(&a.g.offline, "offline", false, "never contact GitHub; use cached pull request data")
	pf.BoolVar(&a.g.noGitHub, "no-github", false, "do not show pull request data")
	pf.BoolVarP(&a.g.all, "all", "a", false, "include remote-only branches")
	pf.StringVar(&a.g.source, "source", "", "branch source: local, all, or remote (remote is for CI checkouts)")

	fl := cmd.Flags()
	fl.BoolVar(&f.json, "json", false, "print the map as JSON")
	fl.BoolVar(&f.agent, "agent", false, "print a compact, token-efficient map for coding agents")
	fl.BoolVar(&f.pretty, "pretty", false, "indent JSON output")
	fl.StringVarP(&f.focus, "focus", "f", "", "show only this branch's lineage and descendants")
	fl.IntVar(&f.maxTokens, "max-tokens", 0, "token budget for --agent output (0 = config default)")
	fl.StringVar(&f.since, "since", "", "print only what changed since a previous state token")
	fl.BoolVar(&f.mine, "mine", false, "only branches whose latest commit you authored")
	fl.BoolVar(&f.hideMerged, "hide-merged", false, "hide merged branches")
	fl.BoolVarP(&f.interactive, "interactive", "i", false, "open the interactive view (same as vb tui)")
	fl.BoolVar(&f.exitCode, "exit-code", false, "exit with status 2 when high-severity issues exist")

	cmd.AddCommand(
		a.showCmd(),
		a.statusCmd(),
		a.contextCmd(),
		a.exportCmd(),
		a.cleanupCmd(),
		a.parentCmd(),
		a.configCmd(),
		a.themesCmd(),
		a.doctorCmd(),
		a.mcpCmd(),
		a.tuiCmd(),
		a.webCmd(),
		a.versionCmd(),
	)
	return cmd
}

// session opens the repository and applies flag overrides to the config.
func (a *app) session(ctx context.Context, light bool) (*engine.Session, error) {
	if a.g.config != "" {
		os.Setenv("VB_CONFIG", a.g.config)
	}
	opts := engine.Options{
		Dir:     a.g.dir,
		Offline: a.g.offline || os.Getenv("VB_OFFLINE") == "1",
		Light:   light,
		Tool:    "vb " + Version(),
		Source:  a.g.source,
	}
	if a.g.noGitHub {
		opts.GitHub = "never"
	}
	if a.g.all && opts.Source == "" {
		opts.Source = engine.SourceAll
	}
	s, err := engine.Open(ctx, opts)
	if err != nil {
		if errors.Is(err, git.ErrNotRepository) {
			return nil, fmt.Errorf("not inside a git repository (use -C <path> to point at one)")
		}
		return nil, err
	}
	if a.g.theme != "" {
		if _, err := theme.Resolve(a.g.theme, s.Config.Themes); err != nil {
			return nil, err
		}
		s.Config.Display.Theme = a.g.theme
	}
	if a.g.ascii {
		s.Config.Display.Icons = "ascii"
	}
	return s, nil
}

// terminal describes how output will be displayed.
type terminal struct {
	w     io.Writer
	plain bool
	width int
	tty   bool
}

func (a *app) terminal() terminal {
	t := terminal{w: a.out}
	f, ok := a.out.(*os.File)
	if ok {
		t.tty = term.IsTerminal(int(f.Fd()))
		if t.tty {
			if w, _, err := term.GetSize(int(f.Fd())); err == nil {
				t.width = w
			}
			enableVT(f)
		}
	}
	pw := colorprofile.NewWriter(a.out, os.Environ())
	if a.g.noColor {
		pw.Profile = colorprofile.NoTTY
	}
	t.plain = pw.Profile < colorprofile.ANSI
	t.w = pw
	return t
}

func (a *app) styles(cfg *config.Config) (render.Styles, render.Icons) {
	return render.NewStyles(cfg.Theme()), render.IconSet(cfg.Display.Icons)
}

func (a *app) filter(s *engine.Session, f rootFlags) render.Filter {
	flt := render.Filter{
		Focus:      f.focus,
		ShowMerged: s.Config.Display.ShowMerged && !f.hideMerged,
		Hide:       s.Config.Display.Hide,
	}
	if f.mine || s.Config.Display.OnlyMine {
		flt.Mine = s.Repo.UserEmail(context.Background())
	}
	return flt
}

func (a *app) runMap(ctx context.Context, f rootFlags) error {
	if f.interactive {
		return a.runTUI(ctx)
	}
	trace("start")
	s, err := a.session(ctx, false)
	if err != nil {
		return err
	}
	trace("session")
	m, err := s.Build(ctx)
	if err != nil {
		return err
	}
	trace("build")
	defer trace("done")
	if f.focus != "" && m.Branch(f.focus) == nil {
		return fmt.Errorf("branch %q not found", f.focus)
	}
	flt := a.filter(s, f)
	t := a.terminal()
	trace("terminal")
	now := time.Now()

	switch {
	case f.since != "":
		old, ok := engine.LoadSnapshot(s.Store, f.since)
		engine.SaveSnapshot(s.Store, m)
		if !ok {
			// Unknown token: fall back to the full map so the caller is never
			// left without state.
			fmt.Fprintf(t.w, "# vb: unknown state %q, showing the full map\n", f.since)
			fmt.Fprint(t.w, render.Agent(m, a.agentOptions(s, f, flt, now)))
			return nil
		}
		changes := engine.Diff(old, engine.TakeSnapshot(m))
		if f.json {
			return writeJSON(t.w, map[string]any{"since": f.since, "state": m.State, "changes": changes}, f.pretty || t.tty)
		}
		fmt.Fprint(t.w, render.Delta(f.since, m.State, changes))
	case f.json:
		engine.SaveSnapshot(s.Store, m)
		if err := writeJSON(t.w, filterMap(m, flt), f.pretty || t.tty); err != nil {
			return err
		}
	case f.agent:
		engine.SaveSnapshot(s.Store, m)
		fmt.Fprint(t.w, render.Agent(m, a.agentOptions(s, f, flt, now)))
	default:
		st, ic := a.styles(s.Config)
		fmt.Fprint(t.w, render.Tree(m, render.TreeOptions{
			Config: s.Config, Styles: st, Icons: ic, Width: t.width, Now: now,
			Plain: t.plain, Filter: flt, CurrentRoot: s.Repo.Root,
		}))
	}
	if f.exitCode && hasHigh(m) {
		return ExitError{Code: 2}
	}
	return nil
}

func (a *app) agentOptions(s *engine.Session, f rootFlags, flt render.Filter, now time.Time) render.AgentOptions {
	budget := f.maxTokens
	if budget == 0 {
		budget = s.Config.AgentOutput.MaxTokens
	}
	return render.AgentOptions{
		Now: now, Filter: flt, MaxTokens: budget,
		Hints: s.Config.AgentOutput.Hints, Legend: s.Config.AgentOutput.Legend,
	}
}

func hasHigh(m *model.Map) bool {
	for _, is := range m.Attention {
		if is.Severity == model.SevHigh {
			return true
		}
	}
	return false
}

// filterMap returns a shallow copy of m restricted to visible branches.
func filterMap(m *model.Map, flt render.Filter) *model.Map {
	rows := render.Rows(m, flt)
	if len(rows) == len(m.Branches) {
		return m
	}
	visible := map[string]bool{}
	cp := *m
	cp.Branches = nil
	for _, r := range rows {
		visible[r.B.Name] = true
		cp.Branches = append(cp.Branches, r.B)
	}
	cp.Attention = nil
	for _, is := range m.Attention {
		if visible[is.Branch] {
			cp.Attention = append(cp.Attention, is)
		}
	}
	cp.Overlaps = nil
	for _, ov := range m.Overlaps {
		if visible[ov.A] || visible[ov.B] {
			cp.Overlaps = append(cp.Overlaps, ov)
		}
	}
	cp.Index()
	return &cp
}

func writeJSON(w io.Writer, v any, pretty bool) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the vb version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(a.out, "vb "+Version())
		},
	}
}

// confirm asks a yes/no question on the terminal.
func confirm(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprint(out, question+" [y/N] ")
	var answer string
	if _, err := fmt.Fscanln(in, &answer); err != nil {
		return false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

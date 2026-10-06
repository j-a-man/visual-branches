package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/j-a-man/visual-branches/internal/engine"
	"github.com/j-a-man/visual-branches/internal/model"
	"github.com/j-a-man/visual-branches/internal/render"
	"github.com/j-a-man/visual-branches/internal/theme"
)

func (a *app) showCmd() *cobra.Command {
	var asJSON, agent, pretty bool
	cmd := &cobra.Command{
		Use:   "show [branch]",
		Short: "Show everything about one branch (default: the current branch)",
		Long: `Show a branch's parent and how it was inferred, commits and files not on
its parent, pull request checks, merge forecast, overlapping branches, and
suggested next steps.`,
		Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return a.completeBranches(cmd.Context()), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.session(ctx, false)
			if err != nil {
				return err
			}
			m, err := s.Build(ctx)
			if err != nil {
				return err
			}
			name := m.Repo.Head
			if len(args) == 1 {
				name = args[0]
			}
			if name == "" {
				return fmt.Errorf("HEAD is detached; name a branch: vb show <branch>")
			}
			d, err := s.Detail(ctx, m, name)
			if err != nil {
				return err
			}
			t := a.terminal()
			switch {
			case asJSON:
				return writeJSON(t.w, d, pretty || t.tty)
			case agent:
				fmt.Fprint(t.w, render.ShowAgent(m, d, time.Now()))
			default:
				st, ic := a.styles(s.Config)
				fmt.Fprint(t.w, render.Show(m, d, render.ShowOptions{Styles: st, Icons: ic, Now: time.Now(), Width: t.width, Hints: s.Config.Display.Hints}))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	cmd.Flags().BoolVar(&agent, "agent", false, "print a compact version for coding agents")
	cmd.Flags().BoolVar(&pretty, "pretty", false, "indent JSON output")
	return cmd
}

func (a *app) completeBranches(ctx context.Context) []string {
	s, err := a.session(ctx, true)
	if err != nil {
		return nil
	}
	refs, err := s.Repo.Refs(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range refs {
		if strings.HasPrefix(r.Name, "refs/heads/") {
			out = append(out, r.Short())
		}
	}
	return out
}

func (a *app) statusCmd() *cobra.Command {
	var format string
	var asJSON, claudeCode bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "One-line summary of the current branch, for prompts and status lines",
		Long: `Print the current branch with its position, PR, CI, and how many issues
need you. Designed to be fast: it skips conflict and overlap analysis and,
by default, uses cached GitHub data only (status.github in config).

Template placeholders: {branch} {parent} {ab} {pr} {ci} {review} {dirty} {attention}

With --claude-code, reads Claude Code's status line JSON from stdin and
uses its workspace directory.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if claudeCode {
				var in struct {
					Workspace struct {
						CurrentDir string `json:"current_dir"`
					} `json:"workspace"`
					CWD string `json:"cwd"`
				}
				if data, err := io.ReadAll(os.Stdin); err == nil && json.Unmarshal(data, &in) == nil {
					if in.Workspace.CurrentDir != "" {
						a.g.dir = in.Workspace.CurrentDir
					} else if in.CWD != "" {
						a.g.dir = in.CWD
					}
				}
			}
			s, err := a.session(ctx, true)
			if err != nil {
				if claudeCode {
					return nil // outside a repository the status line stays empty
				}
				return err
			}
			switch s.Config.Status.GitHub {
			case "cache":
				s.Opts.Offline = true
			case "never":
				s.Opts.GitHub = "never"
			}
			m, err := s.Build(ctx)
			if err != nil {
				return err
			}
			t := a.terminal()
			if asJSON {
				b := m.HeadBranch()
				return writeJSON(t.w, map[string]any{"head": m.Repo.Head, "branch": b, "attention": m.IssuesFor(m.Repo.Head), "state": m.State}, t.tty)
			}
			if format == "" {
				format = s.Config.Status.Format
			}
			st, ic := a.styles(s.Config)
			fmt.Fprintln(t.w, render.StatusLine(m, format, st, ic))
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "", "template (default from status.format)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the current branch as JSON")
	cmd.Flags().BoolVar(&claudeCode, "claude-code", false, "read Claude Code status line JSON from stdin")
	return cmd
}

func (a *app) contextCmd() *cobra.Command {
	var maxTokens int
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Branch context for agent hooks; never fails",
		Long: `Print the compact agent map for use in agent hooks (for example Claude
Code's SessionStart hook). Unlike vb --agent it prints nothing and exits 0
when run outside a repository or when anything goes wrong, so it can never
break an agent session.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
			defer cancel()
			s, err := a.session(ctx, false)
			if err != nil {
				return nil
			}
			m, err := s.Build(ctx)
			if err != nil {
				return nil
			}
			engine.SaveSnapshot(s.Store, m)
			if maxTokens == 0 {
				maxTokens = s.Config.AgentOutput.MaxTokens
			}
			out := render.Agent(m, render.AgentOptions{
				Now: time.Now(), Filter: render.Filter{ShowMerged: true, Hide: s.Config.Display.Hide},
				MaxTokens: maxTokens, Hints: s.Config.AgentOutput.Hints, Legend: s.Config.AgentOutput.Legend,
			})
			fmt.Fprint(a.out, "Branch map from vb (run `vb --agent` to refresh, `vb show <branch> --agent` for detail):\n"+out)
			return nil
		},
	}
	cmd.Flags().IntVar(&maxTokens, "max-tokens", 0, "token budget (0 = agent_output.max_tokens)")
	return cmd
}

func (a *app) exportCmd() *cobra.Command {
	var outPath, focus, title string
	var markdown, hideMerged bool
	cmd := &cobra.Command{
		Use:       "export <mermaid|dot|svg|json>",
		Short:     "Export the branch map as a diagram or data",
		ValidArgs: []string{"mermaid", "dot", "svg", "json"},
		Args:      cobra.ExactArgs(1),
		Example: `  vb export svg -o branches.svg
  vb export mermaid --markdown --focus feat/login   # paste into a PR
  vb export dot | dot -Tpng -o branches.png`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.session(ctx, false)
			if err != nil {
				return err
			}
			m, err := s.Build(ctx)
			if err != nil {
				return err
			}
			if focus != "" && m.Branch(focus) == nil {
				return fmt.Errorf("branch %q not found", focus)
			}
			th := s.Config.Theme()
			if a.g.theme == "" && s.Config.Display.Theme == "ansi" {
				th, _ = theme.Builtin(theme.Default)
			}
			o := render.ExportOptions{
				Theme: th, Now: time.Now(), Title: title,
				Filter: render.Filter{Focus: focus, ShowMerged: s.Config.Display.ShowMerged && !hideMerged, Hide: s.Config.Display.Hide},
			}
			var out string
			switch args[0] {
			case "mermaid":
				out = render.Mermaid(m, o)
				if markdown {
					out = "```mermaid\n" + out + "```\n"
				}
			case "dot":
				out = render.DOT(m, o)
			case "svg":
				out = render.SVG(m, o)
			case "json":
				data, err := json.MarshalIndent(filterMap(m, o.Filter), "", "  ")
				if err != nil {
					return err
				}
				out = string(data) + "\n"
			default:
				return fmt.Errorf("unknown format %q (mermaid, dot, svg, json)", args[0])
			}
			if outPath == "" || outPath == "-" {
				_, err := io.WriteString(a.out, out)
				return err
			}
			if err := os.WriteFile(outPath, []byte(out), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(a.err, "wrote %s\n", outPath)
			return nil
		},
	}
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "write to `file` instead of stdout")
	cmd.Flags().StringVarP(&focus, "focus", "f", "", "only this branch's lineage and descendants")
	cmd.Flags().StringVar(&title, "title", "", "title for SVG output (default: repository name)")
	cmd.Flags().BoolVar(&markdown, "markdown", false, "wrap Mermaid output in a ```mermaid fence")
	cmd.Flags().BoolVar(&hideMerged, "hide-merged", false, "hide merged branches")
	return cmd
}

func (a *app) cleanupCmd() *cobra.Command {
	var apply, yes bool
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "List merged branches that can be deleted; --apply deletes them",
		Long: `List local branches that are merged into a trunk (by ancestry, merged pull
request, squash, or rebase) and not checked out in any worktree.

Nothing is deleted unless you pass --apply and confirm. Branches are
deleted with git branch -D because vb verifies the merge itself (git's own
check only looks at the current branch, and cannot see squash merges). Each
deleted tip is printed so a branch can be restored with git branch <name> <tip>.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.session(ctx, false)
			if err != nil {
				return err
			}
			m, err := s.Build(ctx)
			if err != nil {
				return err
			}
			t := a.terminal()
			st, _ := a.styles(s.Config)
			var victims []*model.Branch
			for _, b := range m.Branches {
				if b.HasFlag(model.FlagDeletable) && !b.RemoteOnly {
					victims = append(victims, b)
				}
			}
			if len(victims) == 0 {
				fmt.Fprintln(t.w, st.Muted.Render("No merged branches to clean up."))
				return nil
			}
			w := 0
			for _, b := range victims {
				w = max(w, len(b.Name))
			}
			for _, b := range victims {
				how := b.Merged.How
				fmt.Fprintf(t.w, "  %s  %s\n", st.Text.Render(fmt.Sprintf("%-*s", w, b.Name)),
					st.Muted.Render(fmt.Sprintf("%s into %s, %s", how, b.Merged.Into, render.Age(time.Since(b.CommittedAt))+" ago")))
			}
			if !apply {
				fmt.Fprintln(t.w, st.Muted.Render(fmt.Sprintf("\n%d %s can be deleted. Run vb cleanup --apply to delete.", len(victims), plural(len(victims), "branch", "branches"))))
				return nil
			}
			if !yes && !confirm(os.Stdin, a.out, fmt.Sprintf("\nDelete %d %s?", len(victims), plural(len(victims), "branch", "branches"))) {
				fmt.Fprintln(t.w, "Nothing deleted.")
				return nil
			}
			failed := 0
			for _, b := range victims {
				// vb verified the merge into a trunk; git branch -d would only
				// check HEAD, so use -D. The old tip is printed for recovery.
				if _, err := s.Repo.Run(ctx, "branch", "-D", b.Name); err != nil {
					failed++
					fmt.Fprintf(a.err, "could not delete %s: %v\n", b.Name, err)
					continue
				}
				fmt.Fprintf(t.w, "deleted %s %s\n", b.Name, st.Muted.Render("(was "+b.Tip[:8]+")"))
			}
			if failed > 0 {
				return ExitError{Code: 1}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "delete the listed branches (asks first)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation with --apply")
	return cmd
}

func (a *app) parentCmd() *cobra.Command {
	var unset bool
	cmd := &cobra.Command{
		Use:   "parent <branch> [parent]",
		Short: "Show, pin, or unpin a branch's parent",
		Long: `Without a parent argument, show the inferred parent and the evidence.
With one, pin it (stored as branch.<branch>.vbparent in the repository's
git config). Use --unset to go back to inference.`,
		Args: cobra.RangeArgs(1, 2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return a.completeBranches(cmd.Context()), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.session(ctx, true)
			if err != nil {
				return err
			}
			branch := args[0]
			key := "branch." + branch + ".vbparent"
			switch {
			case unset:
				if err := s.Repo.ConfigUnset(ctx, key); err != nil {
					return err
				}
				fmt.Fprintf(a.out, "%s: parent is inferred again\n", branch)
				return nil
			case len(args) == 2:
				if _, code, _ := s.Repo.RunCode(ctx, "rev-parse", "-q", "--verify", "refs/heads/"+branch); code != 0 {
					return fmt.Errorf("no local branch %q", branch)
				}
				if err := s.Repo.ConfigSet(ctx, key, args[1]); err != nil {
					return err
				}
				fmt.Fprintf(a.out, "%s: parent pinned to %s\n", branch, args[1])
				return nil
			}
			m, err := s.Build(ctx)
			if err != nil {
				return err
			}
			b := m.Branch(branch)
			if b == nil {
				return fmt.Errorf("branch %q not found", branch)
			}
			if b.Trunk {
				fmt.Fprintf(a.out, "%s is a trunk\n", branch)
				return nil
			}
			fmt.Fprintf(a.out, "%s -> %s (source: %s, confidence: %s)\n", branch, b.Parent, b.ParentSource, b.ParentConfidence)
			return nil
		},
	}
	cmd.Flags().BoolVar(&unset, "unset", false, "remove a pinned parent")
	return cmd
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// openBrowser opens a URL with the platform's default handler.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch {
	case isWindows():
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case isDarwin():
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

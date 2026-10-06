package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/cli/go-gh/v2/pkg/auth"
	"github.com/spf13/cobra"

	"github.com/j-a-man/visual-branches/internal/config"
	"github.com/j-a-man/visual-branches/internal/git"
	"github.com/j-a-man/visual-branches/internal/github"
	"github.com/j-a-man/visual-branches/internal/render"
	"github.com/j-a-man/visual-branches/internal/theme"
)

func isWindows() bool { return runtime.GOOS == "windows" }
func isDarwin() bool  { return runtime.GOOS == "darwin" }

func (a *app) configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Create, locate, and inspect configuration",
		Long: `vb reads configuration in layers, later wins:

  user      ` + config.UserPath() + `
  team      <repo>/.vb.toml       commit it to share settings with your team
  personal  <repo>/.git/vb.toml   never committed
  env       VB_CONFIG, VB_THEME, VB_ICONS, VB_OFFLINE, VB_GITHUB, NO_COLOR
  flags     --theme, --ascii, --offline, ...`,
	}
	var scope string
	var force bool
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Write a commented config file with every setting",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := a.configPath(cmd.Context(), scope)
			if err != nil {
				return err
			}
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(config.Template), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "wrote %s\n", path)
			return nil
		},
	}
	initCmd.Flags().StringVar(&scope, "scope", "user", "which file: user, repo (team, committed), or local (personal)")
	initCmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")

	pathCmd := &cobra.Command{
		Use:   "path",
		Short: "Print config file locations and which exist",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, sc := range []string{"user", "repo", "local"} {
				p, err := a.configPath(cmd.Context(), sc)
				if err != nil {
					fmt.Fprintf(a.out, "%-6s %s\n", sc, "(not in a repository)")
					continue
				}
				state := "missing"
				if _, err := os.Stat(p); err == nil {
					state = "found"
				}
				fmt.Fprintf(a.out, "%-6s %s  (%s)\n", sc, p, state)
			}
			if p := os.Getenv("VB_CONFIG"); p != "" {
				fmt.Fprintf(a.out, "%-6s %s\n", "env", p)
			}
			return nil
		},
	}

	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration after all layers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.loadConfig(cmd.Context())
			if err != nil {
				return err
			}
			out, err := cfg.Encode()
			if err != nil {
				return err
			}
			if len(cfg.Sources) > 0 {
				fmt.Fprintf(a.out, "# loaded: %s\n", strings.Join(cfg.Sources, ", "))
			} else {
				fmt.Fprintln(a.out, "# loaded: built-in defaults only")
			}
			for _, w := range cfg.Warnings {
				fmt.Fprintf(a.out, "# warning: %s\n", w)
			}
			fmt.Fprint(a.out, out)
			return nil
		},
	}
	cmd.AddCommand(initCmd, pathCmd, showCmd)
	return cmd
}

func (a *app) configPath(ctx context.Context, scope string) (string, error) {
	switch scope {
	case "user":
		return config.UserPath(), nil
	case "repo", "team":
		repo, err := git.Open(ctx, a.g.dir)
		if err != nil {
			return "", err
		}
		return config.RepoPath(repo.Root), nil
	case "local", "personal":
		repo, err := git.Open(ctx, a.g.dir)
		if err != nil {
			return "", err
		}
		return config.LocalPath(repo.CommonDir), nil
	}
	return "", fmt.Errorf("unknown scope %q (user, repo, local)", scope)
}

func (a *app) loadConfig(ctx context.Context) (*config.Config, error) {
	if a.g.config != "" {
		os.Setenv("VB_CONFIG", a.g.config)
	}
	repo, err := git.Open(ctx, a.g.dir)
	if err != nil {
		return config.Load("", "")
	}
	return config.Load(repo.Root, repo.CommonDir)
}

func (a *app) themesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "themes",
		Short: "List color themes with a preview",
		Long: `List built-in and custom themes. Set one with display.theme in config,
VB_THEME, or --theme. The "ansi" theme follows your terminal's own palette;
"mono" uses no color.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.loadConfig(cmd.Context())
			if err != nil {
				cfg = config.Defaults()
			}
			t := a.terminal()
			names := theme.Names()
			for n := range cfg.Themes {
				names = append(names, n)
			}
			sort.Strings(names)
			ic := render.IconSet(cfg.Display.Icons)
			for _, n := range names {
				th, err := theme.Resolve(n, cfg.Themes)
				if err != nil {
					continue
				}
				st := render.NewStyles(th)
				marker := "  "
				if n == cfg.Display.Theme {
					marker = st.Accent.Render("* ")
				}
				sample := st.Text.Render("feat/login") + " " + st.Info.Render("#142") + " " +
					st.Success.Render("approved") + " " + st.Success.Render(ic.Pass) + " " +
					st.Subtle.Render(ic.Ahead+"4") + " " + st.Warning.Render(ic.Behind+"1") + " " +
					st.Danger.Render("conflict") + " " + st.Merged.Render("merged") + " " + st.Muted.Render("3d")
				fmt.Fprintf(t.w, "%s%-18s %s\n", marker, n, sample)
			}
			return nil
		},
	}
}

func (a *app) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check git, GitHub access, configuration, and integrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			t := a.terminal()
			cfg := config.Defaults()
			st := render.NewStyles(cfg.Theme())
			ic := render.IconSet("unicode")
			if a.g.ascii {
				ic = render.IconSet("ascii")
			}
			problems := 0
			report := func(ok bool, warn bool, label, detail string) {
				icon := st.Success.Render(ic.Pass)
				switch {
				case !ok && warn:
					icon = st.Warning.Render("!")
				case !ok:
					icon = st.Danger.Render(ic.Fail)
					problems++
				}
				fmt.Fprintf(t.w, "  %s %-14s %s\n", icon, label, st.Muted.Render(detail))
			}
			fmt.Fprintln(t.w, st.Header.Render("vb doctor")+"  "+st.Muted.Render("vb "+Version()+", "+runtime.GOOS+"/"+runtime.GOARCH))

			repo, rerr := git.Open(ctx, a.g.dir)
			if rerr != nil && !errors.Is(rerr, git.ErrNotRepository) {
				report(false, false, "git", rerr.Error())
			}
			if repo != nil {
				v := repo.Version
				switch {
				case v.AtLeast(2, 41):
					report(true, false, "git", v.String())
				case v.AtLeast(2, 38):
					report(false, true, "git", v.String()+": works; 2.41+ is faster (batched ahead/behind)")
				default:
					report(false, true, "git", v.String()+": conflict forecasts need 2.38+")
				}
				report(true, false, "repository", repo.Root)
			} else {
				out, err := exec.Command("git", "version").Output()
				if err != nil {
					report(false, false, "git", "not found in PATH")
				} else {
					report(true, false, "git", strings.TrimSpace(string(out)))
				}
				report(false, true, "repository", "not inside a git repository")
			}

			loaded, err := a.loadConfig(ctx)
			if err != nil {
				report(false, false, "config", err.Error())
			} else {
				detail := "defaults only"
				if len(loaded.Sources) > 0 {
					detail = strings.Join(loaded.Sources, ", ")
				}
				report(len(loaded.Warnings) == 0, true, "config", detail)
				for _, w := range loaded.Warnings {
					fmt.Fprintf(t.w, "    %s\n", st.Warning.Render(w))
				}
				cfg = loaded
			}

			if repo != nil {
				url := repo.RemoteURL(ctx, cfg.Remote)
				host, owner, name, ok := github.ParseRemote(url)
				switch {
				case url == "":
					report(false, true, "remote", "no remote named "+cfg.Remote)
				case !ok:
					report(false, true, "remote", url+" (not a recognized hosting URL)")
				default:
					report(true, false, "remote", fmt.Sprintf("%s/%s/%s", host, owner, name))
					if tok, src := auth.TokenForHost(host); tok != "" {
						report(true, false, "github auth", "token from "+src)
					} else {
						report(false, true, "github auth", "none; run `gh auth login` or set GH_TOKEN to show PRs")
					}
				}
			}

			if _, err := exec.LookPath("gh"); err == nil {
				report(true, false, "gh cli", "installed")
			} else {
				report(false, true, "gh cli", "not installed (optional; used for hints like gh pr checks)")
			}
			if _, err := exec.LookPath("vb"); err == nil {
				report(true, false, "PATH", "vb is on PATH (hooks and MCP clients can run it)")
			} else {
				report(false, true, "PATH", "vb is not on PATH; agent hooks and MCP configs need it")
			}
			if cp := cacheDirState(); cp != "" {
				report(true, false, "cache", cp)
			}
			if problems > 0 {
				return ExitError{Code: 1}
			}
			return nil
		},
	}
}

func cacheDirState() string {
	dir := os.Getenv("VB_CACHE_DIR")
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(base, "vb")
	}
	var size int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return fmt.Sprintf("%s (%d KB)", dir, size/1024)
}

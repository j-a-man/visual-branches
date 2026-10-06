// Package config loads vb's layered configuration.
//
// Layers, later wins: built-in defaults, the user config file, the team
// config committed in the repository (.vb.toml), the personal repository
// config (.git/vb.toml), environment variables, and command-line flags.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/j-a-man/visual-branches/internal/theme"
)

// Config is the complete vb configuration.
type Config struct {
	// Trunks lists long-lived base branches; the first is primary. Empty means
	// auto-detect from origin/HEAD, then main, master, trunk, develop.
	Trunks []string `toml:"trunks"`
	// Remote is the remote used for upstream and GitHub detection.
	Remote string `toml:"remote"`

	Display     Display                      `toml:"display"`
	Analysis    Analysis                     `toml:"analysis"`
	Overlap     Overlap                      `toml:"overlap"`
	GitHub      GitHub                       `toml:"github"`
	Agents      map[string]AgentRule         `toml:"agents"`
	Parents     map[string]string            `toml:"parents"`
	AgentOutput AgentOutput                  `toml:"agent_output"`
	Status      Status                       `toml:"status"`
	TUI         TUI                          `toml:"tui"`
	Web         Web                          `toml:"web"`
	Themes      map[string]map[string]string `toml:"themes"`

	// Sources lists the files that were loaded, in order.
	Sources []string `toml:"-"`
	// Warnings collects unknown keys and similar non-fatal problems.
	Warnings []string `toml:"-"`
}

// Display controls the terminal tree.
type Display struct {
	Theme          string   `toml:"theme"`
	Icons          string   `toml:"icons"`
	Columns        []string `toml:"columns"`
	ShowMerged     bool     `toml:"show_merged"`
	ShowRemote     bool     `toml:"show_remote"`
	Sort           string   `toml:"sort"`
	Header         bool     `toml:"header"`
	Footer         bool     `toml:"footer"`
	Hints          bool     `toml:"hints"`
	AttentionLimit int      `toml:"attention_limit"`
	MaxNameWidth   int      `toml:"max_name_width"`
	Hide           []string `toml:"hide"`
	OnlyMine       bool     `toml:"only_mine"`
}

// Analysis toggles and tunes the analyses.
type Analysis struct {
	StaleAfter        Duration `toml:"stale_after"`
	Conflicts         bool     `toml:"conflicts"`
	Overlap           bool     `toml:"overlap"`
	SquashDetection   bool     `toml:"squash_detection"`
	WorktreeStatus    bool     `toml:"worktree_status"`
	MaxBranches       int      `toml:"max_branches"`
	MaxCommits        int      `toml:"max_commits"`
	SquashLookback    int      `toml:"squash_lookback"`
	OldBaseThreshold  int      `toml:"old_base_threshold"`
	ReadStackMetadata bool     `toml:"read_stack_metadata"`
}

// Overlap tunes file overlap detection between unrelated branches.
type Overlap struct {
	Ignore         []string `toml:"ignore"`
	CheckConflicts bool     `toml:"check_conflicts"`
	MaxPairs       int      `toml:"max_pairs"`
	// MaxBranches caps how many recently active branches are compared.
	MaxBranches int `toml:"max_branches"`
}

// GitHub configures the GitHub integration.
type GitHub struct {
	// Enabled is "auto" (use when authenticated), "always", or "never".
	Enabled  string   `toml:"enabled"`
	CacheTTL Duration `toml:"cache_ttl"`
	// Host overrides the host detected from the remote URL (GitHub Enterprise).
	Host string `toml:"host"`
}

// AgentRule detects branches created by a coding agent.
type AgentRule struct {
	Disabled bool     `toml:"disabled"`
	Branches []string `toml:"branches"`
	Authors  []string `toml:"authors"`
	Trailers []string `toml:"trailers"`
	Paths    []string `toml:"paths"`
}

// AgentOutput configures `--agent` output.
type AgentOutput struct {
	MaxTokens int  `toml:"max_tokens"`
	Hints     bool `toml:"hints"`
	Legend    bool `toml:"legend"`
}

// Status configures `vb status`.
type Status struct {
	// Format is the template used by `vb status --line`.
	Format string `toml:"format"`
	// GitHub is "cache" (never hit the network), "auto", or "never".
	GitHub string `toml:"github"`
}

// TUI configures the interactive view.
type TUI struct {
	Refresh       Duration `toml:"refresh"`
	ConfirmDelete bool     `toml:"confirm_delete"`
}

// Web configures the local web view.
type Web struct {
	Port        int    `toml:"port"`
	OpenBrowser bool   `toml:"open_browser"`
	Theme       string `toml:"theme"`
	LightTheme  string `toml:"light_theme"`
	DarkTheme   string `toml:"dark_theme"`
}

// Duration is a time.Duration that also accepts days ("30d") and weeks ("2w").
type Duration struct{ time.Duration }

var durRe = regexp.MustCompile(`^(\d+)([dw])$`)

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(FormatDuration(d.Duration)), nil
}

// ParseDuration parses Go durations plus "Nd" and "Nw".
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	if m := durRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := 24 * time.Hour
		if m[2] == "w" {
			unit = 7 * 24 * time.Hour
		}
		return time.Duration(n) * unit, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (use values like 90s, 10m, 12h, 30d, 2w)", s)
	}
	return d, nil
}

// FormatDuration renders a duration the way ParseDuration reads it.
func FormatDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0"
	case d%(7*24*time.Hour) == 0:
		return fmt.Sprintf("%dw", d/(7*24*time.Hour))
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return d.String()
}

// Columns that can appear in the tree.
var AllColumns = []string{"pr", "review", "ci", "ahead_behind", "age", "worktree", "flags"}

// Defaults returns the built-in configuration.
func Defaults() *Config {
	return &Config{
		Remote: "origin",
		Display: Display{
			Theme:          theme.Default,
			Icons:          "unicode",
			Columns:        append([]string(nil), AllColumns...),
			ShowMerged:     true,
			Sort:           "recent",
			Header:         true,
			Footer:         true,
			Hints:          true,
			AttentionLimit: 8,
			MaxNameWidth:   44,
		},
		Analysis: Analysis{
			StaleAfter:        Duration{30 * 24 * time.Hour},
			Conflicts:         true,
			Overlap:           true,
			SquashDetection:   true,
			WorktreeStatus:    true,
			MaxBranches:       400,
			MaxCommits:        50000,
			SquashLookback:    1000,
			OldBaseThreshold:  100,
			ReadStackMetadata: true,
		},
		Overlap: Overlap{
			Ignore: []string{
				"go.sum", "*.lock", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
				"bun.lockb", "Cargo.lock", "poetry.lock", "uv.lock", "Gemfile.lock",
				"composer.lock", "CHANGELOG.md",
			},
			CheckConflicts: true,
			MaxPairs:       40,
			MaxBranches:    80,
		},
		GitHub: GitHub{
			Enabled:  "auto",
			CacheTTL: Duration{60 * time.Second},
		},
		Agents:      DefaultAgents(),
		Parents:     map[string]string{},
		AgentOutput: AgentOutput{MaxTokens: 0, Hints: true, Legend: true},
		Status: Status{
			Format: "{branch}{ab}{pr}{ci}{attention}",
			GitHub: "cache",
		},
		TUI:    TUI{Refresh: Duration{30 * time.Second}, ConfirmDelete: true},
		Web:    Web{Port: 7878, OpenBrowser: true, Theme: "auto", LightTheme: "rose-pine-dawn", DarkTheme: theme.Default},
		Themes: map[string]map[string]string{},
	}
}

// DefaultAgents returns the built-in agent detection rules.
func DefaultAgents() map[string]AgentRule {
	return map[string]AgentRule{
		"claude": {
			Branches: []string{"claude/*"},
			Trailers: []string{`(?i)\bclaude\b`},
			Paths:    []string{"*/.claude/worktrees/*"},
		},
		"codex": {
			Branches: []string{"codex/*"},
			Authors:  []string{`(?i)\bcodex\b`},
			Trailers: []string{`(?i)\bcodex\b`},
			Paths:    []string{"*/.codex/*"},
		},
		"copilot": {
			Branches: []string{"copilot/*"},
			Authors:  []string{`(?i)copilot`},
			Trailers: []string{`(?i)copilot`},
		},
		"cursor": {
			Branches: []string{"cursor/*"},
			Authors:  []string{`(?i)^cursor`},
			Trailers: []string{`(?i)\bcursor\b`},
		},
		"devin": {
			Branches: []string{"devin/*"},
			Authors:  []string{`(?i)devin-ai`},
		},
		"jules": {
			Branches: []string{"jules/*"},
			Authors:  []string{`(?i)google-labs-jules`},
		},
		"gemini": {
			Branches: []string{"gemini/*"},
			Trailers: []string{`(?i)\bgemini\b`},
		},
		"aider": {
			Trailers: []string{`(?i)\baider\b`},
			Authors:  []string{`(?i)\(aider\)`},
		},
		"agent": {
			Branches: []string{"agent/*", "agents/*", "ai/*"},
		},
	}
}

// Env lists supported environment variables.
var Env = map[string]string{
	"VB_CONFIG":  "path to an additional config file, loaded after the repository config",
	"VB_THEME":   "theme name, overrides display.theme",
	"VB_ICONS":   "unicode or ascii, overrides display.icons",
	"VB_OFFLINE": "1 to never contact GitHub and use cached data only",
	"VB_GITHUB":  "auto, always, or never, overrides github.enabled",
	"NO_COLOR":   "disable colors (https://no-color.org)",
}

// UserPath returns the user config file path.
func UserPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "vb", "config.toml")
	}
	if runtime.GOOS == "windows" {
		if dir, err := os.UserConfigDir(); err == nil {
			return filepath.Join(dir, "vb", "config.toml")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "vb", "config.toml")
}

// RepoPath returns the team config path for a repository root.
func RepoPath(root string) string { return filepath.Join(root, ".vb.toml") }

// LocalPath returns the personal, uncommitted config path for a git dir.
func LocalPath(commonDir string) string { return filepath.Join(commonDir, "vb.toml") }

// Load reads all configuration layers. root and commonDir may be empty when
// running outside a repository.
func Load(root, commonDir string) (*Config, error) {
	cfg := Defaults()
	paths := []string{UserPath()}
	if root != "" {
		paths = append(paths, RepoPath(root))
	}
	if commonDir != "" {
		paths = append(paths, LocalPath(commonDir))
	}
	if extra := os.Getenv("VB_CONFIG"); extra != "" {
		paths = append(paths, extra)
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := cfg.loadFile(p); err != nil {
			return nil, err
		}
	}
	cfg.applyEnv()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) loadFile(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	// Agent rules defined in a file replace the built-in rule of the same name
	// entirely, so decode them separately and merge by key.
	var agents struct {
		Agents map[string]AgentRule `toml:"agents"`
	}
	if _, err := toml.Decode(string(data), &agents); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	saved := c.Agents
	c.Agents = nil
	md, err := toml.Decode(string(data), c)
	if err != nil {
		c.Agents = saved
		return fmt.Errorf("%s: %w", path, err)
	}
	c.Agents = saved
	for name, rule := range agents.Agents {
		if c.Agents == nil {
			c.Agents = map[string]AgentRule{}
		}
		c.Agents[name] = rule
	}
	for _, key := range md.Undecoded() {
		c.Warnings = append(c.Warnings, fmt.Sprintf("%s: unknown key %q", path, key.String()))
	}
	c.Sources = append(c.Sources, path)
	return nil
}

func (c *Config) applyEnv() {
	if v := os.Getenv("VB_THEME"); v != "" {
		c.Display.Theme = v
	}
	if v := os.Getenv("VB_ICONS"); v != "" {
		c.Display.Icons = v
	}
	if v := os.Getenv("VB_GITHUB"); v != "" {
		c.GitHub.Enabled = v
	}
}

// Validate checks enumerations and cross-field constraints.
func (c *Config) Validate() error {
	var errs []string
	if _, err := theme.Resolve(c.Display.Theme, c.Themes); err != nil {
		errs = append(errs, "display.theme: "+err.Error())
	}
	for name := range c.Themes {
		if _, err := theme.Resolve(name, c.Themes); err != nil {
			errs = append(errs, "themes."+name+": "+err.Error())
		}
	}
	if !oneOf(c.Display.Icons, "unicode", "ascii") {
		errs = append(errs, fmt.Sprintf("display.icons must be unicode or ascii, got %q", c.Display.Icons))
	}
	if !oneOf(c.Display.Sort, "recent", "name", "oldest") {
		errs = append(errs, fmt.Sprintf("display.sort must be recent, name, or oldest, got %q", c.Display.Sort))
	}
	for _, col := range c.Display.Columns {
		if !oneOf(col, AllColumns...) {
			errs = append(errs, fmt.Sprintf("display.columns: unknown column %q (available: %s)", col, strings.Join(AllColumns, ", ")))
		}
	}
	if !oneOf(c.GitHub.Enabled, "auto", "always", "never") {
		errs = append(errs, fmt.Sprintf("github.enabled must be auto, always, or never, got %q", c.GitHub.Enabled))
	}
	if !oneOf(c.Status.GitHub, "cache", "auto", "never") {
		errs = append(errs, fmt.Sprintf("status.github must be cache, auto, or never, got %q", c.Status.GitHub))
	}
	if c.Web.Theme != "auto" {
		if _, err := theme.Resolve(c.Web.Theme, c.Themes); err != nil {
			errs = append(errs, "web.theme: "+err.Error())
		}
	}
	for _, key := range []struct{ name, value string }{{"web.light_theme", c.Web.LightTheme}, {"web.dark_theme", c.Web.DarkTheme}} {
		if _, err := theme.Resolve(key.value, c.Themes); err != nil {
			errs = append(errs, key.name+": "+err.Error())
		}
	}
	if c.Web.Port < 0 || c.Web.Port > 65535 {
		errs = append(errs, fmt.Sprintf("web.port out of range: %d", c.Web.Port))
	}
	for name, rule := range c.Agents {
		for _, re := range append(append([]string{}, rule.Authors...), rule.Trailers...) {
			if _, err := regexp.Compile(re); err != nil {
				errs = append(errs, fmt.Sprintf("agents.%s: invalid regular expression %q: %v", name, re, err))
			}
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("invalid configuration:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

func oneOf(v string, options ...string) bool {
	for _, o := range options {
		if v == o {
			return true
		}
	}
	return false
}

// Theme resolves the configured terminal theme.
func (c *Config) Theme() theme.Theme {
	t, err := theme.Resolve(c.Display.Theme, c.Themes)
	if err != nil {
		t, _ = theme.Builtin(theme.Default)
	}
	return t
}

// HasColumn reports whether a tree column is enabled.
func (c *Config) HasColumn(col string) bool {
	for _, x := range c.Display.Columns {
		if x == col {
			return true
		}
	}
	return false
}

// Encode renders the configuration as TOML.
func (c *Config) Encode() (string, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(c); err != nil {
		return "", err
	}
	return buf.String(), nil
}

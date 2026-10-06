package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	t.Setenv("VB_CONFIG", "")
	t.Setenv("VB_THEME", "")
	t.Setenv("VB_ICONS", "")
	t.Setenv("VB_GITHUB", "")
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultsAreValid(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTemplateMatchesDefaults(t *testing.T) {
	// Uncommenting every setting in the template must reproduce the defaults,
	// so the template never documents a stale value.
	// Only settings of the built-in sections; examples such as custom agent
	// rules, themes, parents, and hide patterns intentionally differ.
	setting := regexp.MustCompile(`^# ([a-z_]+) = (.*)$`)
	skip := map[string]bool{"trunks": true, "hide": true}
	var lines []string
	inExample := false
	for _, line := range strings.Split(Template, "\n") {
		if strings.HasPrefix(line, "[") {
			inExample = false
			lines = append(lines, line)
			continue
		}
		if strings.HasPrefix(line, "# [") {
			inExample = true
			continue
		}
		if m := setting.FindStringSubmatch(line); m != nil && !inExample && !skip[m[1]] {
			lines = append(lines, m[1]+" = "+m[2])
		}
	}
	got := Defaults()
	got.Display.Hide = nil
	if _, err := toml.Decode(strings.Join(lines, "\n"), got); err != nil {
		t.Fatalf("template does not parse: %v", err)
	}
	want := Defaults()
	a, _ := got.Encode()
	b, _ := want.Encode()
	if a != b {
		t.Errorf("template values differ from defaults:\n%s\n---\n%s", a, b)
	}
}

func TestLayering(t *testing.T) {
	dir := isolate(t)
	root := filepath.Join(dir, "repo")
	common := filepath.Join(root, ".git")
	write(t, filepath.Join(dir, "xdg", "vb", "config.toml"), `
[display]
theme = "nord"
sort = "name"
[github]
cache_ttl = "5m"
`)
	write(t, RepoPath(root), `
trunks = ["main", "develop"]
[display]
theme = "tokyo-night"
hide = ["dependabot/*"]
[parents]
"feat/ui" = "feat/api"
[agents.bot]
branches = ["bot/*"]
[agents.aider]
disabled = true
`)
	write(t, LocalPath(common), `
[display]
icons = "ascii"
[analysis]
stale_after = "2w"
`)
	cfg, err := Load(root, common)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Display.Theme != "tokyo-night" || cfg.Display.Sort != "name" || cfg.Display.Icons != "ascii" {
		t.Errorf("display = %+v", cfg.Display)
	}
	if cfg.GitHub.CacheTTL.Duration != 5*time.Minute {
		t.Errorf("cache ttl = %v", cfg.GitHub.CacheTTL)
	}
	if cfg.Analysis.StaleAfter.Duration != 14*24*time.Hour {
		t.Errorf("stale after = %v", cfg.Analysis.StaleAfter)
	}
	if len(cfg.Trunks) != 2 || cfg.Parents["feat/ui"] != "feat/api" {
		t.Errorf("trunks=%v parents=%v", cfg.Trunks, cfg.Parents)
	}
	if !cfg.Agents["aider"].Disabled || len(cfg.Agents["bot"].Branches) != 1 || len(cfg.Agents["claude"].Branches) == 0 {
		t.Errorf("agents = %+v", cfg.Agents)
	}
	if len(cfg.Sources) != 3 {
		t.Errorf("sources = %v", cfg.Sources)
	}

	t.Setenv("VB_THEME", "dracula")
	cfg, err = Load(root, common)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Display.Theme != "dracula" {
		t.Errorf("env theme not applied: %s", cfg.Display.Theme)
	}
}

func TestUnknownKeysWarn(t *testing.T) {
	dir := isolate(t)
	write(t, RepoPath(dir), "[display]\nthemee = \"nord\"\n")
	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "display.themee") {
		t.Errorf("warnings = %v", cfg.Warnings)
	}
}

func TestInvalidValues(t *testing.T) {
	cases := map[string]string{
		"theme":    "[display]\ntheme = \"nope\"\n",
		"icons":    "[display]\nicons = \"emoji\"\n",
		"column":   "[display]\ncolumns = [\"pr\", \"stars\"]\n",
		"github":   "[github]\nenabled = \"sometimes\"\n",
		"duration": "[analysis]\nstale_after = \"soon\"\n",
		"regex":    "[agents.x]\nauthors = [\"(\"]\n",
		"custom":   "[themes.mine]\naccent = \"pink\"\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := isolate(t)
			write(t, RepoPath(dir), content)
			if _, err := Load(dir, ""); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestCustomTheme(t *testing.T) {
	dir := isolate(t)
	write(t, RepoPath(dir), "[display]\ntheme = \"mine\"\n[themes.mine]\nbase = \"nord\"\naccent = \"#ff0000\"\n")
	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	th := cfg.Theme()
	if th.Accent != "#ff0000" || th.Bg != "#2e3440" {
		t.Errorf("custom theme = %+v", th)
	}
}

func TestDurations(t *testing.T) {
	for in, want := range map[string]time.Duration{"90s": 90 * time.Second, "3d": 72 * time.Hour, "2w": 14 * 24 * time.Hour, "0": 0} {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v", in, got, err)
		}
		if in != "0" && FormatDuration(got) != in {
			t.Errorf("FormatDuration(%v) = %q", got, FormatDuration(got))
		}
	}
}

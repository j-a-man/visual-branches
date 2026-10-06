// Package theme defines vb's color palettes.
//
// A theme maps semantic roles (success, danger, muted, ...) to colors. The
// terminal, TUI, SVG export, and web view all read the same roles, so a theme
// looks consistent everywhere. Users can define their own themes in config.
package theme

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Theme is a set of semantic colors. Terminal roles accept hex colors or ANSI
// color numbers ("1".."15"); surface roles used by SVG and the web view must
// be hex.
type Theme struct {
	Name string `json:"name"`
	Dark bool   `json:"dark"`

	// Surfaces (SVG and web).
	Bg      string `json:"bg"`
	Surface string `json:"surface"`
	Overlay string `json:"overlay"`
	Border  string `json:"border"`

	// Text.
	Text   string `json:"text"`
	Subtle string `json:"subtle"`
	Muted  string `json:"muted"`
	Tree   string `json:"tree"`

	// Semantics.
	Accent  string `json:"accent"`
	Success string `json:"success"`
	Warning string `json:"warning"`
	Danger  string `json:"danger"`
	Info    string `json:"info"`
	Merged  string `json:"merged"`
	Agent   string `json:"agent"`
}

// Default is the theme used when none is configured.
const Default = "rose-pine-moon"

var builtin = map[string]Theme{
	"rose-pine-moon": {
		Name: "rose-pine-moon", Dark: true,
		Bg: "#232136", Surface: "#2a273f", Overlay: "#393552", Border: "#44415a",
		Text: "#e0def4", Subtle: "#908caa", Muted: "#6e6a86", Tree: "#56526e",
		Accent: "#ea9a97", Success: "#9ccfd8", Warning: "#f6c177", Danger: "#eb6f92",
		Info: "#3e8fb0", Merged: "#c4a7e7", Agent: "#c4a7e7",
	},
	"rose-pine": {
		Name: "rose-pine", Dark: true,
		Bg: "#191724", Surface: "#1f1d2e", Overlay: "#26233a", Border: "#403d52",
		Text: "#e0def4", Subtle: "#908caa", Muted: "#6e6a86", Tree: "#524f67",
		Accent: "#ebbcba", Success: "#9ccfd8", Warning: "#f6c177", Danger: "#eb6f92",
		Info: "#31748f", Merged: "#c4a7e7", Agent: "#c4a7e7",
	},
	"rose-pine-dawn": {
		Name: "rose-pine-dawn", Dark: false,
		Bg: "#faf4ed", Surface: "#fffaf3", Overlay: "#f2e9e1", Border: "#dfdad9",
		Text: "#575279", Subtle: "#797593", Muted: "#9893a5", Tree: "#cecacd",
		Accent: "#d7827e", Success: "#56949f", Warning: "#ea9d34", Danger: "#b4637a",
		Info: "#286983", Merged: "#907aa9", Agent: "#907aa9",
	},
	"catppuccin-mocha": {
		Name: "catppuccin-mocha", Dark: true,
		Bg: "#1e1e2e", Surface: "#181825", Overlay: "#313244", Border: "#45475a",
		Text: "#cdd6f4", Subtle: "#a6adc8", Muted: "#7f849c", Tree: "#585b70",
		Accent: "#f5c2e7", Success: "#a6e3a1", Warning: "#f9e2af", Danger: "#f38ba8",
		Info: "#89b4fa", Merged: "#cba6f7", Agent: "#b4befe",
	},
	"catppuccin-latte": {
		Name: "catppuccin-latte", Dark: false,
		Bg: "#eff1f5", Surface: "#e6e9ef", Overlay: "#ccd0da", Border: "#bcc0cc",
		Text: "#4c4f69", Subtle: "#6c6f85", Muted: "#8c8fa1", Tree: "#acb0be",
		Accent: "#ea76cb", Success: "#40a02b", Warning: "#df8e1d", Danger: "#d20f39",
		Info: "#1e66f5", Merged: "#8839ef", Agent: "#7287fd",
	},
	"tokyo-night": {
		Name: "tokyo-night", Dark: true,
		Bg: "#1a1b26", Surface: "#16161e", Overlay: "#292e42", Border: "#3b4261",
		Text: "#c0caf5", Subtle: "#a9b1d6", Muted: "#565f89", Tree: "#414868",
		Accent: "#ff9e64", Success: "#9ece6a", Warning: "#e0af68", Danger: "#f7768e",
		Info: "#7aa2f7", Merged: "#bb9af7", Agent: "#7dcfff",
	},
	"nord": {
		Name: "nord", Dark: true,
		Bg: "#2e3440", Surface: "#3b4252", Overlay: "#434c5e", Border: "#4c566a",
		Text: "#eceff4", Subtle: "#d8dee9", Muted: "#616e88", Tree: "#4c566a",
		Accent: "#88c0d0", Success: "#a3be8c", Warning: "#ebcb8b", Danger: "#bf616a",
		Info: "#81a1c1", Merged: "#b48ead", Agent: "#8fbcbb",
	},
	"gruvbox-dark": {
		Name: "gruvbox-dark", Dark: true,
		Bg: "#282828", Surface: "#32302f", Overlay: "#3c3836", Border: "#504945",
		Text: "#ebdbb2", Subtle: "#a89984", Muted: "#928374", Tree: "#665c54",
		Accent: "#fe8019", Success: "#b8bb26", Warning: "#fabd2f", Danger: "#fb4934",
		Info: "#83a598", Merged: "#d3869b", Agent: "#8ec07c",
	},
	"dracula": {
		Name: "dracula", Dark: true,
		Bg: "#282a36", Surface: "#21222c", Overlay: "#44475a", Border: "#6272a4",
		Text: "#f8f8f2", Subtle: "#bfbfbf", Muted: "#6272a4", Tree: "#44475a",
		Accent: "#ff79c6", Success: "#50fa7b", Warning: "#f1fa8c", Danger: "#ff5555",
		Info: "#8be9fd", Merged: "#bd93f9", Agent: "#bd93f9",
	},
	"github-dark": {
		Name: "github-dark", Dark: true,
		Bg: "#0d1117", Surface: "#161b22", Overlay: "#21262d", Border: "#30363d",
		Text: "#e6edf3", Subtle: "#9198a1", Muted: "#6e7681", Tree: "#3d444d",
		Accent: "#f0883e", Success: "#3fb950", Warning: "#d29922", Danger: "#f85149",
		Info: "#4493f8", Merged: "#ab7df8", Agent: "#a371f7",
	},
	"github-light": {
		Name: "github-light", Dark: false,
		Bg: "#ffffff", Surface: "#f6f8fa", Overlay: "#eaeef2", Border: "#d0d7de",
		Text: "#1f2328", Subtle: "#59636e", Muted: "#818b98", Tree: "#d1d9e0",
		Accent: "#bc4c00", Success: "#1a7f37", Warning: "#9a6700", Danger: "#d1242f",
		Info: "#0969da", Merged: "#8250df", Agent: "#8250df",
	},
	// ansi uses the terminal's own 16-color palette, so vb matches whatever
	// theme the terminal is configured with. Surfaces fall back to
	// rose-pine-moon for SVG and web output.
	"ansi": {
		Name: "ansi", Dark: true,
		Bg: "#232136", Surface: "#2a273f", Overlay: "#393552", Border: "#44415a",
		Text: "", Subtle: "7", Muted: "8", Tree: "8",
		Accent: "5", Success: "2", Warning: "3", Danger: "1",
		Info: "4", Merged: "5", Agent: "6",
	},
	// mono uses no color at all; emphasis comes from bold and faint text.
	"mono": {
		Name: "mono", Dark: true,
		Bg: "#1c1c1c", Surface: "#262626", Overlay: "#303030", Border: "#3a3a3a",
	},
}

// Names returns all built-in theme names, sorted.
func Names() []string {
	names := make([]string, 0, len(builtin))
	for n := range builtin {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Builtin returns a built-in theme.
func Builtin(name string) (Theme, bool) {
	t, ok := builtin[strings.ToLower(name)]
	return t, ok
}

var hexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var ansiRe = regexp.MustCompile(`^([0-9]|1[0-5])$`)

// Resolve finds a theme by name, applying custom definitions. A custom theme
// may set `base` to inherit from another theme and override individual roles.
func Resolve(name string, custom map[string]map[string]string) (Theme, error) {
	return resolve(strings.ToLower(name), custom, 0)
}

func resolve(name string, custom map[string]map[string]string, depth int) (Theme, error) {
	if depth > 8 {
		return Theme{}, fmt.Errorf("theme %q: base chain too deep", name)
	}
	def, isCustom := lookupCustom(custom, name)
	if !isCustom {
		t, ok := builtin[name]
		if !ok {
			return Theme{}, fmt.Errorf("unknown theme %q (available: %s)", name, strings.Join(Names(), ", "))
		}
		return t, nil
	}
	base := def["base"]
	if base == "" {
		base = Default
	}
	var t Theme
	var err error
	if strings.EqualFold(base, name) {
		bt, ok := builtin[strings.ToLower(base)]
		if !ok {
			return Theme{}, fmt.Errorf("theme %q cannot use itself as base", name)
		}
		t = bt
	} else {
		t, err = resolve(strings.ToLower(base), custom, depth+1)
		if err != nil {
			return Theme{}, err
		}
	}
	t.Name = name
	for role, value := range def {
		role = strings.ToLower(role)
		if role == "base" {
			continue
		}
		if role == "dark" {
			t.Dark = value == "true"
			continue
		}
		field := t.field(role)
		if field == nil {
			return Theme{}, fmt.Errorf("theme %q: unknown color role %q", name, role)
		}
		if !hexRe.MatchString(value) && !ansiRe.MatchString(value) {
			return Theme{}, fmt.Errorf("theme %q: %s must be #rrggbb or an ANSI color number 0-15, got %q", name, role, value)
		}
		*field = value
	}
	return t, nil
}

func lookupCustom(custom map[string]map[string]string, name string) (map[string]string, bool) {
	for k, v := range custom {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return nil, false
}

// Roles lists the configurable color roles.
func Roles() []string {
	return []string{"bg", "surface", "overlay", "border", "text", "subtle", "muted", "tree", "accent", "success", "warning", "danger", "info", "merged", "agent"}
}

func (t *Theme) field(role string) *string {
	switch role {
	case "bg":
		return &t.Bg
	case "surface":
		return &t.Surface
	case "overlay":
		return &t.Overlay
	case "border":
		return &t.Border
	case "text":
		return &t.Text
	case "subtle":
		return &t.Subtle
	case "muted":
		return &t.Muted
	case "tree":
		return &t.Tree
	case "accent":
		return &t.Accent
	case "success":
		return &t.Success
	case "warning":
		return &t.Warning
	case "danger":
		return &t.Danger
	case "info":
		return &t.Info
	case "merged":
		return &t.Merged
	case "agent":
		return &t.Agent
	}
	return nil
}

// Hex returns a hex color for a role, substituting a fallback theme's color
// when the role holds an ANSI number or is empty. Used by SVG and web output.
func (t Theme) Hex(role string) string {
	tt := t
	v := ""
	if f := tt.field(role); f != nil {
		v = *f
	}
	if hexRe.MatchString(v) {
		return v
	}
	fb := builtin[Default]
	if !t.Dark {
		fb = builtin["rose-pine-dawn"]
	}
	return *fb.field(role)
}

// Web returns the theme with every role converted to hex, for CSS.
func (t Theme) Web() Theme {
	out := t
	for _, r := range Roles() {
		*out.field(r) = t.Hex(r)
	}
	return out
}

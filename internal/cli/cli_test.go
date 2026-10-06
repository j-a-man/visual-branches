package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/j-a-man/visual-branches/internal/demorepo"
	"github.com/j-a-man/visual-branches/internal/testrepo"
)

// runVB executes the command line in-process and returns stdout.
func runVB(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	a := &app{out: &out, err: &errOut}
	root := a.rootCmd()
	root.SetArgs(append([]string{"-C", dir}, args...))
	root.SetOut(&out)
	root.SetErr(&errOut)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func demo(t *testing.T) string {
	t.Helper()
	testrepo.Isolate(t)
	res, err := demorepo.Build(filepath.Join(t.TempDir(), "demo"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("VB_GITHUB_FIXTURE", res.Fixture)
	return res.Repo
}

func TestEndToEnd(t *testing.T) {
	dir := demo(t)

	out, err := runVB(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"webapp", "feat/auth-api", "feat/auth-ui *", "#143", "restack", "collision", "conflict",
		"needs you", "#143 CI failing: lint", "ready to merge", "merged branches can be deleted",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("tree output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("non-terminal output contains escape codes")
	}

	agent, err := runVB(t, dir, "--agent")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(agent, "feat/auth-ui* +2 -1 pr#143:open,review-required,fail(lint)") {
		t.Errorf("agent output:\n%s", agent)
	}
	state := ""
	for _, f := range strings.Fields(strings.SplitN(agent, "\n", 2)[0]) {
		if strings.HasPrefix(f, "state=") {
			state = strings.TrimPrefix(f, "state=")
		}
	}
	delta, err := runVB(t, dir, "--since", state)
	if err != nil || !strings.Contains(delta, "no changes") {
		t.Errorf("--since: %v\n%s", err, delta)
	}

	js, err := runVB(t, dir, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		SchemaVersion int `json:"schemaVersion"`
		Branches      []struct {
			Name   string `json:"name"`
			Parent string `json:"parent"`
		} `json:"branches"`
	}
	if err := json.Unmarshal([]byte(js), &m); err != nil || m.SchemaVersion != 1 || len(m.Branches) < 10 {
		t.Fatalf("json: %v %+v", err, m)
	}

	show, err := runVB(t, dir, "show", "feat/auth-ui")
	if err != nil || !strings.Contains(show, "commits not on feat/auth-api") || !strings.Contains(show, "lint") {
		t.Errorf("show: %v\n%s", err, show)
	}
	focus, err := runVB(t, dir, "--focus", "feat/auth-ui", "--agent")
	if err != nil || strings.Contains(focus, "spike/graphql") || !strings.Contains(focus, "feat/auth-api") {
		t.Errorf("focus: %v\n%s", err, focus)
	}

	for _, format := range []string{"mermaid", "dot", "svg", "json"} {
		if out, err := runVB(t, dir, "export", format); err != nil || len(out) < 100 {
			t.Errorf("export %s: %v (%d bytes)", format, err, len(out))
		}
	}

	status, err := runVB(t, dir, "status")
	if err != nil || !strings.HasPrefix(status, "feat/auth-ui ↑2↓1 #143 ✗") {
		t.Errorf("status: %v %q", err, status)
	}

	if _, err := runVB(t, dir, "--exit-code"); err == nil {
		t.Error("--exit-code should fail when high-severity issues exist")
	}

	cleanup, err := runVB(t, dir, "cleanup")
	if err != nil || !strings.Contains(cleanup, "chore/deps") || !strings.Contains(cleanup, "feat/search") {
		t.Errorf("cleanup: %v\n%s", err, cleanup)
	}
	if _, err := runVB(t, dir, "cleanup", "--apply", "--yes"); err != nil {
		t.Fatal(err)
	}
	after, _ := runVB(t, dir, "--agent")
	if strings.Contains(after, "chore/deps") {
		t.Errorf("cleanup --apply did not delete merged branches:\n%s", after)
	}

	if _, err := runVB(t, dir, "parent", "spike/graphql", "fix/login-race"); err != nil {
		t.Fatal(err)
	}
	p, _ := runVB(t, dir, "parent", "spike/graphql")
	if !strings.Contains(p, "spike/graphql -> fix/login-race (source: config") {
		t.Errorf("parent pin: %q", p)
	}
	if _, err := runVB(t, dir, "parent", "spike/graphql", "--unset"); err != nil {
		t.Fatal(err)
	}

	ctxOut, err := runVB(t, dir, "context", "--max-tokens", "200")
	if err != nil || !strings.Contains(ctxOut, "Branch map from vb") {
		t.Errorf("context: %v\n%s", err, ctxOut)
	}
}

func TestContextOutsideRepoIsSilent(t *testing.T) {
	testrepo.Isolate(t)
	out, err := runVB(t, t.TempDir(), "context")
	if err != nil || out != "" {
		t.Errorf("context outside a repository: %v %q", err, out)
	}
	if _, err := runVB(t, t.TempDir()); err == nil || !strings.Contains(err.Error(), "not inside a git repository") {
		t.Errorf("expected a clear error, got %v", err)
	}
}

func TestConfigCommands(t *testing.T) {
	dir := demo(t)
	if _, err := runVB(t, dir, "config", "init", "--scope", "repo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".vb.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := runVB(t, dir, "config", "init", "--scope", "repo"); err == nil {
		t.Error("init should refuse to overwrite")
	}
	show, err := runVB(t, dir, "config", "show")
	if err != nil || !strings.Contains(show, `theme = "rose-pine-moon"`) || !strings.Contains(show, ".vb.toml") {
		t.Errorf("config show: %v\n%s", err, show)
	}
	themes, err := runVB(t, dir, "themes")
	if err != nil || !strings.Contains(themes, "* rose-pine-moon") || !strings.Contains(themes, "catppuccin-mocha") {
		t.Errorf("themes: %v\n%s", err, themes)
	}
	if out, err := runVB(t, dir, "--theme", "nope"); err == nil {
		t.Errorf("unknown theme accepted: %s", out)
	}
	doctor, _ := runVB(t, dir, "doctor")
	if !strings.Contains(doctor, "repository") || !strings.Contains(doctor, "config") {
		t.Errorf("doctor:\n%s", doctor)
	}
}

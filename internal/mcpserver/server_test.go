package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/j-a-man/visual-branches/internal/mcpserver"
	"github.com/j-a-man/visual-branches/internal/testrepo"
)

func TestTools(t *testing.T) {
	testrepo.Isolate(t)
	t.Setenv("VB_GITHUB", "never")
	r := testrepo.New(t)
	r.Commit("main", "shared.txt", "a\n", "shared")
	r.Branch("feat/a", "main")
	r.Commit("feat/a", "shared.txt", "a from feat\n", "feat edits shared")
	r.Branch("claude/b", "main")
	r.Commit("claude/b", "shared.txt", "a from claude\n", "claude edits shared")
	r.Switch("feat/a")

	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	server := mcpserver.New(mcpserver.Options{Dir: r.Dir, Version: "test", Offline: true})
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not annotated read-only", tool.Name)
		}
	}
	for _, want := range []string{"branch_map", "branch_detail", "changes_since", "check_conflicts", "who_touches", "cleanup_candidates"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}

	call := func(name string, args map[string]any) string {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var b strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				b.WriteString(tc.Text)
			}
		}
		if res.IsError {
			t.Fatalf("%s returned error: %s", name, b.String())
		}
		return b.String()
	}

	out := call("branch_map", map[string]any{})
	if !strings.Contains(out, "feat/a*") || !strings.Contains(out, "claude/b") || !strings.Contains(out, "collision") {
		t.Errorf("branch_map output:\n%s", out)
	}
	state := ""
	for _, f := range strings.Fields(strings.SplitN(out, "\n", 2)[0]) {
		if strings.HasPrefix(f, "state=") {
			state = strings.TrimPrefix(f, "state=")
		}
	}
	if state == "" {
		t.Fatalf("no state token in:\n%s", out)
	}
	if got := call("changes_since", map[string]any{"state": state}); !strings.Contains(got, "no changes") {
		t.Errorf("changes_since:\n%s", got)
	}
	if got := call("who_touches", map[string]any{"paths": []string{"shared.txt"}}); !strings.Contains(got, "claude/b") {
		t.Errorf("who_touches:\n%s", got)
	}
	if got := call("check_conflicts", map[string]any{"branch": "feat/a", "target": "claude/b"}); !strings.Contains(got, "CONFLICTS") {
		t.Errorf("check_conflicts:\n%s", got)
	}
	if got := call("branch_detail", map[string]any{}); !strings.Contains(got, "feat edits shared") {
		t.Errorf("branch_detail:\n%s", got)
	}
	if got := call("cleanup_candidates", map[string]any{}); !strings.Contains(got, "no merged branches") {
		t.Errorf("cleanup_candidates:\n%s", got)
	}
}

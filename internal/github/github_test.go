package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/j-a-man/visual-branches/internal/model"
)

func TestParseRemote(t *testing.T) {
	cases := []struct {
		url, host, owner, repo string
		ok                     bool
	}{
		{"https://github.com/j-a-man/visual-branches.git", "github.com", "j-a-man", "visual-branches", true},
		{"https://github.com/j-a-man/visual-branches", "github.com", "j-a-man", "visual-branches", true},
		{"git@github.com:j-a-man/visual-branches.git", "github.com", "j-a-man", "visual-branches", true},
		{"ssh://git@ssh.github.com:443/j-a-man/visual-branches.git", "github.com", "j-a-man", "visual-branches", true},
		{"https://ghe.example.com/team/app", "ghe.example.com", "team", "app", true},
		{"git://github.com/a/b", "github.com", "a", "b", true},
		{"/srv/git/repo.git", "", "", "", false},
		{"", "", "", "", false},
	}
	for _, c := range cases {
		host, owner, repo, ok := ParseRemote(c.url)
		if ok != c.ok || host != c.host || owner != c.owner || repo != c.repo {
			t.Errorf("ParseRemote(%q) = %q %q %q %v", c.url, host, owner, repo, ok)
		}
	}
}

// fakeClient answers the batched query with canned PR nodes.
type fakeClient struct {
	mu    sync.Mutex
	calls int
	nodes map[string][]prNode
}

func (f *fakeClient) DoWithContext(_ context.Context, query string, vars map[string]interface{}, response interface{}) error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	repo := map[string]any{}
	switch {
	case strings.Contains(query, "states: OPEN"):
		var nodes []map[string]any
		for head, list := range f.nodes {
			for _, n := range list {
				if n.State == "OPEN" {
					nodes = append(nodes, map[string]any{"number": n.Number, "headRefName": head, "headRepositoryOwner": n.HeadRepositoryOwner})
				}
			}
		}
		repo["pullRequests"] = map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": nodes}
	case strings.Contains(query, "pullRequest(number"):
		for k, v := range vars {
			if !strings.HasPrefix(k, "n") || k == "name" {
				continue
			}
			for head, list := range f.nodes {
				for _, n := range list {
					if n.Number == v.(int) {
						n.HeadRefName = head
						repo["p"+k[1:]] = n
					}
				}
			}
		}
	default:
		for k, v := range vars {
			if !strings.HasPrefix(k, "h") {
				continue
			}
			repo["b"+k[1:]] = map[string]any{"nodes": f.nodes[v.(string)]}
		}
	}
	data, _ := json.Marshal(map[string]any{"repository": repo})
	return json.Unmarshal(data, response)
}

func TestFetchManyHeads(t *testing.T) {
	t.Setenv("VB_GITHUB_FIXTURE", "")
	fc := &fakeClient{nodes: map[string][]prNode{}}
	var heads []string
	for i := 0; i < 120; i++ {
		h := fmt.Sprintf("feat/%03d", i)
		heads = append(heads, h)
		state := "MERGED"
		if i%3 == 0 {
			state = "OPEN"
		}
		fc.nodes[h] = []prNode{{Number: 1000 + i, State: state, BaseRefName: "main", HeadRepositoryOwner: &login{Login: "acme"}}}
	}
	f := &Fetcher{Host: "github.com", Owner: "acme", Repo: "app", TTL: time.Minute, Client: fc}
	prs, st := f.PRs(context.Background(), heads)
	if st.Status != model.GitHubOK {
		t.Fatalf("status = %+v", st)
	}
	open := 0
	for _, pr := range prs {
		if pr.State == model.PROpen {
			open++
		}
	}
	// Every open PR is found through the bulk list, even beyond the lookup
	// limit; merged PRs are looked up only for the most recent heads.
	if open != 40 {
		t.Errorf("open PRs = %d, want 40", open)
	}
	if pr := prs["feat/001"]; pr == nil || pr.State != model.PRMerged {
		t.Errorf("recent merged PR missing: %+v", pr)
	}
	if prs["feat/119"] != nil && prs["feat/119"].State == model.PRMerged {
		t.Error("merged PR beyond the lookup limit should not be fetched")
	}
}

func TestFetchChoosesOpenPR(t *testing.T) {
	t.Setenv("VB_GITHUB_FIXTURE", "")
	open := prNode{Number: 7, Title: "Open one", State: "OPEN", BaseRefName: "main", ReviewDecision: "APPROVED", Mergeable: "MERGEABLE",
		HeadRepositoryOwner: &login{Login: "acme"}}
	open.Commits.Nodes = append(open.Commits.Nodes, struct {
		Commit struct {
			StatusCheckRollup *struct {
				State    string `json:"state"`
				Contexts struct {
					Nodes []checkNode `json:"nodes"`
				} `json:"contexts"`
			} `json:"statusCheckRollup"`
		} `json:"commit"`
	}{})
	roll := &struct {
		State    string `json:"state"`
		Contexts struct {
			Nodes []checkNode `json:"nodes"`
		} `json:"contexts"`
	}{State: "FAILURE"}
	roll.Contexts.Nodes = []checkNode{
		{Typename: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Typename: "CheckRun", Name: "lint", Status: "COMPLETED", Conclusion: "FAILURE"},
		{Typename: "StatusContext", Context: "deploy", State: "PENDING"},
	}
	open.Commits.Nodes[0].Commit.StatusCheckRollup = roll
	merged := prNode{Number: 5, State: "MERGED", BaseRefName: "main", HeadRepositoryOwner: &login{Login: "acme"}}
	fork := prNode{Number: 9, State: "OPEN", BaseRefName: "main", HeadRepositoryOwner: &login{Login: "someone-else"}}

	fc := &fakeClient{nodes: map[string][]prNode{
		"feat/a": {fork, open, merged},
		"feat/b": {merged},
	}}
	f := &Fetcher{Host: "github.com", Owner: "acme", Repo: "app", TTL: time.Minute, Client: fc}
	prs, st := f.PRs(context.Background(), []string{"feat/a", "feat/b", "feat/c"})
	if st.Status != model.GitHubOK {
		t.Fatalf("status = %+v", st)
	}
	a := prs["feat/a"]
	if a == nil || a.Number != 7 || a.State != model.PROpen || a.Review != model.ReviewApproved || a.CI != model.CIFail {
		t.Fatalf("feat/a = %+v", a)
	}
	if got := a.FailingChecks(); len(got) != 1 || got[0] != "lint" {
		t.Errorf("failing checks = %v", got)
	}
	if a.Checks[0].Status != model.CIFail {
		t.Errorf("checks not sorted with failures first: %+v", a.Checks)
	}
	if b := prs["feat/b"]; b == nil || b.State != model.PRMerged {
		t.Errorf("feat/b = %+v", b)
	}
	if prs["feat/c"] != nil {
		t.Errorf("feat/c should have no PR")
	}
}

// TestLiveGitHub runs the real GraphQL query against a public repository
// using the local gh login. Opt in with VB_LIVE_GITHUB=1.
func TestLiveGitHub(t *testing.T) {
	if os.Getenv("VB_LIVE_GITHUB") == "" {
		t.Skip("set VB_LIVE_GITHUB=1 to run against the GitHub API")
	}
	heads := []string{"trunk", "does-not-exist-vb-test"}
	if extra := os.Getenv("VB_LIVE_HEADS"); extra != "" {
		heads = append(heads, strings.Split(extra, ",")...)
	}
	f := &Fetcher{Host: "github.com", Owner: "cli", Repo: "cli", TTL: time.Minute}
	prs, st := f.PRs(context.Background(), heads)
	if st.Status != model.GitHubOK {
		t.Fatalf("status = %+v", st)
	}
	for h, pr := range prs {
		t.Logf("%s -> #%d %s review=%q ci=%s mergeable=%s checks=%d draft=%v +%d -%d", h, pr.Number, pr.State, pr.Review, pr.CI, pr.Mergeable, len(pr.Checks), pr.Draft, pr.Additions, pr.Deletions)
	}
}

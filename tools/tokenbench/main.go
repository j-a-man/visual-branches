// Command tokenbench measures how many tokens a coding agent spends learning
// the state of a repository's branches, with and without vb.
//
// The baseline is the set of commands an agent typically runs to orient
// itself: git status, git branch -vv, git log --graph, git worktree list,
// git stash list, gh pr list, and gh pr checks for every open pull request.
// The vb side is a single `vb --agent` call (and a budgeted variant).
//
//	go run ./tools/tokenbench              # demo repository, simulated gh output
//	go run ./tools/tokenbench -repo .      # a real repository, real git and gh
//	go run ./tools/tokenbench -exact       # exact Claude token counts via the API
//
// Estimates use about 3.5 characters per token. With -exact, counts come from
// the Anthropic token counting endpoint (needs ANTHROPIC_API_KEY or an
// `ant auth login` profile).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/j-a-man/visual-branches/internal/config"
	"github.com/j-a-man/visual-branches/internal/demorepo"
	"github.com/j-a-man/visual-branches/internal/engine"
	"github.com/j-a-man/visual-branches/internal/model"
	"github.com/j-a-man/visual-branches/internal/render"
)

type step struct {
	name      string
	output    string
	simulated bool
}

func main() {
	repoFlag := flag.String("repo", "", "repository to measure (default: build the demo repository)")
	exact := flag.Bool("exact", false, "count tokens exactly with the Anthropic API")
	markdown := flag.Bool("markdown", false, "print a Markdown table")
	flag.Parse()

	ctx := context.Background()
	repo := *repoFlag
	simulated := false
	var fixture map[string]*model.PR
	if repo == "" {
		dir, err := os.MkdirTemp("", "vb-tokenbench-")
		must(err)
		defer os.RemoveAll(dir)
		res, err := demorepo.Build(filepath.Join(dir, "demo"))
		must(err)
		repo = res.Repo
		must(os.Setenv("VB_GITHUB_FIXTURE", res.Fixture))
		data, err := os.ReadFile(res.Fixture)
		must(err)
		must(json.Unmarshal(data, &fixture))
		simulated = true
	}
	abs, err := filepath.Abs(repo)
	must(err)
	repo = abs

	baseline := []step{
		{name: "git status", output: run(repo, "git", "status")},
		{name: "git branch -vv", output: run(repo, "git", "branch", "-vv")},
		{name: "git log --oneline --graph --decorate --all -n 60", output: run(repo, "git", "log", "--oneline", "--graph", "--decorate", "--all", "-n", "60")},
		{name: "git worktree list", output: run(repo, "git", "worktree", "list")},
		{name: "git stash list", output: run(repo, "git", "stash", "list")},
	}
	if simulated {
		baseline = append(baseline, simulateGH(fixture)...)
	} else if _, err := exec.LookPath("gh"); err == nil {
		baseline = append(baseline, realGH(repo)...)
	}

	cfg := config.Defaults()
	s, err := engine.Open(ctx, engine.Options{Dir: repo, Config: cfg, Tool: "vb bench"})
	must(err)
	m, err := s.Build(ctx)
	must(err)
	flt := render.Filter{ShowMerged: true}
	full := render.Agent(m, render.AgentOptions{Now: time.Now(), Filter: flt, Hints: true, Legend: true})
	budget := render.Agent(m, render.AgentOptions{Now: time.Now(), Filter: flt, Hints: true, Legend: true, MaxTokens: 250})

	var counter func(string) (int, error)
	if *exact {
		client := anthropic.NewClient()
		counter = func(text string) (int, error) {
			res, err := client.Messages.CountTokens(ctx, anthropic.MessageCountTokensParams{
				Model:    "claude-opus-5",
				Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(text))},
			})
			if err != nil {
				return 0, err
			}
			return int(res.InputTokens), nil
		}
	}

	var joined strings.Builder
	for _, st := range baseline {
		joined.WriteString("$ " + st.name + "\n" + st.output + "\n")
	}
	rows := []result{
		measure("baseline: git + gh commands", len(baseline), joined.String(), counter),
		measure("vb --agent", 1, full, counter),
		measure("vb --agent --max-tokens 250", 1, budget, counter),
	}

	fmt.Printf("repository: %s (%d branches)\n", repo, len(m.Branches))
	if simulated {
		fmt.Println("gh output is simulated from the demo's pull request fixture")
	}
	fmt.Println()
	if *markdown {
		printMarkdown(rows, *exact)
	} else {
		printTable(rows, *exact)
	}
	fmt.Println()
	fmt.Println("baseline breakdown:")
	sort.SliceStable(baseline, func(i, j int) bool { return len(baseline[i].output) > len(baseline[j].output) })
	for _, st := range baseline {
		tag := ""
		if st.simulated {
			tag = " (simulated)"
		}
		fmt.Printf("  %6d est. tokens  %s%s\n", render.EstimateTokens(st.output), st.name, tag)
	}
	fmt.Println()
	fmt.Println("vb --agent output:")
	fmt.Println(indent(full))
}

type result struct {
	name   string
	calls  int
	bytes  int
	est    int
	exact  int
	exErr  error
	output string
}

func measure(name string, calls int, text string, counter func(string) (int, error)) result {
	r := result{name: name, calls: calls, bytes: len(text), est: render.EstimateTokens(text), output: text}
	if counter != nil {
		r.exact, r.exErr = counter(text)
	}
	return r
}

func printTable(rows []result, exact bool) {
	fmt.Printf("%-32s %6s %8s %12s", "approach", "calls", "bytes", "est. tokens")
	if exact {
		fmt.Printf(" %13s", "exact tokens")
	}
	fmt.Println()
	base := rows[0]
	for _, r := range rows {
		fmt.Printf("%-32s %6d %8d %12d", r.name, r.calls, r.bytes, r.est)
		if exact {
			if r.exErr != nil {
				fmt.Printf(" %13s", "error")
			} else {
				fmt.Printf(" %13d", r.exact)
			}
		}
		if r.name != base.name {
			fmt.Printf("   %.0f%% fewer tokens", 100*(1-float64(r.est)/float64(base.est)))
		}
		fmt.Println()
	}
	for _, r := range rows {
		if r.exErr != nil {
			fmt.Printf("\nexact count failed: %v\n", r.exErr)
			break
		}
	}
}

func printMarkdown(rows []result, exact bool) {
	header := "| Approach | Calls | Bytes | Tokens (est.) |"
	sep := "| --- | ---: | ---: | ---: |"
	if exact {
		header += " Tokens (exact) |"
		sep += " ---: |"
	}
	fmt.Println(header + " Saved |")
	fmt.Println(sep + " ---: |")
	base := rows[0]
	for _, r := range rows {
		line := fmt.Sprintf("| %s | %d | %d | %d |", r.name, r.calls, r.bytes, r.est)
		if exact {
			line += fmt.Sprintf(" %d |", r.exact)
		}
		saved := "-"
		if r.name != base.name {
			saved = fmt.Sprintf("%.0f%%", 100*(1-float64(r.est)/float64(base.est)))
		}
		fmt.Println(line + " " + saved + " |")
	}
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

func run(dir string, name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "PAGER=cat", "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out) + err.Error()
	}
	return string(out)
}

// ghPR mirrors `gh pr list --json` field names.
type ghPR struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	HeadRefName    string `json:"headRefName"`
	BaseRefName    string `json:"baseRefName"`
	State          string `json:"state"`
	IsDraft        bool   `json:"isDraft"`
	ReviewDecision string `json:"reviewDecision"`
	Mergeable      string `json:"mergeable"`
	URL            string `json:"url"`
}

const ghFields = "number,title,headRefName,baseRefName,state,isDraft,reviewDecision,mergeable,url"

// simulateGH produces what gh would print for the demo's fixture PRs.
func simulateGH(prs map[string]*model.PR) []step {
	var list []ghPR
	var checks []step
	heads := make([]string, 0, len(prs))
	for h := range prs {
		heads = append(heads, h)
	}
	sort.Strings(heads)
	for _, h := range heads {
		pr := prs[h]
		if pr.State != model.PROpen {
			continue
		}
		list = append(list, ghPR{
			Number: pr.Number, Title: pr.Title, HeadRefName: h, BaseRefName: pr.Base,
			State: strings.ToUpper(pr.State), IsDraft: pr.Draft, ReviewDecision: strings.ToUpper(pr.Review),
			Mergeable: strings.ToUpper(pr.Mergeable), URL: pr.URL,
		})
		var b strings.Builder
		for i, c := range pr.Checks {
			fmt.Fprintf(&b, "%s\t%s\t%ds\t%s/checks?check_run_id=%d\t\n", c.Name, c.Status, 40+i*17, pr.URL, 30000000000+i)
		}
		checks = append(checks, step{name: fmt.Sprintf("gh pr checks %d", pr.Number), output: b.String(), simulated: true})
	}
	data, _ := json.Marshal(list)
	return append([]step{{name: "gh pr list --json " + ghFields, output: string(data) + "\n", simulated: true}}, checks...)
}

// realGH runs the gh commands against the repository's GitHub remote.
func realGH(repo string) []step {
	out := run(repo, "gh", "pr", "list", "--json", ghFields)
	steps := []step{{name: "gh pr list --json " + ghFields, output: out}}
	var list []ghPR
	if json.Unmarshal([]byte(out), &list) != nil {
		return steps
	}
	for _, pr := range list {
		steps = append(steps, step{name: fmt.Sprintf("gh pr checks %d", pr.Number), output: run(repo, "gh", "pr", "checks", fmt.Sprint(pr.Number))})
	}
	return steps
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "tokenbench:", err)
		os.Exit(1)
	}
}

// Package github loads pull request data for branches.
//
// Authentication reuses the GitHub CLI login (or GH_TOKEN / GITHUB_TOKEN), so
// vb never asks for a token. Pull requests for all branches are fetched with a
// few batched GraphQL queries and cached with a short TTL.
package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"

	"github.com/j-a-man/visual-branches/internal/cache"
	"github.com/j-a-man/visual-branches/internal/model"
)

// ParseRemote extracts host, owner, and repository from a remote URL.
// It understands https, ssh, scp-like, and git protocol URLs.
func ParseRemote(raw string) (host, owner, repo string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", false
	}
	var path string
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", "", false
		}
		host = u.Hostname()
		path = u.Path
	case strings.Contains(raw, ":"):
		// scp-like: [user@]host:owner/repo.git
		hostPart, p, _ := strings.Cut(raw, ":")
		if i := strings.LastIndex(hostPart, "@"); i >= 0 {
			hostPart = hostPart[i+1:]
		}
		host, path = hostPart, p
	default:
		return "", "", "", false
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[len(parts)-2] == "" || parts[len(parts)-1] == "" {
		return "", "", "", false
	}
	owner, repo = parts[len(parts)-2], parts[len(parts)-1]
	host = strings.ToLower(host)
	switch host {
	case "ssh.github.com", "www.github.com":
		host = "github.com"
	}
	return host, owner, repo, true
}

// IsGitHubHost reports whether host looks like GitHub or a configured
// GitHub Enterprise host.
func IsGitHubHost(host, configured string) bool {
	if host == "" {
		return false
	}
	if configured != "" {
		return strings.EqualFold(host, configured)
	}
	if host == "github.com" || strings.HasSuffix(host, ".ghe.com") {
		return true
	}
	// A host gh is logged in to is GitHub Enterprise Server.
	if tok, _ := auth.TokenForHost(host); tok != "" {
		return true
	}
	return false
}

// Fetcher loads and caches PR data for one repository.
type Fetcher struct {
	Host    string
	Owner   string
	Repo    string
	Store   *cache.Store
	TTL     time.Duration
	Offline bool
	// Client overrides the GraphQL client, for tests.
	Client graphQL
}

type graphQL interface {
	DoWithContext(ctx context.Context, query string, variables map[string]interface{}, response interface{}) error
}

type cacheEntry struct {
	FetchedAt time.Time `json:"fetchedAt"`
	PR        *model.PR `json:"pr,omitempty"`
}

type cacheFile struct {
	Repo    string                `json:"repo"`
	Entries map[string]cacheEntry `json:"entries"`
}

const cacheName = "github.json"

// PRs returns the most relevant pull request for each head branch name:
// the open PR if there is one, otherwise the most recent merged or closed PR.
func (f *Fetcher) PRs(ctx context.Context, heads []string) (map[string]*model.PR, model.GitHubStatus) {
	if path := os.Getenv("VB_GITHUB_FIXTURE"); path != "" {
		return fixturePRs(path, heads)
	}
	repoKey := f.Host + "/" + f.Owner + "/" + f.Repo
	var cf cacheFile
	if f.Store != nil {
		f.Store.Load(cacheName, &cf)
	}
	if cf.Repo != repoKey || cf.Entries == nil {
		cf = cacheFile{Repo: repoKey, Entries: map[string]cacheEntry{}}
	}
	now := time.Now()
	result := map[string]*model.PR{}
	var stale []string
	var oldest time.Time
	for _, h := range heads {
		e, ok := cf.Entries[h]
		if ok && (f.Offline || now.Sub(e.FetchedAt) < f.TTL) {
			if e.PR != nil {
				result[h] = e.PR
			}
			if oldest.IsZero() || e.FetchedAt.Before(oldest) {
				oldest = e.FetchedAt
			}
			continue
		}
		stale = append(stale, h)
	}
	cachedOnly := func(status, msg string) (map[string]*model.PR, model.GitHubStatus) {
		for _, h := range stale {
			if e, ok := cf.Entries[h]; ok && e.PR != nil {
				result[h] = e.PR
				if oldest.IsZero() || e.FetchedAt.Before(oldest) {
					oldest = e.FetchedAt
				}
			}
		}
		st := model.GitHubStatus{Status: status, Message: msg}
		if !oldest.IsZero() {
			t := oldest
			st.FetchedAt = &t
		}
		return result, st
	}
	if len(stale) == 0 {
		st := model.GitHubStatus{Status: model.GitHubOK}
		if f.Offline {
			st.Status = model.GitHubCached
		}
		if !oldest.IsZero() {
			t := oldest
			st.FetchedAt = &t
		}
		return result, st
	}
	if f.Offline {
		return cachedOnly(model.GitHubCached, "offline, showing cached pull requests")
	}
	client := f.Client
	if client == nil {
		token, _ := auth.TokenForHost(f.Host)
		if token == "" {
			return cachedOnly(model.GitHubNoAuth, "run `gh auth login` to show pull requests")
		}
		c, err := api.NewGraphQLClient(api.ClientOptions{
			Host:         f.Host,
			AuthToken:    token,
			Timeout:      15 * time.Second,
			LogIgnoreEnv: os.Getenv("VB_DEBUG") == "",
			Headers:      map[string]string{"User-Agent": "visual-branches"},
		})
		if err != nil {
			return cachedOnly(model.GitHubError, err.Error())
		}
		client = c
	}

	fetched, err := f.fetch(ctx, client, stale)
	if err != nil {
		return cachedOnly(model.GitHubError, describeError(err))
	}
	fetchedAt := time.Now()
	for _, h := range stale {
		pr := fetched[h]
		cf.Entries[h] = cacheEntry{FetchedAt: fetchedAt, PR: pr}
		if pr != nil {
			result[h] = pr
		}
	}
	// Drop cache entries for branches that no longer exist.
	keep := map[string]bool{}
	for _, h := range heads {
		keep[h] = true
	}
	for h := range cf.Entries {
		if !keep[h] {
			delete(cf.Entries, h)
		}
	}
	if f.Store != nil {
		f.Store.Save(cacheName, cf)
	}
	if oldest.IsZero() {
		oldest = fetchedAt
	}
	t := oldest
	return result, model.GitHubStatus{Status: model.GitHubOK, FetchedAt: &t}
}

func describeError(err error) string {
	var herr *api.HTTPError
	if errors.As(err, &herr) {
		switch herr.StatusCode {
		case http.StatusUnauthorized:
			return "GitHub rejected the token; run `gh auth login`"
		case http.StatusForbidden:
			return "GitHub API access denied or rate limited"
		case http.StatusNotFound:
			return "repository not found on GitHub or no access"
		}
	}
	var gerr *api.GraphQLError
	if errors.As(err, &gerr) {
		for _, e := range gerr.Errors {
			if e.Type == "NOT_FOUND" {
				return "repository not found on GitHub or no access"
			}
		}
	}
	msg := err.Error()
	if strings.Contains(msg, "no such host") || strings.Contains(msg, "dial tcp") || strings.Contains(msg, "timeout") {
		return "GitHub unreachable, showing cached pull requests"
	}
	return msg
}

const prFields = `number title url state isDraft baseRefName headRefName headRefOid
reviewDecision mergeable additions deletions changedFiles updatedAt mergedAt
author { login } headRepositoryOwner { login }
commits(last: 1) { nodes { commit { statusCheckRollup { state
  contexts(first: 60) { nodes { __typename
    ... on CheckRun { name conclusion status detailsUrl }
    ... on StatusContext { context state targetUrl } } } } } } }`

const batchSize = 25

type prNode struct {
	Number              int        `json:"number"`
	Title               string     `json:"title"`
	URL                 string     `json:"url"`
	State               string     `json:"state"`
	IsDraft             bool       `json:"isDraft"`
	BaseRefName         string     `json:"baseRefName"`
	HeadRefName         string     `json:"headRefName"`
	HeadRefOid          string     `json:"headRefOid"`
	ReviewDecision      string     `json:"reviewDecision"`
	Mergeable           string     `json:"mergeable"`
	Additions           int        `json:"additions"`
	Deletions           int        `json:"deletions"`
	ChangedFiles        int        `json:"changedFiles"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	MergedAt            *time.Time `json:"mergedAt"`
	Author              *login     `json:"author"`
	HeadRepositoryOwner *login     `json:"headRepositoryOwner"`
	Commits             struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State    string `json:"state"`
					Contexts struct {
						Nodes []checkNode `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

type login struct {
	Login string `json:"login"`
}

type checkNode struct {
	Typename   string `json:"__typename"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	Status     string `json:"status"`
	DetailsURL string `json:"detailsUrl"`
	Context    string `json:"context"`
	State      string `json:"state"`
	TargetURL  string `json:"targetUrl"`
}

// Above this many branches, open PRs are listed in bulk and only the most
// recent remaining branches are looked up one by one.
const (
	bulkThreshold = 50
	lookupLimit   = 50
	maxOpenPages  = 5
)

// fetch loads PRs for heads, which callers order most recent first.
func (f *Fetcher) fetch(ctx context.Context, client graphQL, heads []string) (map[string]*model.PR, error) {
	if len(heads) <= bulkThreshold {
		return f.fetchHeads(ctx, client, heads)
	}
	// Two independent requests in parallel: every open PR (listed cheaply,
	// then detailed only for our heads), and the latest PR of the most
	// recent heads, which also finds merged and closed PRs.
	var open, recent map[string]*model.PR
	var openErr, recentErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		open, openErr = f.fetchOpen(ctx, client, heads)
	}()
	go func() {
		defer wg.Done()
		recent, recentErr = f.fetchHeads(ctx, client, heads[:min(lookupLimit, len(heads))])
	}()
	wg.Wait()
	if openErr != nil {
		return nil, openErr
	}
	if recentErr != nil {
		return nil, recentErr
	}
	for h, pr := range open {
		recent[h] = pr
	}
	return recent, nil
}

// fetchOpen lists open PRs with minimal fields, then loads full details for
// the ones whose head is one of heads and lives in this repository.
func (f *Fetcher) fetchOpen(ctx context.Context, client graphQL, heads []string) (map[string]*model.PR, error) {
	want := make(map[string]bool, len(heads))
	for _, h := range heads {
		want[h] = true
	}
	query := `query($owner: String!, $name: String!, $cursor: String) { repository(owner: $owner, name: $name) {
pullRequests(states: OPEN, first: 100, after: $cursor, orderBy: {field: UPDATED_AT, direction: DESC}) {
pageInfo { hasNextPage endCursor } nodes { number headRefName headRepositoryOwner { login } } } } }`
	var numbers []int
	seen := map[string]bool{}
	var cursor *string
	for page := 0; page < maxOpenPages; page++ {
		var resp struct {
			Repository struct {
				PullRequests struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						Number              int    `json:"number"`
						HeadRefName         string `json:"headRefName"`
						HeadRepositoryOwner *login `json:"headRepositoryOwner"`
					} `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		}
		vars := map[string]interface{}{"owner": f.Owner, "name": f.Repo, "cursor": cursor}
		if err := client.DoWithContext(ctx, query, vars, &resp); err != nil {
			return nil, err
		}
		for _, n := range resp.Repository.PullRequests.Nodes {
			if !want[n.HeadRefName] || seen[n.HeadRefName] {
				continue
			}
			if n.HeadRepositoryOwner != nil && !strings.EqualFold(n.HeadRepositoryOwner.Login, f.Owner) {
				continue
			}
			seen[n.HeadRefName] = true
			numbers = append(numbers, n.Number)
		}
		info := resp.Repository.PullRequests.PageInfo
		if !info.HasNextPage {
			break
		}
		c := info.EndCursor
		cursor = &c
	}
	return f.fetchNumbers(ctx, client, numbers)
}

// fetchNumbers loads full details of PRs by number, keyed by head branch.
func (f *Fetcher) fetchNumbers(ctx context.Context, client graphQL, numbers []int) (map[string]*model.PR, error) {
	out := map[string]*model.PR{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	sem := make(chan struct{}, 4)
	for start := 0; start < len(numbers); start += batchSize {
		batch := numbers[start:min(start+batchSize, len(numbers))]
		wg.Add(1)
		sem <- struct{}{}
		go func(batch []int) {
			defer wg.Done()
			defer func() { <-sem }()
			var q strings.Builder
			vars := map[string]interface{}{"owner": f.Owner, "name": f.Repo}
			q.WriteString("query($owner: String!, $name: String!")
			for i := range batch {
				fmt.Fprintf(&q, ", $n%d: Int!", i)
			}
			q.WriteString(") { repository(owner: $owner, name: $name) {\n")
			for i, n := range batch {
				vars[fmt.Sprintf("n%d", i)] = n
				fmt.Fprintf(&q, "p%d: pullRequest(number: $n%d) { %s }\n", i, i, prFields)
			}
			q.WriteString("} }")
			var resp struct {
				Repository map[string]*prNode `json:"repository"`
			}
			err := client.DoWithContext(ctx, q.String(), vars, &resp)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			for _, n := range resp.Repository {
				if n != nil {
					out[n.HeadRefName] = convert(n)
				}
			}
		}(batch)
	}
	wg.Wait()
	return out, firstErr
}

// fetchHeads looks up the latest PR of each head with aliased queries.
func (f *Fetcher) fetchHeads(ctx context.Context, client graphQL, heads []string) (map[string]*model.PR, error) {
	out := map[string]*model.PR{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	sem := make(chan struct{}, 4)
	for start := 0; start < len(heads); start += batchSize {
		batch := heads[start:min(start+batchSize, len(heads))]
		wg.Add(1)
		sem <- struct{}{}
		go func(batch []string) {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := f.fetchBatch(ctx, client, batch)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			for k, v := range res {
				out[k] = v
			}
		}(batch)
	}
	wg.Wait()
	return out, firstErr
}

func (f *Fetcher) fetchBatch(ctx context.Context, client graphQL, heads []string) (map[string]*model.PR, error) {
	var q strings.Builder
	vars := map[string]interface{}{"owner": f.Owner, "name": f.Repo}
	q.WriteString("query($owner: String!, $name: String!")
	for i := range heads {
		fmt.Fprintf(&q, ", $h%d: String!", i)
	}
	q.WriteString(") { repository(owner: $owner, name: $name) {\n")
	for i, h := range heads {
		vars[fmt.Sprintf("h%d", i)] = h
		fmt.Fprintf(&q, "b%d: pullRequests(headRefName: $h%d, first: 5, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { %s } }\n", i, i, prFields)
	}
	q.WriteString("} }")

	var resp struct {
		Repository map[string]struct {
			Nodes []prNode `json:"nodes"`
		} `json:"repository"`
	}
	if err := client.DoWithContext(ctx, q.String(), vars, &resp); err != nil {
		return nil, err
	}
	out := map[string]*model.PR{}
	for i, h := range heads {
		conn, ok := resp.Repository[fmt.Sprintf("b%d", i)]
		if !ok {
			continue
		}
		if pr := choose(conn.Nodes, f.Owner); pr != nil {
			out[h] = pr
		}
	}
	return out, nil
}

// choose picks the open PR from the repository owner, else the newest one.
func choose(nodes []prNode, owner string) *model.PR {
	var best *prNode
	for i := range nodes {
		n := &nodes[i]
		if n.HeadRepositoryOwner != nil && !strings.EqualFold(n.HeadRepositoryOwner.Login, owner) {
			continue // a fork's branch with the same name
		}
		if n.State == "OPEN" {
			best = n
			break
		}
		if best == nil {
			best = n
		}
	}
	if best == nil {
		return nil
	}
	return convert(best)
}

func convert(n *prNode) *model.PR {
	pr := &model.PR{
		Number:       n.Number,
		Title:        n.Title,
		URL:          n.URL,
		State:        strings.ToLower(n.State),
		Draft:        n.IsDraft,
		Base:         n.BaseRefName,
		HeadOID:      n.HeadRefOid,
		Additions:    n.Additions,
		Deletions:    n.Deletions,
		ChangedFiles: n.ChangedFiles,
		UpdatedAt:    n.UpdatedAt,
		MergedAt:     n.MergedAt,
		CI:           model.CINone,
	}
	if n.Author != nil {
		pr.Author = n.Author.Login
	}
	switch n.ReviewDecision {
	case "APPROVED":
		pr.Review = model.ReviewApproved
	case "CHANGES_REQUESTED":
		pr.Review = model.ReviewChanges
	case "REVIEW_REQUIRED":
		pr.Review = model.ReviewRequired
	}
	switch n.Mergeable {
	case "MERGEABLE":
		pr.Mergeable = model.MergeClean
	case "CONFLICTING":
		pr.Mergeable = model.MergeConflicting
	default:
		pr.Mergeable = model.MergeUnknown
	}
	if len(n.Commits.Nodes) > 0 {
		if roll := n.Commits.Nodes[0].Commit.StatusCheckRollup; roll != nil {
			switch roll.State {
			case "SUCCESS":
				pr.CI = model.CIPass
			case "FAILURE", "ERROR":
				pr.CI = model.CIFail
			case "PENDING", "EXPECTED":
				pr.CI = model.CIPending
			}
			seen := map[string]bool{}
			for _, c := range roll.Contexts.Nodes {
				ch := convertCheck(c)
				if ch.Name == "" || seen[ch.Name] {
					continue
				}
				seen[ch.Name] = true
				pr.Checks = append(pr.Checks, ch)
			}
			sort.SliceStable(pr.Checks, func(i, j int) bool {
				return checkRank(pr.Checks[i].Status) < checkRank(pr.Checks[j].Status)
			})
		}
	}
	return pr
}

func convertCheck(c checkNode) model.Check {
	if c.Typename == "StatusContext" {
		ch := model.Check{Name: c.Context, URL: c.TargetURL}
		switch c.State {
		case "SUCCESS":
			ch.Status = model.CIPass
		case "FAILURE", "ERROR":
			ch.Status = model.CIFail
		default:
			ch.Status = model.CIPending
		}
		return ch
	}
	ch := model.Check{Name: c.Name, URL: c.DetailsURL}
	if c.Status != "COMPLETED" {
		ch.Status = model.CIPending
		return ch
	}
	switch c.Conclusion {
	case "SUCCESS", "NEUTRAL":
		ch.Status = model.CIPass
	case "SKIPPED", "STALE":
		ch.Status = "skipped"
	default:
		ch.Status = model.CIFail
	}
	return ch
}

func checkRank(s string) int {
	switch s {
	case model.CIFail:
		return 0
	case model.CIPending:
		return 1
	case model.CIPass:
		return 2
	}
	return 3
}

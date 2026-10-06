// Package demorepo builds a realistic repository for demos, screenshots,
// benchmarks, and end-to-end tests.
//
// The repository has stacked branches, agent worktrees that collide, squash
// and merge-commit merges, stale and unpushed work, and a fixture file with
// pull request data so the GitHub columns render without network access.
package demorepo

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/j-a-man/visual-branches/internal/model"
)

// Result locates the generated repository.
type Result struct {
	// Repo is the main work tree.
	Repo string
	// Fixture is the pull request fixture for VB_GITHUB_FIXTURE.
	Fixture string
}

// Build creates the demo under root, which must not exist yet.
func Build(root string) (res Result, err error) {
	if _, statErr := os.Stat(root); statErr == nil {
		return res, fmt.Errorf("%s already exists; remove it first", root)
	}
	d := &demo{root: root, repo: filepath.Join(root, "webapp"), now: time.Now()}
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(demoError); ok {
				err = e.err
				return
			}
			panic(r)
		}
	}()
	d.build()
	return Result{Repo: d.repo, Fixture: filepath.Join(root, "prs.json")}, nil
}

type demoError struct{ err error }

func check(err error) {
	if err != nil {
		panic(demoError{err})
	}
}

type demo struct {
	root string
	repo string
	now  time.Time
	at   time.Time
}

func (d *demo) ago(dur time.Duration) { d.at = d.now.Add(-dur) }

func (d *demo) git(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	ts := d.at.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+ts, "GIT_COMMITTER_DATE="+ts)
	out, err := cmd.CombinedOutput()
	if err != nil {
		check(fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out))
	}
	return strings.TrimSpace(string(out))
}

func (d *demo) write(dir, name, content string) {
	p := filepath.Join(dir, filepath.FromSlash(name))
	check(os.MkdirAll(filepath.Dir(p), 0o755))
	check(os.WriteFile(p, []byte(content), 0o644))
}

// commit writes files on branch and commits them at the current demo time.
func (d *demo) commit(branch, msg string, files map[string]string, author ...string) {
	if d.git(d.repo, "symbolic-ref", "--short", "HEAD") != branch {
		d.git(d.repo, "switch", "-q", branch)
	}
	for name, content := range files {
		d.write(d.repo, name, content)
	}
	d.git(d.repo, "add", "-A")
	args := []string{"commit", "-q", "--no-verify", "-m", msg}
	if len(author) == 1 {
		args = append(args, "--author", author[0])
	}
	d.git(d.repo, args...)
}

func (d *demo) build() {
	check(os.MkdirAll(d.repo, 0o755))
	origin := filepath.Join(d.root, "origin.git")
	d.ago(60 * 24 * time.Hour)
	d.git(d.root, "init", "-q", "--bare", "-b", "main", origin)
	d.git(d.repo, "init", "-q", "-b", "main")
	for k, v := range map[string]string{"user.name": "Jaylin", "user.email": "jaylin@example.com", "commit.gpgsign": "false", "core.autocrlf": "false"} {
		d.git(d.repo, "config", k, v)
	}
	d.commit("main", "Initial app skeleton", map[string]string{
		"README.md":         "# webapp\n",
		"src/config.ts":     "export const config = {\n  retries: 3,\n  timeoutMs: 5000,\n}\n",
		"src/db/pool.ts":    "export function createPool() {\n  return { size: 10 }\n}\n",
		"src/auth/token.ts": "export function issueToken() {}\n",
		"package.json":      "{ \"name\": \"webapp\" }\n",
	})
	d.git(d.repo, "remote", "add", "origin", origin)
	d.git(d.repo, "push", "-q", "-u", "origin", "main")

	// A stale branch nobody has touched in weeks.
	d.ago(45 * 24 * time.Hour)
	d.git(d.repo, "branch", "feat/billing-v2", "main")
	d.commit("feat/billing-v2", "Sketch billing v2 invoices", map[string]string{"src/billing/invoice.ts": "export const v2 = true\n"})

	// Merged with a merge commit, never deleted.
	d.ago(20 * 24 * time.Hour)
	d.git(d.repo, "branch", "feat/search", "main")
	d.commit("feat/search", "Add search endpoint", map[string]string{"src/search/index.ts": "export function search() {}\n"})
	d.commit("feat/search", "Rank search results", map[string]string{"src/search/rank.ts": "export function rank() {}\n"})
	d.ago(18 * 24 * time.Hour)
	d.git(d.repo, "switch", "-q", "main")
	d.git(d.repo, "merge", "-q", "--no-ff", "--no-edit", "feat/search")

	// Squash-merged on GitHub; the local branch lingers.
	d.ago(9 * 24 * time.Hour)
	d.git(d.repo, "branch", "chore/deps", "main")
	d.commit("chore/deps", "Bump dependencies", map[string]string{"package.json": "{ \"name\": \"webapp\", \"deps\": 2 }\n"})
	d.ago(8 * 24 * time.Hour)
	d.git(d.repo, "switch", "-q", "main")
	d.git(d.repo, "merge", "-q", "--squash", "chore/deps")
	d.git(d.repo, "commit", "-q", "--no-verify", "-m", "Bump dependencies (#131)")

	// A stack: auth API, then auth UI on top.
	d.ago(6 * 24 * time.Hour)
	d.git(d.repo, "branch", "feat/auth-api", "main")
	d.commit("feat/auth-api", "Add session endpoints", map[string]string{"src/auth/session.ts": "export function createSession() {}\n"})
	d.commit("feat/auth-api", "Rotate refresh tokens", map[string]string{"src/auth/token.ts": "export function issueToken() {\n  return rotate()\n}\n"})
	d.ago(5 * 24 * time.Hour)
	d.commit("feat/auth-api", "Rate-limit login attempts", map[string]string{"src/auth/limit.ts": "export const limit = 5\n"})
	d.git(d.repo, "push", "-q", "-u", "origin", "feat/auth-api")
	d.ago(4 * 24 * time.Hour)
	d.git(d.repo, "branch", "feat/auth-ui", "feat/auth-api")
	d.commit("feat/auth-ui", "Login form", map[string]string{"src/ui/login.tsx": "export function Login() {}\n"})
	d.ago(26 * time.Hour)
	d.commit("feat/auth-ui", "Session expiry banner", map[string]string{"src/ui/banner.tsx": "export function Banner() {}\n"})
	d.git(d.repo, "push", "-q", "-u", "origin", "feat/auth-ui")
	// Review feedback lands on the parent after the child was stacked.
	d.ago(20 * time.Hour)
	d.commit("feat/auth-api", "Address review: stricter token checks", map[string]string{"src/auth/token.ts": "export function issueToken() {\n  return rotate({ strict: true })\n}\n"})
	d.git(d.repo, "push", "-q", "origin", "feat/auth-api")

	// Main moves on, including a config change.
	d.ago(3 * 24 * time.Hour)
	d.commit("main", "Tune timeouts", map[string]string{"src/config.ts": "export const config = {\n  retries: 5,\n  timeoutMs: 8000,\n}\n"})
	d.git(d.repo, "push", "-q", "origin", "main")

	// A PR that now conflicts with main (edits the same config lines).
	d.ago(4 * 24 * time.Hour)
	d.git(d.repo, "branch", "feat/payments", "main~1")
	d.commit("feat/payments", "Payments retry policy", map[string]string{"src/config.ts": "export const config = {\n  retries: 10,\n  timeoutMs: 3000,\n}\n", "src/payments/retry.ts": "export const retry = 10\n"})
	d.git(d.repo, "push", "-q", "-u", "origin", "feat/payments")

	// A hotfix in its own worktree, with uncommitted work.
	d.ago(5 * time.Hour)
	d.git(d.repo, "branch", "fix/login-race", "main")
	d.commit("fix/login-race", "Serialize concurrent logins", map[string]string{"src/auth/lock.ts": "export const lock = new Mutex()\n"})
	d.git(d.repo, "push", "-q", "-u", "origin", "fix/login-race")
	d.git(d.repo, "switch", "-q", "main")
	fixWT := filepath.Join(d.root, "webapp-fix")
	d.git(d.repo, "worktree", "add", "-q", fixWT, "fix/login-race")
	d.write(fixWT, "src/auth/lock.test.ts", "test('lock', () => {})\n")
	d.write(fixWT, "src/auth/lock.ts", "export const lock = new Mutex({ fair: true })\n")

	// Two coding agents working in parallel worktrees on the same file.
	d.ago(50 * time.Minute)
	d.git(d.repo, "branch", "claude/refactor-db-pool", "main")
	d.commit("claude/refactor-db-pool", "Extract pool config", map[string]string{"src/db/pool.ts": "export function createPool(size = 20) {\n  return { size }\n}\n"},
		"Jaylin <jaylin@example.com>")
	d.ago(35 * time.Minute)
	d.commit("claude/refactor-db-pool", "Add pool metrics", map[string]string{"src/db/metrics.ts": "export const metrics = {}\n"})
	d.git(d.repo, "switch", "-q", "main")
	agentWT := filepath.Join(d.repo, ".claude", "worktrees", "refactor-db-pool")
	d.git(d.repo, "worktree", "add", "-q", agentWT, "claude/refactor-db-pool")
	d.write(d.repo, ".git/info/exclude", ".claude/\n")

	d.ago(25 * time.Minute)
	d.git(d.repo, "branch", "codex/cache-layer", "main")
	d.commit("codex/cache-layer", "Cache hot queries", map[string]string{"src/db/pool.ts": "export function createPool() {\n  return { size: 10, cache: true }\n}\n", "src/db/cache.ts": "export const cache = new Map()\n"})
	d.git(d.repo, "switch", "-q", "main")
	codexWT := filepath.Join(d.root, "webapp-codex")
	d.git(d.repo, "worktree", "add", "-q", codexWT, "codex/cache-layer")

	// Local experiment that was never pushed.
	d.ago(2 * time.Hour)
	d.git(d.repo, "branch", "spike/graphql", "main")
	d.commit("spike/graphql", "Try a GraphQL gateway", map[string]string{"src/gateway/schema.graphql": "type Query { ok: Boolean }\n"})

	// Docs branch with a local commit not pushed yet.
	d.ago(30 * time.Hour)
	d.git(d.repo, "branch", "docs/onboarding", "main")
	d.commit("docs/onboarding", "Onboarding guide", map[string]string{"docs/onboarding.md": "# Onboarding\n"})
	d.git(d.repo, "push", "-q", "-u", "origin", "docs/onboarding")
	d.ago(3 * time.Hour)
	d.commit("docs/onboarding", "Add local setup steps", map[string]string{"docs/onboarding.md": "# Onboarding\n\n1. Install\n"})

	d.ago(0)
	d.git(d.repo, "switch", "-q", "feat/auth-ui")
	d.git(d.repo, "fetch", "-q", "origin")
	// Point origin at a GitHub URL so vb treats it as a GitHub repository.
	// Pushing is done; vb never fetches.
	d.git(d.repo, "remote", "set-url", "origin", "https://github.com/acme/webapp.git")

	d.writeFixture()
}

func (d *demo) tip(ref string) string { return d.git(d.repo, "rev-parse", ref) }

func (d *demo) writeFixture() {
	t := func(ago time.Duration) time.Time { return d.now.Add(-ago) }
	merged := t(8 * 24 * time.Hour)
	prs := map[string]*model.PR{
		"feat/auth-api": {Number: 142, Title: "Session endpoints with token rotation", State: model.PROpen, Base: "main",
			HeadOID: d.tip("feat/auth-api"), Review: model.ReviewApproved, CI: model.CIPass, Mergeable: model.MergeClean,
			Additions: 214, Deletions: 38, ChangedFiles: 6, Author: "jaylin", UpdatedAt: t(20 * time.Hour),
			URL:    "https://github.com/acme/webapp/pull/142",
			Checks: []model.Check{{Name: "build", Status: model.CIPass}, {Name: "test", Status: model.CIPass}, {Name: "lint", Status: model.CIPass}}},
		"feat/auth-ui": {Number: 143, Title: "Login form and session banner", State: model.PROpen, Base: "feat/auth-api",
			HeadOID: d.tip("feat/auth-ui"), Review: model.ReviewRequired, CI: model.CIFail, Mergeable: model.MergeClean,
			Additions: 188, Deletions: 12, ChangedFiles: 4, Author: "jaylin", UpdatedAt: t(26 * time.Hour),
			URL:    "https://github.com/acme/webapp/pull/143",
			Checks: []model.Check{{Name: "lint", Status: model.CIFail}, {Name: "build", Status: model.CIPass}, {Name: "test", Status: model.CIPass}}},
		"feat/payments": {Number: 138, Title: "Payments retry policy", State: model.PROpen, Base: "main",
			HeadOID: d.tip("feat/payments"), Review: model.ReviewChanges, CI: model.CIPass, Mergeable: model.MergeConflicting,
			Additions: 64, Deletions: 9, ChangedFiles: 2, Author: "sam", UpdatedAt: t(2 * 24 * time.Hour),
			URL:    "https://github.com/acme/webapp/pull/138",
			Checks: []model.Check{{Name: "build", Status: model.CIPass}, {Name: "test", Status: model.CIPass}}},
		"fix/login-race": {Number: 150, Title: "Serialize concurrent logins", State: model.PROpen, Draft: true, Base: "main",
			HeadOID: d.tip("fix/login-race"), CI: model.CIPending, Mergeable: model.MergeClean,
			Additions: 22, Deletions: 3, ChangedFiles: 1, Author: "jaylin", UpdatedAt: t(5 * time.Hour),
			URL:    "https://github.com/acme/webapp/pull/150",
			Checks: []model.Check{{Name: "test", Status: model.CIPending}, {Name: "build", Status: model.CIPass}}},
		"docs/onboarding": {Number: 147, Title: "Onboarding guide", State: model.PROpen, Base: "main",
			HeadOID: d.tip("origin/docs/onboarding"), Review: model.ReviewApproved, CI: model.CIPass, Mergeable: model.MergeClean,
			Additions: 40, Deletions: 0, ChangedFiles: 1, Author: "jaylin", UpdatedAt: t(30 * time.Hour),
			URL:    "https://github.com/acme/webapp/pull/147",
			Checks: []model.Check{{Name: "docs", Status: model.CIPass}}},
		"chore/deps": {Number: 131, Title: "Bump dependencies", State: model.PRMerged, Base: "main",
			HeadOID: d.tip("chore/deps"), CI: model.CIPass, Author: "renovate", UpdatedAt: merged, MergedAt: &merged,
			URL: "https://github.com/acme/webapp/pull/131"},
	}
	data, err := json.MarshalIndent(prs, "", "  ")
	check(err)
	check(os.WriteFile(filepath.Join(d.root, "prs.json"), data, 0o644))
}

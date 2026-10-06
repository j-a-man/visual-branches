// Package testrepo builds real git repositories for tests.
//
// Every repository is isolated from the user's git and vb configuration and
// uses deterministic commit dates, so tests and golden files are stable.
package testrepo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Epoch is the timestamp of the first commit in every test repository. It is
// a week ago, so test branches are recent: stale-branch rules (which depend
// on the real clock in commands like the MCP server) do not kick in.
var Epoch = time.Now().UTC().Add(-7 * 24 * time.Hour).Truncate(time.Hour)

// Repo is a scratch repository.
type Repo struct {
	T   testing.TB
	Dir string
	// Remote is the path of the bare "origin" repository, when created.
	Remote string
	n      int
}

// Isolate points git and vb at empty configuration and a private cache.
// Call it before creating repositories; it uses t.Setenv.
func Isolate(t testing.TB) string {
	t.Helper()
	home := t.TempDir()
	global := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(global, []byte("[init]\n\tdefaultBranch = main\n[advice]\n\tdetachedHead = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if tt, ok := t.(interface{ Setenv(string, string) }); ok {
		tt.Setenv("GIT_CONFIG_GLOBAL", global)
		tt.Setenv("GIT_CONFIG_NOSYSTEM", "1")
		tt.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
		tt.Setenv("VB_CACHE_DIR", filepath.Join(home, "cache"))
		tt.Setenv("VB_CONFIG", "")
		tt.Setenv("VB_THEME", "")
		tt.Setenv("VB_GITHUB", "")
		tt.Setenv("GH_TOKEN", "")
		tt.Setenv("GITHUB_TOKEN", "")
		tt.Setenv("NO_COLOR", "")
	}
	return home
}

// New creates a repository with one commit on main.
func New(t testing.TB) *Repo {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Repo{T: t, Dir: dir}
	r.Git("init", "-q", "-b", "main")
	r.Git("config", "user.name", "Test User")
	r.Git("config", "user.email", "test@example.com")
	r.Git("config", "commit.gpgsign", "false")
	r.Git("config", "core.autocrlf", "false")
	r.Write("README.md", "# test\n")
	r.Git("add", "-A")
	r.commit("initial commit")
	return r
}

func (r *Repo) env() []string {
	r.n++
	ts := Epoch.Add(time.Duration(r.n) * time.Hour).Format(time.RFC3339)
	return append(os.Environ(), "GIT_AUTHOR_DATE="+ts, "GIT_COMMITTER_DATE="+ts)
}

// Git runs git in the repository and returns trimmed stdout.
func (r *Repo) Git(args ...string) string {
	r.T.Helper()
	return r.GitIn(r.Dir, args...)
}

// GitIn runs git in dir.
func (r *Repo) GitIn(dir string, args ...string) string {
	r.T.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = r.env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.T.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Write writes a file relative to the repository root.
func (r *Repo) Write(name, content string) {
	r.T.Helper()
	r.WriteIn(r.Dir, name, content)
}

// WriteIn writes a file relative to dir.
func (r *Repo) WriteIn(dir, name, content string) {
	r.T.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.T.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.T.Fatal(err)
	}
}

func (r *Repo) commit(msg string, extra ...string) string {
	args := append([]string{"commit", "-q", "--no-verify", "-m", msg}, extra...)
	r.Git(args...)
	return r.Git("rev-parse", "HEAD")
}

// Switch checks out a branch.
func (r *Repo) Switch(branch string) {
	r.T.Helper()
	r.Git("switch", "-q", branch)
}

// Branch creates branch at start without switching to it.
func (r *Repo) Branch(name, start string) {
	r.T.Helper()
	r.Git("branch", name, start)
}

// Commit switches to branch, writes file, and commits it.
func (r *Repo) Commit(branch, file, content, msg string, trailers ...string) string {
	r.T.Helper()
	r.Switch(branch)
	r.Write(file, content)
	r.Git("add", "-A")
	if len(trailers) > 0 {
		msg += "\n\n" + strings.Join(trailers, "\n")
	}
	return r.commit(msg)
}

// Commits adds n commits to branch, each touching its own file.
func (r *Repo) Commits(branch string, n int) {
	r.T.Helper()
	for i := 0; i < n; i++ {
		file := fmt.Sprintf("%s/file-%d.txt", strings.ReplaceAll(branch, "/", "-"), r.n)
		r.Commit(branch, file, fmt.Sprintf("%s %d\n", branch, i), fmt.Sprintf("%s change %d", branch, i))
	}
}

// Merge merges branch into target with a merge commit.
func (r *Repo) Merge(branch, target string) {
	r.T.Helper()
	r.Switch(target)
	r.Git("merge", "-q", "--no-ff", "--no-edit", branch)
}

// SquashMerge squashes branch into target as one commit.
func (r *Repo) SquashMerge(branch, target string) {
	r.T.Helper()
	r.Switch(target)
	r.Git("merge", "-q", "--squash", branch)
	r.commit("squash " + branch)
}

// AddRemote creates a bare origin, pushes main, and sets origin/HEAD.
func (r *Repo) AddRemote() {
	r.T.Helper()
	r.Remote = filepath.Join(filepath.Dir(r.Dir), "origin.git")
	r.GitIn(filepath.Dir(r.Dir), "init", "-q", "--bare", "-b", "main", r.Remote)
	r.Git("remote", "add", "origin", r.Remote)
	r.Git("push", "-q", "-u", "origin", "main")
	r.Git("remote", "set-head", "origin", "main")
}

// Push pushes a branch and sets its upstream.
func (r *Repo) Push(branch string) {
	r.T.Helper()
	r.Git("push", "-q", "-u", "origin", branch)
}

// Worktree adds a worktree for an existing branch and returns its path.
func (r *Repo) Worktree(name, branch string) string {
	r.T.Helper()
	p := filepath.Join(filepath.Dir(r.Dir), name)
	r.Git("worktree", "add", "-q", p, branch)
	return p
}

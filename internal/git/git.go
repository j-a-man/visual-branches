// Package git runs git commands and parses their output.
//
// Every command runs with optional locks disabled and colors forced off so
// that vb never contends with the user's own git processes and never writes
// to the index while reading status.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Error describes a failed git invocation.
type Error struct {
	Args     []string
	ExitCode int
	Stderr   string
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", e.ExitCode)
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

// debug logs every git invocation with its duration to stderr (VB_DEBUG=1).
var debug = os.Getenv("VB_DEBUG") != ""

// IsMissingObject reports whether err comes from an object that is not
// available locally, as happens in partial clones.
func IsMissingObject(err error) bool {
	var gerr *Error
	if !errors.As(err, &gerr) {
		return false
	}
	s := gerr.Stderr
	return strings.Contains(s, "lazy fetching disabled") || strings.Contains(s, "missing blob object") ||
		strings.Contains(s, "unable to read") || strings.Contains(s, "bad object") || strings.Contains(s, "could not read")
}

// ErrNotRepository is returned when the directory is not inside a git work tree.
var ErrNotRepository = errors.New("not a git repository (or any parent directory)")

// Repo is a handle to a git repository.
type Repo struct {
	// Root is the top-level directory of the current work tree.
	Root string
	// GitDir is the git directory of the current work tree.
	GitDir string
	// CommonDir is the git directory shared by all work trees.
	CommonDir string
	// Version is the installed git version.
	Version Version

	bin string

	mu        sync.Mutex
	quietTree *bool
}

// Open discovers the repository containing dir.
func Open(ctx context.Context, dir string) (*Repo, error) {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	r := &Repo{Root: abs, bin: "git"}
	if env := os.Getenv("VB_GIT"); env != "" {
		r.bin = env
	}
	// Ask for the version concurrently; process start-up dominates both calls.
	type versionResult struct {
		out string
		err error
	}
	vch := make(chan versionResult, 1)
	go func() {
		out, err := r.Run(ctx, "version")
		vch <- versionResult{out, err}
	}()
	out, err := r.Run(ctx, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-dir", "--git-common-dir")
	if err != nil {
		var gerr *Error
		if errors.As(err, &gerr) && strings.Contains(gerr.Stderr, "not a git repository") {
			return nil, ErrNotRepository
		}
		if errors.As(err, &gerr) && strings.Contains(gerr.Stderr, "this operation must be run in a work tree") {
			return nil, fmt.Errorf("bare repositories are not supported yet")
		}
		return nil, err
	}
	lines := splitLines(out)
	if len(lines) < 3 {
		return nil, fmt.Errorf("unexpected rev-parse output: %q", out)
	}
	r.Root = filepath.Clean(lines[0])
	r.GitDir = filepath.Clean(lines[1])
	r.CommonDir = filepath.Clean(lines[2])
	v := <-vch
	if v.err != nil {
		return nil, v.err
	}
	r.Version = ParseVersion(v.out)
	return r, nil
}

// Run executes git with args in the repository root and returns stdout.
func (r *Repo) Run(ctx context.Context, args ...string) (string, error) {
	out, _, err := r.run(ctx, r.Root, nil, args...)
	return out, err
}

// RunIn executes git with args in a specific directory.
func (r *Repo) RunIn(ctx context.Context, dir string, args ...string) (string, error) {
	out, _, err := r.run(ctx, dir, nil, args...)
	return out, err
}

// RunInput executes git with args, feeding stdin.
func (r *Repo) RunInput(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	out, _, err := r.run(ctx, r.Root, stdin, args...)
	return out, err
}

// RunCode executes git and returns stdout and the exit code without treating
// a non-zero exit code as an error. It still returns an error when git could
// not be started or was killed.
func (r *Repo) RunCode(ctx context.Context, args ...string) (string, int, error) {
	out, code, err := r.run(ctx, r.Root, nil, args...)
	var gerr *Error
	if errors.As(err, &gerr) {
		return out, gerr.ExitCode, nil
	}
	return out, code, err
}

// Pipe runs `git <first...> | git <second...>` and returns the output of the
// second command.
func (r *Repo) Pipe(ctx context.Context, first []string, second []string) (string, error) {
	out, err := r.Run(ctx, first...)
	if err != nil {
		return "", err
	}
	if out == "" {
		return "", nil
	}
	return r.RunInput(ctx, strings.NewReader(out), second...)
}

func (r *Repo) run(ctx context.Context, dir string, stdin io.Reader, args ...string) (string, int, error) {
	full := append([]string{"-c", "color.ui=never", "-c", "core.quotepath=false", "--no-pager"}, args...)
	cmd := exec.CommandContext(ctx, r.bin, full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		// vb never touches the network through git. In partial clones,
		// commands that need missing objects fail instead of downloading
		// them, and the analysis that needed them is skipped.
		"GIT_NO_LAZY_FETCH=1",
		"LC_ALL=C",
		"LANG=C",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = stdin
	if debug {
		start := time.Now()
		defer func() {
			fmt.Fprintf(os.Stderr, "vb: %6.1fms git %s\n", float64(time.Since(start).Microseconds())/1000, strings.Join(args, " "))
		}()
	}
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.String(), exitErr.ExitCode(), &Error{Args: args, ExitCode: exitErr.ExitCode(), Stderr: stderr.String()}
		}
		if errors.Is(err, exec.ErrNotFound) {
			return "", -1, fmt.Errorf("git executable not found in PATH")
		}
		return "", -1, err
	}
	return stdout.String(), 0, nil
}

// Version is a parsed git version.
type Version struct {
	Major, Minor, Patch int
	Raw                 string
}

// ParseVersion parses `git version` output such as "git version 2.53.0.windows.1".
func ParseVersion(s string) Version {
	s = strings.TrimSpace(s)
	v := Version{Raw: s}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return v
	}
	num := fields[len(fields)-1]
	for _, f := range fields {
		if len(f) > 0 && f[0] >= '0' && f[0] <= '9' {
			num = f
			break
		}
	}
	parts := strings.Split(num, ".")
	nums := make([]int, 3)
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			break
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v
}

// AtLeast reports whether v >= major.minor.
func (v Version) AtLeast(major, minor int) bool {
	if v.Major != major {
		return v.Major > major
	}
	return v.Minor >= minor
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Feature gates.
func (r *Repo) HasAheadBehind() bool { return r.Version.AtLeast(2, 41) }
func (r *Repo) HasMergeTree() bool   { return r.Version.AtLeast(2, 38) }

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// SplitLines splits command output into lines, dropping the trailing newline.
func SplitLines(s string) []string { return splitLines(s) }

// Package source fetches an app's repository at an exact commit into a
// fresh per-operation workspace, and proves that the commit belongs to the
// app's tracked branch (ARCHITECTURE §5 step 3, ADR-0004, invariant 7).
//
// It drives the git CLI without a shell. Every input is validated before git
// sees it, git's own configuration files are ignored, only one transport is
// allowed, and a credential travels in the environment, never in argv (which
// other users can read through /proc) or in the URL.
package source

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hami9/shipyard/internal/app"
)

var (
	// ErrNotOnBranch means the commit is not an ancestor of the tracked
	// branch's head. Commits from forks are reachable through the upstream
	// repository, so this check is what keeps them out [GH-FORKS].
	ErrNotOnBranch = errors.New("commit is not on the tracked branch")
	// ErrBranchNotFound means the remote has no such branch.
	ErrBranchNotFound = errors.New("branch not found in the repository")
)

var (
	opIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	repoRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)
)

// Fetcher checks out repositories under Root, one directory per operation.
type Fetcher struct {
	Root    string // workspace root, e.g. /var/lib/shipyard/work
	BaseURL string // "https://github.com"; tests use a loopback http server
	Git     string // git binary; "git" when empty
}

// Request says what to fetch. Token, when set, authenticates the fetch; it
// is never logged, stored in the workspace, or passed on the command line.
type Request struct {
	OperationID string
	Repo        string // owner/name
	Branch      string
	Ref         string // full commit SHA; empty means the branch head
	Token       string
}

// Checkout is a verified working tree.
type Checkout struct {
	Dir        string
	SHA        string // the commit checked out
	BranchHead string // the tracked branch's head at fetch time
}

// Workspace returns the operation's directory under Root.
func (f *Fetcher) Workspace(opID string) (string, error) {
	if !opIDRE.MatchString(opID) {
		return "", fmt.Errorf("invalid operation id %q", opID)
	}
	return filepath.Join(f.Root, "op-"+opID), nil
}

// Fetch clones the tracked branch into a fresh workspace, resolves the
// commit, checks ancestry, and checks it out. A retry of the same operation
// starts over in an empty directory.
func (f *Fetcher) Fetch(ctx context.Context, req Request) (Checkout, error) {
	if err := validate(req); err != nil {
		return Checkout{}, err
	}
	dir, err := f.Workspace(req.OperationID)
	if err != nil {
		return Checkout{}, err
	}
	remote, scheme, err := f.remoteURL(req.Repo)
	if err != nil {
		return Checkout{}, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return Checkout{}, fmt.Errorf("reset workspace: %w", err)
	}
	if err := os.MkdirAll(f.Root, 0o700); err != nil {
		return Checkout{}, err
	}
	g := &gitRunner{bin: f.gitBin(), scheme: scheme, token: req.Token}

	// A blobless clone has every commit of the branch, so ancestry is exact,
	// but downloads file contents only for the commit checked out.
	if _, err := g.run(ctx, "", "clone", "--quiet", "--no-checkout", "--filter=blob:none",
		"--single-branch", "--no-tags", "--branch", req.Branch, "--", remote, dir); err != nil {
		if strings.Contains(err.Error(), "Remote branch") && strings.Contains(err.Error(), "not found") {
			return Checkout{}, fmt.Errorf("%w: %s", ErrBranchNotFound, req.Branch)
		}
		return Checkout{}, fmt.Errorf("clone %s: %w", req.Repo, err)
	}
	trackingRef := "refs/remotes/origin/" + req.Branch
	head, err := g.run(ctx, dir, "rev-parse", "--verify", "--end-of-options", trackingRef+"^{commit}")
	if err != nil {
		return Checkout{}, fmt.Errorf("resolve branch head: %w", err)
	}
	sha := req.Ref
	if sha == "" {
		sha = head
	}
	// A commit missing from the branch's full history cannot be on the
	// branch. Lazy fetching is off for both checks, so a commit from another
	// branch or a fork is refused without being downloaded.
	if _, err := g.local(ctx, dir, "cat-file", "-e", "--end-of-options", sha+"^{commit}"); err != nil {
		return Checkout{}, fmt.Errorf("%w: %s is not in the history of %s", ErrNotOnBranch, sha, req.Branch)
	}
	if _, err := g.local(ctx, dir, "merge-base", "--is-ancestor", sha, trackingRef); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return Checkout{}, fmt.Errorf("%w: %s is not an ancestor of %s", ErrNotOnBranch, sha, req.Branch)
		}
		return Checkout{}, fmt.Errorf("ancestry check: %w", err)
	}
	if _, err := g.run(ctx, dir, "-c", "advice.detachedHead=false", "checkout", "--quiet", "--detach", sha); err != nil {
		return Checkout{}, fmt.Errorf("checkout %s: %w", sha, err)
	}
	got, err := g.run(ctx, dir, "rev-parse", "HEAD")
	if err != nil || got != sha {
		return Checkout{}, fmt.Errorf("checkout verification failed: HEAD is %q, want %s (%v)", got, sha, err)
	}
	return Checkout{Dir: dir, SHA: sha, BranchHead: head}, nil
}

// ErrEscapes means a path leaves the checkout, directly or through a symlink.
var ErrEscapes = errors.New("path escapes the repository checkout")

// Path resolves rel (e.g. the Dockerfile path) inside the checkout, following
// symlinks, and refuses anything that ends up outside it (ADR-0004). A
// repository can contain a symlink to /etc; the textual checks in
// internal/app cannot see that.
func (c Checkout) Path(rel string) (string, error) {
	if err := app.CheckRepoPath(rel, true); err != nil {
		return "", fmt.Errorf("%w: %v", ErrEscapes, err)
	}
	root, err := filepath.EvalSymlinks(c.Dir)
	if err != nil {
		return "", err
	}
	p, err := filepath.EvalSymlinks(filepath.Join(root, rel))
	if err != nil {
		return "", fmt.Errorf("%s: %w", rel, err)
	}
	if p != root && !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s resolves outside it", ErrEscapes, rel)
	}
	return p, nil
}

// Cleanup removes an operation's workspace.
func (f *Fetcher) Cleanup(opID string) error {
	dir, err := f.Workspace(opID)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func validate(req Request) error {
	var fe app.FieldErrors
	if !opIDRE.MatchString(req.OperationID) {
		fe = append(fe, app.FieldError{Field: "operation", Detail: "must be a UUID"})
	}
	if !repoRE.MatchString(req.Repo) || strings.Contains(req.Repo, "..") {
		fe = append(fe, app.FieldError{Field: "repo", Detail: "must be owner/name"})
	}
	if err := app.CheckBranch(req.Branch); err != nil {
		fe = append(fe, app.FieldError{Field: "branch", Detail: err.Error()})
	}
	if req.Ref != "" {
		if err := app.CheckCommitSHA(req.Ref); err != nil {
			fe = append(fe, app.FieldError{Field: "ref", Detail: err.Error()})
		}
	}
	return fe.Err()
}

// remoteURL builds the clone URL and returns the one transport to allow.
// Plain http is accepted only on loopback, for tests.
func (f *Fetcher) remoteURL(repo string) (string, string, error) {
	base := f.BaseURL
	if base == "" {
		base = "https://github.com"
	}
	u, err := url.Parse(base)
	if err != nil || u.User != nil {
		return "", "", errors.New("invalid source base URL (credentials never go in the URL)")
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1" || u.Hostname() == "localhost"):
	default:
		return "", "", fmt.Errorf("source base URL must be https (got %q)", u.Scheme)
	}
	return strings.TrimRight(base, "/") + "/" + repo + ".git", u.Scheme, nil
}

func (f *Fetcher) gitBin() string {
	if f.Git != "" {
		return f.Git
	}
	return "git"
}

// gitRunner runs git with a locked-down environment.
type gitRunner struct {
	bin, scheme, token string
	noLazyFetch        bool
}

// local runs git using only objects already fetched. A blobless clone is a
// partial clone, and git fetches a missing object from the remote on demand,
// even a commit that is on another branch or in a fork [GIT-PARTIAL].
func (g *gitRunner) local(ctx context.Context, dir string, args ...string) (string, error) {
	l := *g
	l.noLazyFetch = true
	return l.run(ctx, dir, args...)
}

// run executes git and returns trimmed stdout. Errors carry the tail of
// stderr, with the token redacted in case a server echoes it.
func (g *gitRunner) run(ctx context.Context, dir string, args ...string) (string, error) {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, g.bin, args...)
	cmd.Env = g.env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 2000 {
			msg = "…" + msg[len(msg)-2000:]
		}
		if g.token != "" {
			msg = strings.ReplaceAll(msg, g.token, "[redacted]")
		}
		return "", &gitError{args: args[:min(len(args), 3)], stderr: msg, err: err}
	}
	return strings.TrimSpace(stdout.String()), nil
}

// env ignores the host's git configuration (no url.insteadOf, credential
// helpers, or hooks from ~/.gitconfig), never prompts, and allows only the
// base URL's transport, so a crafted remote cannot switch to ext:: or file://.
func (g *gitRunner) env() []string {
	cfg := [][2]string{
		{"protocol.allow", "never"},
		{"protocol." + g.scheme + ".allow", "always"},
		{"core.hooksPath", os.DevNull},
		{"credential.helper", ""},
	}
	if g.token != "" {
		// GitHub App installation tokens use the x-access-token user [GH-APP-TOKEN].
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + g.token))
		cfg = append(cfg, [2]string{"http.extraHeader", "Authorization: Basic " + basic})
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.TempDir(),
		"LANG=C", "LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(cfg)),
	}
	for i, kv := range cfg {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, kv[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, kv[1]))
	}
	if g.noLazyFetch {
		env = append(env, "GIT_NO_LAZY_FETCH=1")
	}
	return env
}

type gitError struct {
	args   []string
	stderr string
	err    error
}

func (e *gitError) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.err, e.stderr)
}

func (e *gitError) Unwrap() error { return e.err }

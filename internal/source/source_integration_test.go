//go:build integration

package source

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// gitServer serves bare repositories over git's smart HTTP protocol with
// `git http-backend`, the same protocol GitHub speaks, on loopback.
type gitServer struct {
	url      string
	root     string
	mu       sync.Mutex
	authSeen []string
}

// repo is a test repository: main has A -> B -> C, and feature has F on B.
type repo struct {
	A, B, C, F string
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newGitServer(t *testing.T) (*gitServer, repo) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git is required for source tests")
	}
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	os.MkdirAll(src, 0o755)
	git(t, src, "init", "-q", "-b", "main")
	commit := func(file, content, msg string) string {
		os.WriteFile(filepath.Join(src, file), []byte(content), 0o644)
		git(t, src, "add", "-A")
		git(t, src, "commit", "-q", "-m", msg)
		return git(t, src, "rev-parse", "HEAD")
	}
	var r repo
	r.A = commit("Dockerfile", "FROM scratch # v1\n", "A")
	r.B = commit("README", "b\n", "B")
	git(t, src, "switch", "-q", "-c", "feature")
	r.F = commit("fork.txt", "from a fork\n", "F")
	git(t, src, "switch", "-q", "main")
	os.Symlink("/etc", filepath.Join(src, "evil"))
	os.Symlink("Dockerfile", filepath.Join(src, "link"))
	r.C = commit("Dockerfile", "FROM scratch # v3\n", "C")

	s := &gitServer{root: filepath.Join(tmp, "repos")}
	bare := filepath.Join(s.root, "hami9", "demo.git")
	os.MkdirAll(filepath.Dir(bare), 0o755)
	git(t, tmp, "clone", "-q", "--bare", src, bare)
	git(t, bare, "config", "uploadpack.allowFilter", "true") // GitHub allows partial clone
	git(t, bare, "config", "http.receivepack", "false")

	execPath := git(t, tmp, "--exec-path")
	backend := &cgi.Handler{
		Path: filepath.Join(execPath, "git-http-backend"),
		Env:  []string{"GIT_PROJECT_ROOT=" + s.root, "GIT_HTTP_EXPORT_ALL=1"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		s.authSeen = append(s.authSeen, req.Header.Get("Authorization"))
		s.mu.Unlock()
		if strings.Contains(req.URL.Path, "private/") && req.Header.Get("Authorization") == "" {
			http.Error(w, "auth required", http.StatusForbidden)
			return
		}
		backend.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s, r
}

const opA = "11111111-1111-4111-8111-111111111111"
const opB = "22222222-2222-4222-8222-222222222222"

func fetcher(t *testing.T, s *gitServer) *Fetcher {
	return &Fetcher{Root: filepath.Join(t.TempDir(), "work"), BaseURL: s.url}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFetchBranchHead(t *testing.T) {
	s, r := newGitServer(t)
	f := fetcher(t, s)
	co, err := f.Fetch(t.Context(), Request{OperationID: opA, Repo: "hami9/demo", Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if co.SHA != r.C || co.BranchHead != r.C || co.Dir != filepath.Join(f.Root, "op-"+opA) {
		t.Fatalf("checkout = %+v, want head %s", co, r.C)
	}
	if got := read(t, filepath.Join(co.Dir, "Dockerfile")); got != "FROM scratch # v3\n" {
		t.Fatalf("Dockerfile = %q", got)
	}
	if _, err := os.Stat(filepath.Join(co.Dir, "fork.txt")); !os.IsNotExist(err) {
		t.Fatal("a file from another branch is in the checkout")
	}
	if fi, _ := os.Stat(f.Root); fi.Mode().Perm() != 0o700 {
		t.Fatalf("workspace root mode %v, want 0700", fi.Mode().Perm())
	}
}

func TestFetchPinnedAncestor(t *testing.T) {
	s, r := newGitServer(t)
	f := fetcher(t, s)
	co, err := f.Fetch(t.Context(), Request{OperationID: opA, Repo: "hami9/demo", Branch: "main", Ref: r.A})
	if err != nil {
		t.Fatal(err)
	}
	if co.SHA != r.A || co.BranchHead != r.C || read(t, filepath.Join(co.Dir, "Dockerfile")) != "FROM scratch # v1\n" {
		t.Fatalf("pinned checkout = %+v", co)
	}
	// Blobless: file contents of commits not checked out were never downloaded.
	missing := git(t, co.Dir, "rev-list", "--objects", "--missing=print", "--all")
	if !strings.Contains(missing, "\n?") && !strings.HasPrefix(missing, "?") {
		t.Fatal("expected a partial (blobless) clone with missing blobs")
	}
}

// Invariant 7: a commit that is not on the tracked branch is refused, even if
// the repository (or its fork network) contains it.
func TestFetchRejectsCommitsOffBranch(t *testing.T) {
	s, r := newGitServer(t)
	f := fetcher(t, s)
	for name, ref := range map[string]string{"feature-branch commit": r.F, "unknown commit": strings.Repeat("0", 40)} {
		_, err := f.Fetch(t.Context(), Request{OperationID: opA, Repo: "hami9/demo", Branch: "main", Ref: ref})
		// Refused by the history check, not only by the later ancestry check.
		if !errors.Is(err, ErrNotOnBranch) || !strings.Contains(err.Error(), "is not in the history of main") {
			t.Errorf("%s: %v, want ErrNotOnBranch from the history check", name, err)
		}
	}
	// The off-branch commit was never downloaded: a partial clone would
	// otherwise fetch it lazily from the server [GIT-PARTIAL].
	if _, err := f.Fetch(t.Context(), Request{OperationID: opA, Repo: "hami9/demo", Branch: "main", Ref: r.F}); !errors.Is(err, ErrNotOnBranch) {
		t.Fatalf("feature-branch commit: %v", err)
	}
	dir, _ := f.Workspace(opA)
	probe := exec.Command("git", "-C", dir, "cat-file", "-e", r.F)
	probe.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1")
	if probe.Run() == nil {
		t.Error("the feature-branch commit is in the workspace")
	}
	// The same commit is fine for the branch it is on.
	if co, err := f.Fetch(t.Context(), Request{OperationID: opA, Repo: "hami9/demo", Branch: "feature", Ref: r.F}); err != nil || co.SHA != r.F {
		t.Fatalf("feature@F = %+v, %v", co, err)
	}
	if _, err := f.Fetch(t.Context(), Request{OperationID: opA, Repo: "hami9/demo", Branch: "nope"}); !errors.Is(err, ErrBranchNotFound) {
		t.Fatalf("missing branch: %v", err)
	}
}

func TestWorkspacePerOperation(t *testing.T) {
	s, _ := newGitServer(t)
	f := fetcher(t, s)
	a, err := f.Fetch(t.Context(), Request{OperationID: opA, Repo: "hami9/demo", Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.Fetch(t.Context(), Request{OperationID: opB, Repo: "hami9/demo", Branch: "main"})
	if err != nil || a.Dir == b.Dir {
		t.Fatalf("workspaces %q and %q (%v)", a.Dir, b.Dir, err)
	}
	// A retry starts from an empty directory.
	stale := filepath.Join(a.Dir, "stale-build-output")
	os.WriteFile(stale, []byte("x"), 0o600)
	if _, err := f.Fetch(t.Context(), Request{OperationID: opA, Repo: "hami9/demo", Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("retry reused a dirty workspace")
	}
	if err := f.Cleanup(opA); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Dir); !os.IsNotExist(err) {
		t.Fatal("Cleanup left the workspace")
	}
	if _, err := os.Stat(b.Dir); err != nil {
		t.Fatal("Cleanup removed another operation's workspace")
	}
}

func TestTokenTravelsInHeaderOnly(t *testing.T) {
	s, _ := newGitServer(t)
	f := fetcher(t, s)
	const token = "ghs_testtoken1234567890"
	// The server refuses "private/" without credentials; the repo does not
	// exist there, so the fetch fails after authenticating, and the error
	// must not repeat the token.
	_, err := f.Fetch(t.Context(), Request{OperationID: opA, Repo: "private/none", Branch: "main", Token: token})
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("private fetch error = %v", err)
	}
	co, err := f.Fetch(t.Context(), Request{OperationID: opB, Repo: "hami9/demo", Branch: "main", Token: token})
	if err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
	s.mu.Lock()
	sent := strings.Join(s.authSeen, "\n")
	s.mu.Unlock()
	if !strings.Contains(sent, want) {
		t.Fatalf("Authorization header not sent; saw %q", sent)
	}
	// Nothing in the workspace (including .git/config) holds the token.
	filepath.Walk(co.Dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), token) || strings.Contains(string(b), want[6:]) {
				t.Errorf("token stored in %s", p)
			}
		}
		return nil
	})
}

func TestCheckoutPath(t *testing.T) {
	s, _ := newGitServer(t)
	co, err := fetcher(t, s).Fetch(t.Context(), Request{OperationID: opA, Repo: "hami9/demo", Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ok := range []string{"Dockerfile", ".", "link"} {
		if _, err := co.Path(ok); err != nil {
			t.Errorf("Path(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"evil", "evil/passwd", "../x", "/etc/passwd"} {
		if _, err := co.Path(bad); !errors.Is(err, ErrEscapes) {
			t.Errorf("Path(%q) = %v, want ErrEscapes", bad, err)
		}
	}
	if _, err := co.Path("missing/Dockerfile"); err == nil {
		t.Error("missing path accepted")
	}
}

func TestFetchRejectsBadInput(t *testing.T) {
	// The git binary does not exist: validation must fail before any exec.
	f := &Fetcher{Root: t.TempDir(), BaseURL: "https://github.com", Git: "/nonexistent/git"}
	bad := []Request{
		{OperationID: "x", Repo: "hami9/demo", Branch: "main"},
		{OperationID: opA, Repo: "../etc", Branch: "main"},
		{OperationID: opA, Repo: "hami9/demo", Branch: "--upload-pack=touch /tmp/pwned"},
		{OperationID: opA, Repo: "hami9/demo", Branch: "main", Ref: "HEAD~1"},
	}
	for _, req := range bad {
		if _, err := f.Fetch(t.Context(), req); err == nil || strings.Contains(err.Error(), "nonexistent") {
			t.Errorf("Fetch(%+v) = %v, want a validation error", req, err)
		}
	}
	for _, base := range []string{"http://github.com", "https://user:pw@github.com", "ext::sh -c touch% /tmp/pwned", "file:///srv/repos"} {
		f.BaseURL = base
		if _, err := f.Fetch(t.Context(), Request{OperationID: opA, Repo: "hami9/demo", Branch: "main"}); err == nil || strings.Contains(err.Error(), "nonexistent") {
			t.Errorf("base %q: %v, want refusal before git runs", base, err)
		}
	}
}

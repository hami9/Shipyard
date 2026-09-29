package app

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func TestCheckBranch(t *testing.T) {
	good := []string{"main", "release/1.x", "feature/foo-bar", "v1.2.3", "a", "user@host", strings.Repeat("b", 255)}
	for _, b := range good {
		if err := CheckBranch(b); err != nil {
			t.Errorf("CheckBranch(%q) = %v", b, err)
		}
	}
	bad := []string{
		"", strings.Repeat("b", 256),
		"-f", "--upload-pack=touch /tmp/x", // option injection
		"@", "a..b", "a@{1}", "a//b", "/main", "main/", "main.",
		"has space", "a~1", "a^", "a:b", "a?", "a*", "a[b", `a\b`, "a\x00b", "a\tb", "a\x7fb",
		".hidden", "feat/.x", "main.lock", "feat/x.lock",
	}
	for _, b := range bad {
		if err := CheckBranch(b); err == nil {
			t.Errorf("CheckBranch(%q) accepted", b)
		}
	}
}

func TestCheckRepoPath(t *testing.T) {
	for _, p := range []string{"Dockerfile", "docker/prod.Dockerfile", "./Dockerfile", "a/./b", "..hidden/Dockerfile", "dir..name/x"} {
		if err := CheckRepoPath(p, false); err != nil {
			t.Errorf("CheckRepoPath(%q) = %v", p, err)
		}
	}
	if err := CheckRepoPath(".", true); err != nil {
		t.Errorf("build context '.' rejected: %v", err)
	}
	for _, p := range []string{"", ".", "./", "/etc/passwd", "..", "../Dockerfile", "a/../../Dockerfile", "a/..", `a\..\b`, "a\x00b", strings.Repeat("x", 256)} {
		if err := CheckRepoPath(p, false); err == nil {
			t.Errorf("CheckRepoPath(%q) accepted", p)
		}
	}
}

func TestValidateNew(t *testing.T) {
	ok := Settings{Branch: ptr("main"), InternalPort: ptr(3000)}
	if err := ValidateNew("web", "hami9/demo", ok); err != nil {
		t.Fatalf("minimal app rejected: %v", err)
	}

	err := ValidateNew("Web_1", "not-a-repo", Settings{
		DockerfilePath: ptr("../Dockerfile"), InternalPort: ptr(0), HealthPath: ptr("health"),
		HealthTimeout: ptr(0 * time.Second), StopTimeout: ptr(-time.Second), CPULimit: ptr(0.0),
		MemoryLimit: ptr(int64(1 << 20)), GitHubInstallationID: ptr(int64(-1)),
	})
	var fe FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want FieldErrors", err)
	}
	got := map[string]bool{}
	for _, e := range fe {
		got[e.Field] = true
	}
	for _, f := range []string{"slug", "repo", "branch", "port", "dockerfile_path", "health_path", "health_timeout", "stop_timeout", "cpu_limit", "memory_limit", "github_installation_id"} {
		if !got[f] {
			t.Errorf("no error for %s (got %v)", f, fe)
		}
	}
	if len(fe) != 11 {
		t.Errorf("%d errors, want 11: %v", len(fe), fe)
	}
	if !strings.Contains(err.Error(), "slug: must be") {
		t.Errorf("Error() = %q", err.Error())
	}

	for _, repo := range []string{"hami9/..", "../x", "a/b/c", "/x", "x/"} {
		if ValidateNew("web", repo, ok) == nil {
			t.Errorf("repo %q accepted", repo)
		}
	}
	for _, slug := range []string{"", "-web", "web-", "1web", strings.Repeat("a", 41)} {
		if ValidateNew(slug, "hami9/demo", ok) == nil {
			t.Errorf("slug %q accepted", slug)
		}
	}
	if !ValidSlug(strings.Repeat("a", 40)) {
		t.Error("40-character slug rejected")
	}
}

func TestValidateUpdate(t *testing.T) {
	if err := ValidateUpdate(Settings{}); err != nil {
		t.Fatalf("empty update: %v", err)
	}
	if err := ValidateUpdate(Settings{Branch: ptr("-x"), StopTimeout: ptr(0 * time.Second)}); err == nil {
		t.Fatal("bad branch accepted in update")
	} else if fe := err.(FieldErrors); len(fe) != 1 || fe[0].Field != "branch" {
		t.Fatalf("update errors = %v (stop_timeout 0 is valid)", fe)
	}
}

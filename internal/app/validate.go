// Package app holds Shipyard's domain rules as pure functions with no I/O:
// validation of app settings now, and deployment state transitions later
// (ARCHITECTURE §8).
package app

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// FieldError is one invalid input field. Detail is shown to API clients.
type FieldError struct {
	Field  string `json:"field"`
	Detail string `json:"detail"`
}

// FieldErrors collects every problem with a request, so a client can fix
// them all at once.
type FieldErrors []FieldError

func (fe FieldErrors) Error() string {
	parts := make([]string, len(fe))
	for i, e := range fe {
		parts[i] = e.Field + ": " + e.Detail
	}
	return "invalid fields: " + strings.Join(parts, "; ")
}

// Err returns fe as an error, or nil when it is empty.
func (fe FieldErrors) Err() error {
	if len(fe) == 0 {
		return nil
	}
	return fe
}

func (fe *FieldErrors) add(field, format string, args ...any) {
	*fe = append(*fe, FieldError{Field: field, Detail: fmt.Sprintf(format, args...)})
}

// Limits. The database enforces the same bounds (migrations/0002).
const (
	MaxSlugLen       = 40
	MinMemoryLimit   = 6 << 20 // Docker's minimum [DK-RESOURCES]
	MaxHealthTimeout = 30 * time.Minute
	MaxStopTimeout   = 10 * time.Minute
	maxPathLen       = 255
	maxBranchLen     = 255
	maxHealthPathLen = 1024
	maxCPULimit      = 512.0
)

var (
	slugRE = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,38}[a-z0-9])?$`)
	// GitHub owners and repository names; the database CHECK matches.
	repoRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)
)

// Settings are the optional, changeable app settings, as pointers so that
// "not given" is distinct from a zero value.
type Settings struct {
	Branch               *string
	DockerfilePath       *string
	BuildContext         *string
	InternalPort         *int
	HealthPath           *string
	HealthTimeout        *time.Duration
	CPULimit             *float64
	MemoryLimit          *int64
	StopTimeout          *time.Duration
	AutoDeploy           *bool
	GitHubInstallationID *int64
}

// ValidateNew checks a new app. Branch and port are required.
func ValidateNew(slug, repo string, s Settings) error {
	var fe FieldErrors
	checkSlug(&fe, slug)
	checkRepo(&fe, repo)
	if s.Branch == nil {
		fe.add("branch", "is required")
	}
	if s.InternalPort == nil {
		fe.add("port", "is required")
	}
	checkSettings(&fe, s)
	return fe.Err()
}

// ValidateUpdate checks a partial update.
func ValidateUpdate(s Settings) error {
	var fe FieldErrors
	checkSettings(&fe, s)
	return fe.Err()
}

// ValidSlug reports whether s is a well-formed app slug.
func ValidSlug(s string) bool { return slugRE.MatchString(s) }

func checkSlug(fe *FieldErrors, s string) {
	if !slugRE.MatchString(s) {
		fe.add("slug", "must be 1-%d characters of a-z, 0-9, and '-', start with a letter, and not end with '-'", MaxSlugLen)
	}
}

func checkRepo(fe *FieldErrors, r string) {
	owner, name, _ := strings.Cut(r, "/")
	if !repoRE.MatchString(r) || name == "." || name == ".." || owner == "." || owner == ".." {
		fe.add("repo", "must be a GitHub repository as owner/name")
	}
}

func checkSettings(fe *FieldErrors, s Settings) {
	if s.Branch != nil {
		if err := CheckBranch(*s.Branch); err != nil {
			fe.add("branch", "%v", err)
		}
	}
	if s.DockerfilePath != nil {
		if err := CheckRepoPath(*s.DockerfilePath, false); err != nil {
			fe.add("dockerfile_path", "%v", err)
		}
	}
	if s.BuildContext != nil {
		if err := CheckRepoPath(*s.BuildContext, true); err != nil {
			fe.add("build_context", "%v", err)
		}
	}
	if s.InternalPort != nil && (*s.InternalPort < 1 || *s.InternalPort > 65535) {
		fe.add("port", "must be between 1 and 65535")
	}
	if s.HealthPath != nil {
		p := *s.HealthPath
		if !strings.HasPrefix(p, "/") || len(p) > maxHealthPathLen || strings.ContainsFunc(p, isSpaceOrControl) {
			fe.add("health_path", "must start with '/' and contain no spaces or control characters")
		}
	}
	if s.HealthTimeout != nil && (*s.HealthTimeout < time.Second || *s.HealthTimeout > MaxHealthTimeout) {
		fe.add("health_timeout", "must be between 1s and %s", MaxHealthTimeout)
	}
	if s.StopTimeout != nil && (*s.StopTimeout < 0 || *s.StopTimeout > MaxStopTimeout) {
		fe.add("stop_timeout", "must be between 0s and %s", MaxStopTimeout)
	}
	if s.CPULimit != nil && (*s.CPULimit <= 0 || *s.CPULimit > maxCPULimit) {
		fe.add("cpu_limit", "must be greater than 0 and at most %g", maxCPULimit)
	}
	if s.MemoryLimit != nil && *s.MemoryLimit < MinMemoryLimit {
		fe.add("memory_limit", "must be at least %d bytes (6 MiB)", MinMemoryLimit)
	}
	if s.GitHubInstallationID != nil && *s.GitHubInstallationID <= 0 {
		fe.add("github_installation_id", "must be positive")
	}
}

// CheckBranch applies git's ref-name rules to a branch name [GIT-REFNAME],
// plus one of Shipyard's: no leading '-', so the name can never be read as
// an option by a git command (argument injection).
func CheckBranch(b string) error {
	switch {
	case b == "" || len(b) > maxBranchLen:
		return fmt.Errorf("must be 1-%d characters", maxBranchLen)
	case strings.HasPrefix(b, "-"):
		return errors.New("must not start with '-'")
	case b == "@":
		return errors.New("must not be '@'")
	case strings.Contains(b, ".."), strings.Contains(b, "@{"), strings.Contains(b, "//"):
		return errors.New("must not contain '..', '@{', or '//'")
	case strings.HasPrefix(b, "/"), strings.HasSuffix(b, "/"), strings.HasSuffix(b, "."):
		return errors.New("must not start or end with '/' or end with '.'")
	case strings.ContainsAny(b, " ~^:?*[\\"), strings.ContainsFunc(b, isControl):
		return errors.New(`must not contain spaces, control characters, or any of ~ ^ : ? * [ \`)
	}
	for _, part := range strings.Split(b, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return errors.New("no path component may start with '.' or end with '.lock'")
		}
	}
	return nil
}

// CheckRepoPath checks a path that must stay inside the repository checkout:
// relative, no '..' component, no backslash or control characters. The
// worker re-checks after resolving symlinks (ADR-0004). allowDot accepts "."
// for the repository root.
func CheckRepoPath(p string, allowDot bool) error {
	switch {
	case p == "" || len(p) > maxPathLen:
		return fmt.Errorf("must be 1-%d characters", maxPathLen)
	case strings.HasPrefix(p, "/"):
		return errors.New("must be relative to the repository root")
	case strings.Contains(p, `\`), strings.ContainsFunc(p, isControl):
		return errors.New("must not contain backslashes or control characters")
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return errors.New("must not contain '..'")
		}
	}
	if clean := path.Clean(p); clean == "." && !allowDot {
		return errors.New("must name a file inside the repository")
	}
	return nil
}

func isControl(r rune) bool        { return r < 0x20 || r == 0x7f }
func isSpaceOrControl(r rune) bool { return r == ' ' || isControl(r) }

package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Deployment status states Shipyard uses [GH-DEPLOY].
const (
	StateInProgress = "in_progress"
	StateSuccess    = "success"
	StateFailure    = "failure"
	StateInactive   = "inactive"
)

// maxDescription is GitHub's limit on a status description [GH-DEPLOY].
const maxDescription = 140

// tokenReuse is how long before expiry a cached token is replaced.
const tokenReuse = 5 * time.Minute

// repoPathRE mirrors apps.repo_full_name.
var repoPathRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)

// validRepo reports whether repo is owner/name and safe in a URL path: no
// ".." (as internal/source refuses it) and no "." segment, so "../x" or
// "./x" cannot move the request to another path.
func validRepo(repo string) bool {
	owner, name, _ := strings.Cut(repo, "/")
	return repoPathRE.MatchString(repo) && !strings.Contains(repo, "..") && owner != "." && name != "."
}

// Deployment is what Shipyard tells GitHub about a release.
type Deployment struct {
	Ref         string // the full commit SHA
	Environment string // the app's slug
	Description string
	ShipyardID  string // the Shipyard deployment, in the payload
}

// DeploymentStatus is one state change of a GitHub Deployment.
type DeploymentStatus struct {
	State          string
	Description    string // cut to 140 characters
	EnvironmentURL string // where the release serves; may be empty
}

// Reporter mirrors Shipyard deployments as GitHub Deployments of the app's
// repository (ROADMAP P4.6). Its tokens are limited to one repository and
// deployments: write, and reused until shortly before they expire.
type Reporter struct {
	App *App

	mu     sync.Mutex
	tokens map[string]Token
}

func (r *Reporter) token(ctx context.Context, installationID int64, repo string) (string, error) {
	key := strconv.FormatInt(installationID, 10) + "/" + repo
	r.mu.Lock()
	t, ok := r.tokens[key]
	r.mu.Unlock()
	if ok && r.App.now().Before(t.ExpiresAt.Add(-tokenReuse)) {
		return t.Value, nil
	}
	t, err := r.App.TokenWith(ctx, installationID, repo, map[string]string{"deployments": "write"})
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	if r.tokens == nil {
		r.tokens = map[string]Token{}
	}
	r.tokens[key] = t
	r.mu.Unlock()
	return t.Value, nil
}

// CreateDeployment creates the GitHub Deployment of an exact commit and
// returns its id. auto_merge is off and required_contexts empty: GitHub
// would otherwise merge the default branch into the ref or refuse a commit
// whose status checks are not all green, and Shipyard deploys exactly the
// commit it built [GH-DEPLOY].
func (r *Reporter) CreateDeployment(ctx context.Context, installationID int64, repo string, d Deployment) (int64, error) {
	if !validRepo(repo) {
		return 0, fmt.Errorf("repository %q is not owner/name", repo)
	}
	tok, err := r.token(ctx, installationID, repo)
	if err != nil {
		return 0, err
	}
	body, _ := json.Marshal(map[string]any{
		"ref":                    d.Ref,
		"task":                   "deploy",
		"auto_merge":             false,
		"required_contexts":      []string{},
		"environment":            d.Environment,
		"description":            cut(d.Description),
		"production_environment": true,
		"transient_environment":  false,
		"payload":                map[string]string{"shipyard_deployment": d.ShipyardID},
	})
	raw, err := r.App.post(ctx, tok, "/repos/"+repo+"/deployments", body)
	if err != nil {
		return 0, fmt.Errorf("create a GitHub deployment: %w", err)
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ID <= 0 {
		return 0, errors.New("create a GitHub deployment: the answer has no id")
	}
	return out.ID, nil
}

// CreateStatus adds a status to a GitHub Deployment. auto_inactive is off:
// it would not apply to a production environment anyway, and Shipyard
// marks the release it replaced inactive itself [GH-DEPLOY].
func (r *Reporter) CreateStatus(ctx context.Context, installationID int64, repo string, deploymentID int64, s DeploymentStatus) error {
	if !validRepo(repo) {
		return fmt.Errorf("repository %q is not owner/name", repo)
	}
	tok, err := r.token(ctx, installationID, repo)
	if err != nil {
		return err
	}
	status := map[string]any{"state": s.State, "description": cut(s.Description), "auto_inactive": false}
	if s.EnvironmentURL != "" {
		status["environment_url"] = s.EnvironmentURL
	}
	body, _ := json.Marshal(status)
	if _, err := r.App.post(ctx, tok, "/repos/"+repo+"/deployments/"+strconv.FormatInt(deploymentID, 10)+"/statuses", body); err != nil {
		return fmt.Errorf("set GitHub deployment %d to %s: %w", deploymentID, s.State, err)
	}
	return nil
}

func cut(s string) string {
	if r := []rune(s); len(r) > maxDescription {
		return string(r[:maxDescription-1]) + "…"
	}
	return s
}

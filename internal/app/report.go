package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hami9/shipyard/internal/store"
)

// GitHub reporting (ROADMAP P4.6, ARCHITECTURE §7 GitHub): a deployment of
// an app with a GitHub App installation is mirrored as a GitHub Deployment
// in the environment named by the app's slug. It is best effort: GitHub
// being down, or the App lacking the Deployments permission, costs a
// warning event, never the deploy.

// installation is the app's installation when reporting is on, else 0.
func (r *deployRun) installation() int64 {
	if r.GitHub == nil || r.app.GitHubInstallationID == nil {
		return 0
	}
	return *r.app.GitHubInstallationID
}

// reportStarted creates the deployment's GitHub Deployment once (a resumed
// run reuses the recorded one) and marks it in progress, once per run. Only
// a lost lease or a database error is returned.
func (r *deployRun) reportStarted(ctx context.Context) error {
	inst := r.installation()
	if inst == 0 || r.dep.ID == "" || r.reported {
		return nil
	}
	r.reported = true
	if r.dep.GitHubDeploymentID == 0 {
		desc := "Shipyard deploy " + r.dep.ID
		if r.dep.Kind == KindRollback && r.dep.SourceDeployment != nil {
			desc = "Shipyard rollback to deployment " + *r.dep.SourceDeployment
		}
		id, err := r.GitHub.CreateDeployment(ctx, inst, r.app.RepoFullName, GitHubDeployment{
			Ref: r.dep.SourceCommitSHA, Environment: r.app.Slug, Description: desc, ShipyardID: r.dep.ID})
		if err != nil {
			r.reportWarn(ctx, err)
			return nil
		}
		if err := r.Store.RecordGitHubDeployment(ctx, r.dep.ID, r.owner, id); err != nil {
			return fmt.Errorf("record the GitHub deployment: %w", err)
		}
		r.dep.GitHubDeploymentID = id
		r.event(ctx, store.LevelInfo, "reporting to GitHub as deployment %d (environment %s)", id, r.app.Slug)
	}
	r.reportStatus(ctx, r.dep.GitHubDeploymentID, GitHubStatus{State: GitHubInProgress,
		Description: "Deploying " + short(r.dep.SourceCommitSHA) + " (Shipyard operation " + r.op.ID + ")"})
	return nil
}

// reportActive marks the deployment successful, with the app's address,
// and the release it replaced inactive: production environments get no
// automatic inactive status [GH-DEPLOY].
func (r *deployRun) reportActive(ctx context.Context, prev *store.Deployment) {
	url := ""
	if len(r.hosts) > 0 {
		url = "https://" + r.hosts[0]
	}
	r.reportStatus(ctx, r.dep.GitHubDeploymentID, GitHubStatus{State: GitHubSuccess,
		Description: "Active: " + short(r.dep.SourceCommitSHA), EnvironmentURL: url})
	if prev != nil {
		r.reportStatus(ctx, prev.GitHubDeploymentID, GitHubStatus{State: GitHubInactive, Description: "Replaced by Shipyard deployment " + r.dep.ID})
	}
}

// reportFailed marks the deployment failed. The description names the
// phase and the operation, not the error: the reason is for operators, and
// GitHub shows the status to everyone who can read the repository.
func (r *deployRun) reportFailed(ctx context.Context) {
	phase := r.lastPhase
	if phase == "" {
		phase = "the deploy"
	}
	r.reportStatus(ctx, r.dep.GitHubDeploymentID, GitHubStatus{State: GitHubFailure,
		Description: "Failed during " + phase + "; see Shipyard operation " + r.op.ID})
}

func (r *deployRun) reportStatus(ctx context.Context, id int64, s GitHubStatus) {
	if inst := r.installation(); inst != 0 && id != 0 {
		if err := r.GitHub.CreateStatus(ctx, inst, r.app.RepoFullName, id, s); err != nil {
			r.reportWarn(ctx, err)
		}
	}
}

func (r *deployRun) reportWarn(ctx context.Context, err error) {
	r.log.Warn("GitHub deployment report failed", slog.Any("err", err))
	r.event(ctx, store.LevelWarn, "could not report to GitHub: %v", err)
}

func short(sha string) string { return sha[:min(12, len(sha))] }

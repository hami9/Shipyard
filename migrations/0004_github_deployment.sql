-- P4.6: the GitHub Deployment that mirrors a deployment of an app with a
-- GitHub App installation. It is kept so a resumed deploy reports to the
-- same one, and so the next release can mark it inactive. NULL: not
-- reported (no installation, or GitHub refused).
ALTER TABLE deployments ADD COLUMN github_deployment_id bigint CHECK (github_deployment_id > 0);

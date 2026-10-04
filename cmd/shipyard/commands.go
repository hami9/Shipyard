package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hami9/shipyard/internal/client"
)

func table(e env, header string, rows [][]string) error {
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, header)
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	return tw.Flush()
}

// cmdLogin verifies a token against the API before saving it, so a typo is
// caught now rather than on the next deploy.
func cmdLogin(ctx context.Context, e env, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	url := fs.String("url", "", "")
	if _, err := parse(fs, args, 0); err != nil || *url == "" {
		return errUsage
	}
	token, err := readSecret(e, "API token")
	if err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	c, err := client.New(*url, token)
	if err != nil {
		return err
	}
	who, err := c.Whoami(ctx)
	if err != nil {
		return fmt.Errorf("token check failed: %w", err)
	}
	path, err := defaultConfigPath(e.getenv)
	if err != nil {
		return err
	}
	if err := saveConfig(path, config{URL: *url, Token: token}); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Logged in to %s as token %s (%s). Saved to %s.\n", *url, who.Token, strings.Join(who.Scopes, ","), path)
	return nil
}

func cmdWhoami(ctx context.Context, e env, c *client.Client) error {
	w, err := c.Whoami(ctx)
	if err != nil {
		return err
	}
	expires := "never"
	if w.ExpiresAt != nil {
		expires = w.ExpiresAt.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(e.stdout, "token:   %s (%s)\nscopes:  %s\nexpires: %s\n", w.Token, w.Name, strings.Join(w.Scopes, ","), expires)
	return nil
}

func cmdAppList(ctx context.Context, e env, c *client.Client) error {
	apps, err := c.ListApps(ctx)
	if err != nil {
		return err
	}
	rows := make([][]string, len(apps))
	for i, a := range apps {
		rows[i] = []string{a.Slug, a.Repo, a.Branch, fmt.Sprint(a.Port)}
	}
	return table(e, "APP\tREPO\tBRANCH\tPORT", rows)
}

func cmdAppCreate(ctx context.Context, e env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("app create", flag.ContinueOnError)
	var n client.NewApp
	fs.StringVar(&n.Repo, "repo", "", "")
	fs.StringVar(&n.Branch, "branch", "", "")
	fs.IntVar(&n.Port, "port", 0, "")
	fs.StringVar(&n.DockerfilePath, "dockerfile", "", "")
	fs.StringVar(&n.BuildContext, "context", "", "")
	fs.StringVar(&n.HealthPath, "health-path", "", "")
	fs.BoolVar(&n.AutoDeploy, "auto-deploy", false, "")
	fs.Int64Var(&n.GitHubInstallation, "github-installation", 0, "")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	n.Slug = pos[0]
	a, err := c.CreateApp(ctx, n)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Created app %s (%s, branch %s, port %d%s).\n", a.Slug, a.Repo, a.Branch, a.Port, autoDeployNote(a.AutoDeploy))
	return nil
}

// cmdAppUpdate changes the settings that decide what a push deploys (the
// tracked branch and auto-deploy) and how the repository is fetched (the
// GitHub App installation). Only the flags given are sent.
func cmdAppUpdate(ctx context.Context, e env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("app update", flag.ContinueOnError)
	branch := fs.String("branch", "", "")
	auto := fs.Bool("auto-deploy", false, "")
	installation := fs.Int64("github-installation", 0, "")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	var u client.AppUpdate
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "branch":
			u.Branch = branch
		case "auto-deploy":
			u.AutoDeploy = auto
		case "github-installation":
			u.GitHubInstallation = installation
		}
	})
	if u.Branch == nil && u.AutoDeploy == nil && u.GitHubInstallation == nil {
		return errUsage
	}
	a, err := c.UpdateApp(ctx, pos[0], u)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Updated app %s (branch %s%s).\n", a.Slug, a.Branch, autoDeployNote(a.AutoDeploy))
	return nil
}

func autoDeployNote(on bool) string {
	if on {
		return ", deploys on push"
	}
	return ""
}

func cmdAppShow(ctx context.Context, e env, c *client.Client, args []string) error {
	pos, err := parse(flag.NewFlagSet("app show", flag.ContinueOnError), args, 1)
	if err != nil {
		return err
	}
	a, err := c.GetApp(ctx, pos[0])
	if err != nil {
		return err
	}
	return table(e, "FIELD\tVALUE", [][]string{
		{"id", a.ID}, {"repo", a.Repo}, {"branch", a.Branch}, {"dockerfile", a.DockerfilePath},
		{"context", a.BuildContext}, {"port", fmt.Sprint(a.Port)}, {"health", a.HealthPath + " (timeout " + a.HealthTimeout + ")"},
		{"cpu", fmt.Sprint(a.CPULimit)}, {"memory", fmt.Sprintf("%d MiB", a.MemoryLimit>>20)},
		{"stop timeout", a.StopTimeout}, {"auto deploy", fmt.Sprint(a.AutoDeploy)}, {"github app", installationNote(a.GitHubInstallation)},
	})
}

func installationNote(id *int64) string {
	if id == nil {
		return "none (public repository)"
	}
	return fmt.Sprintf("installation %d", *id)
}

// cmdAppDelete queues the delete of an app. It is irreversible, so it wants
// --yes. With --follow it streams the operation's events; the operation
// disappears with the app, so "the app is gone" is the success it reports.
func cmdAppDelete(ctx context.Context, e env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("app delete", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	follow := fs.Bool("follow", false, "")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	slug := pos[0]
	if !*yes {
		return fmt.Errorf("deleting %s takes it offline and removes its containers, network, images, domains, configuration, and history; this cannot be undone. Repeat with --yes", slug)
	}
	res, err := c.DeleteApp(ctx, slug)
	if err != nil {
		return err
	}
	verb := "Queued the"
	if !res.Created {
		verb = "Already in progress: the"
	}
	fmt.Fprintf(e.stdout, "%s delete of %s.\noperation: %s (%s)\n", verb, slug, res.Operation.ID, res.Operation.Status)
	for _, id := range res.Superseded {
		fmt.Fprintf(e.stdout, "cancelled older queued operation %s\n", id)
	}
	if !*follow {
		return nil
	}
	op, followErr := c.FollowEvents(ctx, res.Operation.ID, 0, printEvent(e))
	if _, err := c.GetApp(ctx, slug); client.IsNotFound(err) {
		fmt.Fprintf(e.stdout, "Deleted %s.\n", slug)
		return nil
	}
	switch {
	case followErr != nil:
		return followErr
	case op.LastError != "":
		return fmt.Errorf("operation %s %s: %s", op.ID, op.Status, op.LastError)
	}
	return fmt.Errorf("operation %s %s, and %s still exists", op.ID, op.Status, slug)
}

func printEnv(e env, vars client.Env) error {
	rows := make([][]string, len(vars.Vars))
	for i, v := range vars.Vars {
		kind := "plain"
		if v.Secret {
			kind = "secret"
		}
		rows[i] = []string{v.Key, kind}
	}
	fmt.Fprintf(e.stdout, "revision %d\n", vars.Revision)
	return table(e, "KEY\tTYPE", rows)
}

func cmdEnvSet(ctx context.Context, e env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("env set", flag.ContinueOnError)
	plain := fs.Bool("plain", false, "")
	pos, err := parse(fs, args, 2)
	if err != nil {
		return err
	}
	value, err := readSecret(e, "Value for "+pos[1])
	if err != nil {
		return err
	}
	res, err := c.SetEnv(ctx, pos[0], pos[1], value, !*plain)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Set %s on %s: revision %d. It applies on the next deploy.\n", pos[1], pos[0], res.Revision)
	return nil
}

func cmdEnvUnset(ctx context.Context, e env, c *client.Client, args []string) error {
	pos, err := parse(flag.NewFlagSet("env unset", flag.ContinueOnError), args, 2)
	if err != nil {
		return err
	}
	res, err := c.UnsetEnv(ctx, pos[0], pos[1])
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Removed %s from %s: revision %d. It applies on the next deploy.\n", pos[1], pos[0], res.Revision)
	return nil
}

func cmdEnvList(ctx context.Context, e env, c *client.Client, args []string) error {
	pos, err := parse(flag.NewFlagSet("env list", flag.ContinueOnError), args, 1)
	if err != nil {
		return err
	}
	res, err := c.ListEnv(ctx, pos[0])
	if err != nil {
		return err
	}
	return printEnv(e, res)
}

func cmdDomainAdd(ctx context.Context, e env, c *client.Client, args []string) error {
	pos, err := parse(flag.NewFlagSet("domain add", flag.ContinueOnError), args, 2)
	if err != nil {
		return err
	}
	d, err := c.AddDomain(ctx, pos[0], pos[1])
	if err != nil {
		return err
	}
	target := "no active deployment yet: it serves 503 until the next deploy"
	if d.DeploymentID != nil {
		target = "serving deployment " + *d.DeploymentID
	}
	fmt.Fprintf(e.stdout, "Added %s to %s (%s).\nCaddy loads it at the worker's next sync, within a minute.\n", d.Hostname, pos[0], target)
	return nil
}

func cmdDomainRemove(ctx context.Context, e env, c *client.Client, args []string) error {
	pos, err := parse(flag.NewFlagSet("domain remove", flag.ContinueOnError), args, 2)
	if err != nil {
		return err
	}
	if err := c.RemoveDomain(ctx, pos[0], pos[1]); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Removed %s from %s. Caddy drops it at the worker's next sync.\n", pos[1], pos[0])
	return nil
}

func cmdDomainList(ctx context.Context, e env, c *client.Client, args []string) error {
	pos, err := parse(flag.NewFlagSet("domain list", flag.ContinueOnError), args, 1)
	if err != nil {
		return err
	}
	ds, err := c.ListDomains(ctx, pos[0])
	if err != nil {
		return err
	}
	rows := make([][]string, len(ds))
	for i, d := range ds {
		dep, dns := "-", "skipped"
		if d.DeploymentID != nil {
			dep = *d.DeploymentID
		}
		if d.DNSCheckedAt != nil {
			dns = d.DNSCheckedAt.UTC().Format(time.RFC3339)
		}
		rows[i] = []string{d.Hostname, dep, dns}
	}
	return table(e, "HOSTNAME\tDEPLOYMENT\tDNS CHECKED", rows)
}

func cmdDeploy(ctx context.Context, e env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	ref := fs.String("ref", "", "")
	key := fs.String("idempotency-key", "", "")
	follow := fs.Bool("follow", false, "")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	res, err := c.Deploy(ctx, pos[0], *ref, *key)
	if err != nil {
		return err
	}
	target := "the head of the tracked branch"
	if *ref != "" {
		target = *ref
	}
	verb := "Queued"
	if !res.Created {
		verb = "Already requested with this idempotency key:"
	}
	fmt.Fprintf(e.stdout, "%s deploy of %s at %s.\noperation: %s (%s)\n", verb, pos[0], target, res.Operation.ID, res.Operation.Status)
	for _, id := range res.Superseded {
		fmt.Fprintf(e.stdout, "cancelled older queued operation %s\n", id)
	}
	if *follow {
		return followEvents(ctx, e, c, res.Operation.ID)
	}
	return nil
}

// cmdLogs prints the app's container output: stdout lines to stdout and
// stderr lines to stderr, like docker logs. Known secret values arrive
// redacted (ADR-0008).
func cmdLogs(ctx context.Context, e env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	tail := fs.Int("tail", 100, "")
	follow := fs.Bool("follow", false, "")
	fs.BoolVar(follow, "f", false, "")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	reason, err := c.Logs(ctx, pos[0], *tail, *follow, func(l client.LogLine) {
		w := e.stdout
		if l.Stream == "stderr" {
			w = e.stderr
		}
		fmt.Fprintf(w, "%s %s\n", l.TS.Local().Format(time.TimeOnly), l.Line)
	})
	if err != nil {
		return err
	}
	if *follow {
		fmt.Fprintf(e.stderr, "-- %s\n", reason)
	}
	return nil
}

func cmdEvents(ctx context.Context, e env, c *client.Client, args []string) error {
	pos, err := parse(flag.NewFlagSet("events", flag.ContinueOnError), args, 1)
	if err != nil {
		return err
	}
	return followEvents(ctx, e, c, pos[0])
}

// printEvent prints one event line, with continuation lines indented.
func printEvent(e env) func(client.Event) {
	return func(ev client.Event) {
		msg := strings.ReplaceAll(strings.TrimRight(ev.Message, "\n"), "\n", "\n"+strings.Repeat(" ", len("15:04:05 level ")))
		fmt.Fprintf(e.stdout, "%s %-5s %s\n", ev.TS.Local().Format(time.TimeOnly), ev.Level, msg)
	}
}

// followEvents prints an operation's events until it ends, and fails unless
// it succeeded, so `deploy --follow` can gate a CI job.
func followEvents(ctx context.Context, e env, c *client.Client, id string) error {
	op, err := c.FollowEvents(ctx, id, 0, printEvent(e))
	if err != nil {
		return err
	}
	if op.Status != "succeeded" {
		if op.LastError != "" {
			return fmt.Errorf("operation %s %s: %s", op.ID, op.Status, op.LastError)
		}
		return fmt.Errorf("operation %s %s", op.ID, op.Status)
	}
	fmt.Fprintf(e.stdout, "operation %s succeeded\n", op.ID)
	return nil
}

// cmdRollback queues a rollback to an earlier deployment, given by its full
// ID or a unique prefix from `shipyard releases`.
func cmdRollback(ctx context.Context, e env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	to := fs.String("to", "", "")
	var o client.RollbackOptions
	fs.BoolVar(&o.WithCurrentConfig, "with-current-config", false, "")
	fs.BoolVar(&o.WithOldConfig, "with-old-config", false, "")
	key := fs.String("idempotency-key", "", "")
	follow := fs.Bool("follow", false, "")
	pos, err := parse(fs, args, 1)
	if err != nil || *to == "" {
		return errUsage
	}
	target, err := resolveDeployment(ctx, c, pos[0], *to)
	if err != nil {
		return err
	}
	res, err := c.Rollback(ctx, pos[0], target.ID, o, *key)
	if err != nil {
		return err
	}
	verb := "Queued"
	if !res.Created {
		verb = "Already requested with this idempotency key:"
	}
	fmt.Fprintf(e.stdout, "%s rollback of %s to deployment %s (commit %s).\noperation: %s (%s)\n",
		verb, pos[0], target.ID, target.Commit[:min(12, len(target.Commit))], res.Operation.ID, res.Operation.Status)
	for _, id := range res.Superseded {
		fmt.Fprintf(e.stdout, "cancelled older queued operation %s\n", id)
	}
	if *follow {
		return followEvents(ctx, e, c, res.Operation.ID)
	}
	return nil
}

// resolveDeployment finds the deployment of app that id names, as a full
// ID or a unique prefix among the latest 100.
func resolveDeployment(ctx context.Context, c *client.Client, app, id string) (client.Release, error) {
	page, err := c.Releases(ctx, app, 100, "")
	if err != nil {
		return client.Release{}, err
	}
	var found []client.Release
	for _, d := range page.Deployments {
		if strings.HasPrefix(d.ID, strings.ToLower(id)) {
			found = append(found, d)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return client.Release{}, fmt.Errorf("no deployment %q among %s's latest %d; see `shipyard releases %s`", id, app, len(page.Deployments), app)
	}
	return client.Release{}, fmt.Errorf("%q matches %d deployments; give more of the ID", id, len(found))
}

// cmdReleases prints an app's deployment history, newest first.
func cmdReleases(ctx context.Context, e env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("releases", flag.ContinueOnError)
	limit := fs.Int("limit", 0, "")
	before := fs.String("before", "", "")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	page, err := c.Releases(ctx, pos[0], *limit, *before)
	if err != nil {
		return err
	}
	if len(page.Deployments) == 0 {
		fmt.Fprintln(e.stdout, "No releases.")
		return nil
	}
	rows := make([][]string, len(page.Deployments))
	for i, d := range page.Deployments {
		env := "-"
		if d.EnvRevision > 0 {
			env = fmt.Sprintf("#%d", d.EnvRevision)
		}
		note, _, _ := strings.Cut(d.FailureReason, "\n")
		if len(note) > 60 {
			note = note[:57] + "..."
		}
		if note == "" && d.RollbackOf != "" {
			note = "rollback of " + d.RollbackOf[:min(8, len(d.RollbackOf))]
		}
		rows[i] = []string{d.ID[:8], d.Status, d.Commit[:min(12, len(d.Commit))], env, d.CreatedAt.Local().Format("2006-01-02 15:04"), note}
	}
	if err := table(e, "DEPLOYMENT\tSTATUS\tCOMMIT\tENV\tCREATED\tNOTE", rows); err != nil {
		return err
	}
	if page.Next != "" {
		fmt.Fprintf(e.stdout, "more: shipyard releases %s --before %s\n", pos[0], page.Next)
	}
	return nil
}

func cmdOperation(ctx context.Context, e env, c *client.Client, args []string) error {
	pos, err := parse(flag.NewFlagSet("operation", flag.ContinueOnError), args, 1)
	if err != nil {
		return err
	}
	o, err := c.Operation(ctx, pos[0])
	if err != nil {
		return err
	}
	rows := [][]string{{"id", o.ID}, {"kind", o.Kind}, {"status", o.Status}, {"attempt", fmt.Sprintf("%d of %d", o.Attempt, o.MaxAttempts)},
		{"created", o.CreatedAt.UTC().Format(time.RFC3339)}}
	if o.Phase != "" {
		rows = append(rows, []string{"phase", o.Phase})
	}
	if o.LastError != "" {
		rows = append(rows, []string{"last error", o.LastError})
	}
	return table(e, "FIELD\tVALUE", rows)
}

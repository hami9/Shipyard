// Command shipyard-worker executes deployment operations recorded in
// PostgreSQL. It is the only Shipyard process with Docker access (ADR-0001).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hami9/shipyard/internal/api"
	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/applogs"
	"github.com/hami9/shipyard/internal/backup"
	"github.com/hami9/shipyard/internal/build"
	"github.com/hami9/shipyard/internal/buildinfo"
	"github.com/hami9/shipyard/internal/config"
	"github.com/hami9/shipyard/internal/github"
	"github.com/hami9/shipyard/internal/logging"
	"github.com/hami9/shipyard/internal/queue"
	"github.com/hami9/shipyard/internal/reconcile"
	"github.com/hami9/shipyard/internal/routing"
	"github.com/hami9/shipyard/internal/runtime"
	"github.com/hami9/shipyard/internal/secrets"
	"github.com/hami9/shipyard/internal/source"
	"github.com/hami9/shipyard/internal/store"
)

const usage = `Usage: shipyard-worker <command>

Commands:
  run       Run the deployment worker
  backup    Write one backup: the database and Caddy's data to
            SHIPYARD_BACKUP_DIR, the KEKs to SHIPYARD_BACKUP_KEK_DIR
  restore --from DIR
            Load one backup directory into an empty database and into
            Caddy; see docs/RESTORE.md. Stop both services first
  version   Print version information

Configuration is read from SHIPYARD_* environment variables (see deploy/shipyard.env.example).
`

func main() {
	os.Exit(run(os.Args[1:], os.LookupEnv, os.Stdout, os.Stderr))
}

func run(args []string, lookup config.LookupFunc, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var from string // restore: the backup directory
	switch args[0] {
	case "version", "--version", "-version":
		fmt.Fprintln(stdout, "shipyard-worker", buildinfo.Get())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	case "run", "backup":
	case "restore":
		// --from DIR or --from=DIR; nothing else.
		switch rest := args[1:]; {
		case len(rest) == 2 && rest[0] == "--from" && rest[1] != "":
			from = rest[1]
		case len(rest) == 1 && strings.HasPrefix(rest[0], "--from=") && rest[0] != "--from=":
			from = strings.TrimPrefix(rest[0], "--from=")
		default:
			fmt.Fprintf(stderr, "restore needs --from DIR, one backup directory\n\n%s", usage)
			return 2
		}
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}

	cfg, err := config.LoadWorker(lookup)
	if err != nil {
		fmt.Fprintf(stderr, "invalid configuration:\n%v\n", err)
		return 1
	}
	log := logging.New(stderr, cfg.Log.Level, cfg.Log.Format).With(
		slog.String("component", "worker"),
		slog.String("worker_id", cfg.WorkerID),
	)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "backup":
		if err := backupNow(ctx, cfg, log); err != nil {
			log.Error("backup failed", slog.Any("err", err))
			return 1
		}
		return 0
	case "restore":
		if err := restoreNow(ctx, cfg, from, log); err != nil {
			log.Error("restore failed", slog.Any("err", err))
			return 1
		}
		log.Info("restore finished: start shipyard-api and shipyard-worker; the worker rebuilds each app's active commit")
		return 0
	}
	if err := work(ctx, cfg, log); err != nil {
		log.Error("worker failed", slog.Any("err", err))
		return 1
	}
	return 0
}

// backupNow writes one backup and exits (ADR-0006); a systemd timer runs it
// nightly. It needs no database connection of its own: pg_dump makes one.
func backupNow(ctx context.Context, cfg config.Worker, log *slog.Logger) error {
	b := cfg.Backup
	job := &backup.Job{DatabaseURL: cfg.DatabaseURL, PGDump: b.PGDump, KEKSource: cfg.KEKDir,
		Dir: b.Dir, KEKDir: b.KEKDir, Hook: b.Hook, KEKHook: b.KEKHook,
		KeepDaily: b.KeepDaily, KeepWeekly: b.KeepWeekly, Version: buildinfo.Get().String(), Log: log}
	if cfg.Caddy.Enabled {
		rt, err := runtime.New()
		if err != nil {
			return err
		}
		defer rt.Close()
		job.Caddy = edgeData{rt, cfg.Caddy.Name}
	}
	return job.Run(ctx)
}

// restoreNow loads one backup onto this host (ADR-0006, docs/RESTORE.md):
// Caddy's data into the edge container, then the dump into the empty
// database. The worker must not be running: it would act on a half-restored
// database.
func restoreNow(ctx context.Context, cfg config.Worker, from string, log *slog.Logger) error {
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	job := &backup.Restore{From: from, DatabaseURL: cfg.DatabaseURL, PGRestore: cfg.Backup.PGRestore,
		KEKDir: cfg.KEKDir, DatabaseEmpty: store.New(db).Empty, Log: log}
	if cfg.Caddy.Enabled {
		rt, err := runtime.New()
		if err != nil {
			return err
		}
		defer rt.Close()
		// Without the API's socket directory, which exists only once the API
		// has run: the worker recreates the edge with it at its next start,
		// keeping the volumes.
		spec := edgeSpec(cfg)
		spec.APISocketDir = ""
		job.Caddy = edgeRestore{rt, spec}
	}
	return job.Run(ctx)
}

// work claims operations one at a time until shutdown. A shutdown in the
// middle of a deploy leaves its lease to expire; the next start resumes it.
func work(ctx context.Context, cfg config.Worker, log *slog.Logger) error {
	log.Info("starting", slog.String("version", buildinfo.Get().String()))
	// The worker decrypts environment revisions for containers.
	if cfg.KEKActive == "" {
		return fmt.Errorf("%s is required: it names the KEK in %s", config.EnvKEKActive, cfg.KEKDir)
	}
	keys, err := secrets.LoadKeyring(cfg.KEKDir, cfg.KEKActive)
	if err != nil {
		return fmt.Errorf("load KEKs: %w", err)
	}
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	rt, err := runtime.New()
	if err != nil {
		return err
	}
	defer rt.Close()
	builder := &build.Builder{Name: cfg.BuilderName}
	limits := build.Limits{Memory: cfg.BuilderMemory, CPUQuota: int(math.Round(cfg.BuilderCPUs * 100_000))}
	if err := builder.Ensure(ctx, limits); err != nil {
		return fmt.Errorf("buildx builder: %w", err)
	}

	s := store.New(db)
	if err := ensureEdge(ctx, cfg, rt, log); err != nil {
		return err
	}
	router, err := syncRoutes(ctx, cfg, s, log)
	if err != nil {
		return err
	}

	env := secrets.NewEnv(keys, s)
	logSrv, err := serveLogs(cfg.WorkerSocket, &applogs.Server{Store: s, Secrets: env, Source: runtimeAdapter{rt}, Log: log}, log)
	if err != nil {
		return err
	}
	defer logSrv.Close()

	src := sourceAdapter{f: &source.Fetcher{Root: cfg.WorkDir, BaseURL: cfg.SourceBaseURL}}
	var ghApp *github.App
	if cfg.GitHub.AppID != "" {
		// The App's key stays in its file, out of the database (P4.4).
		key, err := github.LoadKey(cfg.GitHub.KeyFile)
		if err != nil {
			return fmt.Errorf("load the GitHub App key: %w", err)
		}
		ghApp = &github.App{ID: cfg.GitHub.AppID, Key: key, APIURL: cfg.GitHub.APIURL, HTTP: &http.Client{Timeout: 30 * time.Second}}
		src.gh = ghApp
	}
	deployer := &app.Deployer{
		Store:   s,
		Source:  src,
		Builder: buildAdapter{builder},
		Runtime: runtimeAdapter{rt},
		Env:     env,
		Health:  healthGate,
		Log:     log,
	}
	if ghApp != nil {
		// Deploys of apps with an installation show up in GitHub (P4.6).
		deployer.GitHub = githubReporter{&github.Reporter{App: ghApp}}
	}
	if router != nil { // a nil *routing.Router must not become a non-nil interface
		deployer.Router = router
	}
	if cfg.CrashAt != "" {
		if deployer.Fault, err = crashAt(cfg.CrashAt, log); err != nil {
			return err
		}
	}
	deleter := &app.Deleter{Store: s, Runtime: runtimeAdapter{rt}, Log: log}
	if router != nil {
		deleter.Router = router
	}
	q := queue.New(s, log, cfg.WorkerID, cfg.Lease, cfg.PollInterval)
	rec := &reconcile.Reconciler{Queue: s, Store: s, Runtime: runtimeAdapter{rt}, Env: env, Log: log,
		Janitor: &app.Janitor{Store: s, Runtime: runtimeAdapter{rt}, Window: cfg.ObservationWindow, Log: log},
		Images:  &app.ImagePruner{Store: s, Runtime: runtimeAdapter{rt}, Keep: cfg.RetainImages, Log: log}}
	if router != nil {
		rec.Router = router
	}

	// The rest of retention (ADR-0006): daily by default, and at start.
	ret := &app.Retention{Events: s, Cache: builder, CacheMax: cfg.BuildCacheMax,
		KeepOperations: cfg.RetainOperations, MaxEventBytes: cfg.OperationLogMax, Log: log}

	// Pushes whose webhook never arrived (P4.5): at start, which is when
	// they are likely, and then every catch-up interval.
	catchUp := &app.CatchUp{Store: s, Heads: src, Log: log}

	var wg sync.WaitGroup
	wg.Go(func() { rec.Run(ctx, cfg.ReconcileInterval) })
	wg.Go(func() {
		reconcile.Every(ctx, cfg.RetentionInterval, func(ctx context.Context) {
			if err := ret.Run(ctx); err != nil && ctx.Err() == nil {
				log.Warn("retention incomplete", slog.Any("err", err))
			}
		})
	})
	wg.Go(func() {
		reconcile.Every(ctx, cfg.CatchUpInterval, func(ctx context.Context) {
			if _, err := catchUp.Run(ctx); err != nil && ctx.Err() == nil {
				log.Warn("catch-up incomplete", slog.Any("err", err))
			}
		})
	})
	log.Info("worker ready", slog.Duration("poll_interval", cfg.PollInterval), slog.String("work_dir", cfg.WorkDir))
	for {
		op, err := q.Next(ctx)
		if err != nil {
			break // shutdown
		}
		process(ctx, q, s, deployer, deleter, op, log)
	}
	wg.Wait()
	log.Info("worker stopped")
	return nil
}

// crashAt is the crash-safety suite's hook (P3.7, SHIPYARD_TEST_CRASH_AT):
// when a deploy reaches the fault point, this process is killed as by
// `kill -9`: no deferred call, no shutdown, no flush. Never set it in
// production.
func crashAt(point string, log *slog.Logger) (func(string), error) {
	if !slices.Contains(app.FaultPoints, point) {
		return nil, fmt.Errorf("%s=%q: no such fault point (one of %s)", config.EnvTestCrashAt, point, strings.Join(app.FaultPoints, ", "))
	}
	log.Warn("fault injection is on: this worker kills itself during a deploy", slog.String("at", point))
	return func(p string) {
		if p != point {
			return
		}
		log.Warn("fault injection: killing the worker", slog.String("at", p))
		if proc, err := os.FindProcess(os.Getpid()); err != nil || proc.Kill() != nil {
			os.Exit(137)
		}
		select {} // the signal is on its way: nothing may run past the point
	}, nil
}

// serveLogs serves app logs to the API on a private Unix socket, mode 0660
// for the shared group (ADR-0008). Closing the server drops open streams;
// the API reports them as cut short.
func serveLogs(path string, h http.Handler, log *slog.Logger) (*http.Server, error) {
	ln, err := api.Listen("unix:" + path)
	if err != nil {
		return nil, fmt.Errorf("log socket: %w", err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("log socket stopped", slog.Any("err", err))
		}
	}()
	log.Info("log socket ready", slog.String("path", path))
	return srv, nil
}

// ensureEdge keeps the Caddy container running and joined to every app
// network, with its admin API on a socket only the worker's group can use
// (ADR-0003). Routes are loaded from Phase 2 on (P2.2–P2.4). With an API
// hostname, the API's socket directory is mounted in too (P2.8).
func ensureEdge(ctx context.Context, cfg config.Worker, rt *runtime.Runtime, log *slog.Logger) error {
	c := cfg.Caddy
	if !c.Enabled {
		log.Warn("caddy is disabled; apps get no routes", slog.String("env", config.EnvCaddy))
		return nil
	}
	spec := edgeSpec(cfg)
	id, err := rt.EnsureEdge(ctx, spec)
	if err != nil {
		return fmt.Errorf("caddy: %w", err)
	}
	rt.Edge = c.Name
	log.Info("caddy ready", slog.String("container", c.Name), slog.String("container_id", id[:12]),
		slog.String("admin_socket", spec.AdminSocket()))
	return nil
}

// edgeSpec is the Caddy container this configuration asks for.
func edgeSpec(cfg config.Worker) runtime.EdgeSpec {
	c := cfg.Caddy
	spec := runtime.EdgeSpec{Name: c.Name, Image: c.Image, AdminDir: c.AdminDir, GID: os.Getegid(),
		BindIP: c.BindIP, HTTPPort: c.HTTPPort, HTTPSPort: c.HTTPSPort}
	if cfg.APIHostname != "" {
		spec.APISocketDir = filepath.Dir(cfg.APISocket())
	}
	if spec.Image == "" {
		spec.Image = runtime.DefaultEdgeImage
	}
	return spec
}

// syncRoutes makes Caddy serve exactly what the routes table says
// (ARCHITECTURE §5, Reconciler step 3): render, then load only if the
// running config differs, guarded by its Etag. It returns the router that
// deploys use to switch traffic; nil when Caddy is disabled. With an API
// hostname, Caddy serves the API there over HTTPS from its socket (P2.8).
func syncRoutes(ctx context.Context, cfg config.Worker, s *store.Store, log *slog.Logger) (*routing.Router, error) {
	c := cfg.Caddy
	if !c.Enabled {
		return nil, nil
	}
	settings := routing.Settings{
		AdminSocket:  filepath.Join(c.AdminDir, runtime.AdminSocketName),
		VerifySocket: filepath.Join(c.AdminDir, routing.VerifySocketName),
		CA:           c.CA,
		ACMEEmail:    c.ACMEEmail,
	}
	if cfg.APIHostname != "" {
		settings.APIHostname, settings.APIUpstream = cfg.APIHostname, "unix/"+cfg.APISocket() // [CADDY-RP]
	}
	router, err := routing.NewRouter(s, settings)
	if err != nil {
		return nil, fmt.Errorf("caddy router: %w", err)
	}
	changed, err := router.Sync(ctx)
	if err != nil {
		return nil, fmt.Errorf("caddy config: %w", err)
	}
	log.Info("caddy config in sync", slog.Bool("reloaded", changed), slog.String("api_hostname", cfg.APIHostname))
	return router, nil
}

// process runs one claimed operation while holding its lease.
func process(ctx context.Context, q *queue.Queue, s *store.Store, d *app.Deployer, del *app.Deleter, op store.Operation, log *slog.Logger) {
	log = log.With(slog.String("operation_id", op.ID), slog.String("app_id", op.AppID), slog.String("kind", op.Kind))
	log.Info("operation claimed", slog.Int("attempt", op.Attempt))
	held, release := q.Hold(ctx, op)
	defer release()
	var err error
	switch op.Kind {
	case app.KindDeploy, app.KindRollback:
		err = d.Run(held, op, q.Owner())
	case app.KindDelete:
		err = del.Run(held, op, q.Owner())
	default:
		_, err = s.FailOperation(held, op.ID, q.Owner(), fmt.Sprintf("operation kind %q is not supported", op.Kind), 0)
	}
	if err != nil {
		log.Warn("operation not finished; it resumes after its lease expires", slog.Any("err", err))
		return
	}
	log.Info("operation finished")
}

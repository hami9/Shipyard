// Command shipyard-worker executes deployment operations recorded in
// PostgreSQL. It is the only Shipyard process with Docker access (ADR-0001).
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/build"
	"github.com/hami9/shipyard/internal/buildinfo"
	"github.com/hami9/shipyard/internal/config"
	"github.com/hami9/shipyard/internal/logging"
	"github.com/hami9/shipyard/internal/queue"
	"github.com/hami9/shipyard/internal/runtime"
	"github.com/hami9/shipyard/internal/secrets"
	"github.com/hami9/shipyard/internal/source"
	"github.com/hami9/shipyard/internal/store"
)

const usage = `Usage: shipyard-worker <command>

Commands:
  run       Run the deployment worker
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
	switch args[0] {
	case "version", "--version", "-version":
		fmt.Fprintln(stdout, "shipyard-worker", buildinfo.Get())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	case "run":
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

	if err := work(ctx, cfg, log); err != nil {
		log.Error("worker failed", slog.Any("err", err))
		return 1
	}
	return 0
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

	if err := ensureEdge(ctx, cfg.Caddy, rt, log); err != nil {
		return err
	}

	s := store.New(db)
	deployer := &app.Deployer{
		Store:   s,
		Source:  sourceAdapter{&source.Fetcher{Root: cfg.WorkDir, BaseURL: cfg.SourceBaseURL}},
		Builder: buildAdapter{builder},
		Runtime: runtimeAdapter{rt},
		Env:     secrets.NewEnv(keys, s),
		Health:  healthGate,
		Log:     log,
	}
	q := queue.New(s, log, cfg.WorkerID, 0, cfg.PollInterval)

	var wg sync.WaitGroup
	wg.Go(func() { requeueExpired(ctx, s, log) })
	log.Info("worker ready", slog.Duration("poll_interval", cfg.PollInterval), slog.String("work_dir", cfg.WorkDir))
	for {
		op, err := q.Next(ctx)
		if err != nil {
			break // shutdown
		}
		process(ctx, q, s, deployer, op, log)
	}
	wg.Wait()
	log.Info("worker stopped")
	return nil
}

// ensureEdge keeps the Caddy container running and joined to every app
// network, with its admin API on a socket only the worker's group can use
// (ADR-0003). Routes are loaded from Phase 2 on (P2.2–P2.4).
func ensureEdge(ctx context.Context, c config.Caddy, rt *runtime.Runtime, log *slog.Logger) error {
	if !c.Enabled {
		log.Warn("caddy is disabled; apps get no routes", slog.String("env", config.EnvCaddy))
		return nil
	}
	spec := runtime.EdgeSpec{Name: c.Name, Image: c.Image, AdminDir: c.AdminDir, GID: os.Getegid(),
		BindIP: c.BindIP, HTTPPort: c.HTTPPort, HTTPSPort: c.HTTPSPort}
	if spec.Image == "" {
		spec.Image = runtime.DefaultEdgeImage
	}
	id, err := rt.EnsureEdge(ctx, spec)
	if err != nil {
		return fmt.Errorf("caddy: %w", err)
	}
	rt.Edge = c.Name
	log.Info("caddy ready", slog.String("container", c.Name), slog.String("container_id", id[:12]),
		slog.String("admin_socket", spec.AdminSocket()))
	return nil
}

// process runs one claimed operation while holding its lease.
func process(ctx context.Context, q *queue.Queue, s *store.Store, d *app.Deployer, op store.Operation, log *slog.Logger) {
	log = log.With(slog.String("operation_id", op.ID), slog.String("app_id", op.AppID), slog.String("kind", op.Kind))
	log.Info("operation claimed", slog.Int("attempt", op.Attempt))
	held, release := q.Hold(ctx, op)
	defer release()
	var err error
	switch op.Kind {
	case "deploy":
		err = d.Run(held, op, q.Owner())
	default: // rollback arrives in Phase 3
		_, err = s.FailOperation(held, op.ID, q.Owner(), fmt.Sprintf("operation kind %q is not supported yet", op.Kind), 0)
	}
	if err != nil {
		log.Warn("operation not finished; it resumes after its lease expires", slog.Any("err", err))
		return
	}
	log.Info("operation finished")
}

// requeueExpired returns operations of crashed workers to the queue, at start
// and then once per lease (ARCHITECTURE §5, Reconciler step 1). The rest of
// the reconciler arrives later.
func requeueExpired(ctx context.Context, s *store.Store, log *slog.Logger) {
	tick := time.NewTicker(queue.DefaultLease)
	defer tick.Stop()
	for {
		requeued, failed, err := s.RequeueExpired(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			log.Warn("requeue expired operations failed", slog.Any("err", err))
		case requeued+failed > 0:
			log.Info("expired operations requeued", slog.Int("requeued", requeued), slog.Int("failed", failed))
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

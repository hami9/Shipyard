// Command shipyard-worker executes deployment operations recorded in
// PostgreSQL. It is the only Shipyard process with Docker access (ADR-0001).
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hami9/shipyard/internal/buildinfo"
	"github.com/hami9/shipyard/internal/config"
	"github.com/hami9/shipyard/internal/logging"
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

// work holds the worker's database connection until shutdown. The operation
// loop (claim, lease, heartbeat) arrives with the queue in P1.5.
func work(ctx context.Context, cfg config.Worker, log *slog.Logger) error {
	log.Info("starting", slog.String("version", buildinfo.Get().String()))
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	log.Info("worker ready", slog.Duration("poll_interval", cfg.PollInterval))
	<-ctx.Done()
	log.Info("worker stopped")
	return nil
}

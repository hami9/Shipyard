// Command shipyard-api serves Shipyard's HTTP API and applies database
// migrations. It runs as an unprivileged user without Docker access
// (ADR-0001).
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hami9/shipyard/internal/api"
	"github.com/hami9/shipyard/internal/buildinfo"
	"github.com/hami9/shipyard/internal/config"
	"github.com/hami9/shipyard/internal/logging"
	"github.com/hami9/shipyard/internal/store"
	"github.com/hami9/shipyard/migrations"
)

const usage = `Usage: shipyard-api <command>

Commands:
  serve     Run the HTTP API server
  migrate   Apply pending database migrations
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
		fmt.Fprintln(stdout, "shipyard-api", buildinfo.Get())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	case "serve", "migrate":
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}

	cfg, err := config.LoadAPI(lookup)
	if err != nil {
		fmt.Fprintf(stderr, "invalid configuration:\n%v\n", err)
		return 1
	}
	log := logging.New(stderr, cfg.Log.Level, cfg.Log.Format).With(slog.String("component", "api"))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if args[0] == "migrate" {
		err = migrate(ctx, cfg, log)
	} else {
		err = serve(ctx, cfg, log)
	}
	if err != nil {
		log.Error(args[0]+" failed", slog.Any("err", err))
		return 1
	}
	return 0
}

func serve(ctx context.Context, cfg config.API, log *slog.Logger) error {
	log.Info("starting", slog.String("version", buildinfo.Get().String()))
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	ln, err := api.Listen(cfg.Listen)
	if err != nil {
		return err
	}
	return api.Serve(ctx, ln, api.NewHandler(log, db), cfg.ShutdownTimeout, log)
}

func migrate(ctx context.Context, cfg config.API, log *slog.Logger) error {
	ms, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	applied, err := store.Migrate(ctx, db, ms)
	if err != nil {
		return err
	}
	for _, m := range applied {
		log.Info("migration applied", slog.Int("version", m.Version), slog.String("name", m.Name))
	}
	log.Info("database schema up to date", slog.Int("applied", len(applied)), slog.Int("latest", ms[len(ms)-1].Version))
	return nil
}

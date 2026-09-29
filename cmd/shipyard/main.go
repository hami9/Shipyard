// Command shipyard is the operator CLI. It talks only to shipyard-api over
// HTTPS (or a local socket); it never touches Docker or the database.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/hami9/shipyard/internal/buildinfo"
	"github.com/hami9/shipyard/internal/client"
)

const usage = `Usage: shipyard <command> [arguments]

Connection:
  login --url URL          Save the API URL and a token (read from stdin)
  whoami                   Show the configured token's name, scopes, and expiry

Apps:
  app create SLUG --repo OWNER/NAME --branch BRANCH --port PORT
             [--dockerfile PATH] [--context PATH] [--health-path PATH]
  app list                 List apps (same as ps)
  app show APP             Show an app's settings
  ps                       List apps

Environment (values are read from stdin and never printed):
  env set APP KEY [--plain]   Set KEY; secret unless --plain
  env unset APP KEY
  env list APP                Keys only

Domains (DNS must point at the server first; Caddy picks changes up within a minute):
  domain add APP HOSTNAME
  domain remove APP HOSTNAME
  domain list APP

Deploys:
  deploy APP [--ref SHA] [--idempotency-key KEY] [--follow]
                           --follow streams the events and fails unless it succeeds
  releases APP [--limit N] [--before ID]
                           An app's deployments, newest first
  operation ID             Show an operation's status
  events ID                Stream an operation's events until it ends
  logs APP [--tail N] [--follow|-f]
                           The running release's output (default: last 100 lines);
                           known secret values are redacted

  version                  Print version information

Configuration: ~/.config/shipyard/config.json (SHIPYARD_CONFIG), or
SHIPYARD_URL and SHIPYARD_TOKEN in the environment.
`

// env is the process environment, injectable for tests.
type env struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	getenv         func(string) string
}

// errUsage asks run to print usage and exit 2.
var errUsage = errors.New("usage")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], env{os.Stdin, os.Stdout, os.Stderr, os.Getenv}))
}

func run(ctx context.Context, args []string, e env) int {
	if len(args) == 0 {
		fmt.Fprint(e.stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "version", "--version", "-version":
		fmt.Fprintln(e.stdout, "shipyard", buildinfo.Get())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(e.stdout, usage)
		return 0
	case "login":
		err = cmdLogin(ctx, e, args[1:])
	case "whoami", "app", "ps", "env", "domain", "deploy", "operation", "events", "logs", "releases":
		err = withClient(e, func(c *client.Client) error { return dispatch(ctx, e, c, args) })
	default:
		fmt.Fprintf(e.stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	switch {
	case errors.Is(err, errUsage):
		fmt.Fprint(e.stderr, usage)
		return 2
	case err != nil:
		fmt.Fprintln(e.stderr, "error:", err)
		return 1
	}
	return 0
}

func withClient(e env, fn func(*client.Client) error) error {
	path, err := defaultConfigPath(e.getenv)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(path, e.getenv)
	if err != nil {
		return err
	}
	c, err := client.New(cfg.URL, cfg.Token)
	if err != nil {
		return err
	}
	return fn(c)
}

func dispatch(ctx context.Context, e env, c *client.Client, args []string) error {
	cmd, rest := args[0], args[1:]
	sub := ""
	if (cmd == "app" || cmd == "env" || cmd == "domain") && len(rest) > 0 {
		sub, rest = rest[0], rest[1:]
	}
	switch cmd + " " + sub {
	case "whoami ":
		return cmdWhoami(ctx, e, c)
	case "ps ", "app list":
		return cmdAppList(ctx, e, c)
	case "app create":
		return cmdAppCreate(ctx, e, c, rest)
	case "app show":
		return cmdAppShow(ctx, e, c, rest)
	case "env set":
		return cmdEnvSet(ctx, e, c, rest)
	case "env unset":
		return cmdEnvUnset(ctx, e, c, rest)
	case "env list":
		return cmdEnvList(ctx, e, c, rest)
	case "domain add":
		return cmdDomainAdd(ctx, e, c, rest)
	case "domain remove":
		return cmdDomainRemove(ctx, e, c, rest)
	case "domain list":
		return cmdDomainList(ctx, e, c, rest)
	case "deploy ":
		return cmdDeploy(ctx, e, c, rest)
	case "operation ":
		return cmdOperation(ctx, e, c, rest)
	case "events ":
		return cmdEvents(ctx, e, c, rest)
	case "logs ":
		return cmdLogs(ctx, e, c, rest)
	case "releases ":
		return cmdReleases(ctx, e, c, rest)
	}
	return errUsage
}

// parse parses flags that may come before or after positional arguments,
// and requires exactly want positional arguments.
func parse(fs *flag.FlagSet, args []string, want int) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, errUsage
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(pos) != want {
		return nil, errUsage
	}
	return pos, nil
}

// readSecret reads a value from stdin, dropping one trailing newline so
// `echo value | shipyard env set` works as expected.
func readSecret(e env, prompt string) (string, error) {
	if f, ok := e.stdin.(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			fmt.Fprintln(e.stderr, prompt+" (end with Ctrl-D):")
		}
	}
	b, err := io.ReadAll(io.LimitReader(e.stdin, 1<<20))
	if err != nil {
		return "", err
	}
	s := strings.TrimSuffix(string(b), "\n")
	return strings.TrimSuffix(s, "\r"), nil
}

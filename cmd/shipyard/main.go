// Command shipyard is the operator CLI. It talks only to shipyard-api over
// HTTPS; it never touches Docker or the database directly.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/hami9/shipyard/internal/buildinfo"
)

const usage = `Usage: shipyard <command>

Commands:
  version   Print version information

App, deploy, env, and logs commands arrive in Phase 1 (docs/ROADMAP.md).
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version", "-version":
		fmt.Fprintln(stdout, "shipyard", buildinfo.Get())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

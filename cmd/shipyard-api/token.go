package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hami9/shipyard/internal/api"
	"github.com/hami9/shipyard/internal/store"
)

const tokenUsage = `Usage:
  shipyard-api token create [--user NAME] [--name NAME] [--scope SCOPES] [--ttl DURATION]
  shipyard-api token list
  shipyard-api token revoke PREFIX

create prints the new token once on stdout; store it immediately.
  --user    owner, created if missing (default "admin")
  --name    label shown in listings (default "cli")
  --scope   comma-separated: read, deploy, admin (default "admin")
  --ttl     lifetime, e.g. 12h or 90d; 1h to 366d (default "90d")
`

const (
	minTokenTTL = time.Hour
	maxTokenTTL = 366 * 24 * time.Hour
	cliActor    = "cli:shipyard-api"
)

// tokenCommand runs on the server with database access, which is how the
// first admin token is bootstrapped before any token exists (ADR-0007).
func tokenCommand(ctx context.Context, s *store.Store, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageError(tokenUsage)
	}
	switch args[0] {
	case "create":
		return tokenCreate(ctx, s, args[1:], stdout, stderr)
	case "list":
		return tokenList(ctx, s, stdout)
	case "revoke":
		if len(args) != 2 {
			return usageError(tokenUsage)
		}
		return tokenRevoke(ctx, s, args[1], stderr)
	default:
		return usageError(tokenUsage)
	}
}

type usageError string

func (u usageError) Error() string { return string(u) }

type tokenOptions struct {
	user, name string
	scopes     []string
	ttl        time.Duration
}

func parseTokenCreate(args []string) (tokenOptions, error) {
	fs := flag.NewFlagSet("token create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	user := fs.String("user", "admin", "")
	name := fs.String("name", "cli", "")
	scope := fs.String("scope", api.ScopeAdmin, "")
	ttl := fs.String("ttl", "90d", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return tokenOptions{}, usageError(tokenUsage)
	}
	o := tokenOptions{user: *user, name: *name}
	for _, sc := range strings.Split(*scope, ",") {
		if !api.ValidScope(sc) {
			return o, fmt.Errorf("unknown scope %q (want read, deploy, or admin)", sc)
		}
		o.scopes = append(o.scopes, sc)
	}
	d, err := parseTTL(*ttl)
	if err != nil {
		return o, err
	}
	o.ttl = d
	return o, nil
}

// parseTTL accepts Go durations plus a whole-day suffix, e.g. 90d.
func parseTTL(s string) (time.Duration, error) {
	var d time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("invalid --ttl %q", s)
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("invalid --ttl %q", s)
		}
	}
	if d < minTokenTTL || d > maxTokenTTL {
		return 0, fmt.Errorf("--ttl must be between 1h and 366d")
	}
	return d, nil
}

func tokenCreate(ctx context.Context, s *store.Store, args []string, stdout, stderr io.Writer) error {
	o, err := parseTokenCreate(args)
	if err != nil {
		return err
	}
	var plaintext string
	var tok store.Token
	err = s.InTx(ctx, func(tx *store.Store) error {
		u, err := tx.UserByName(ctx, o.user)
		if errors.Is(err, store.ErrNotFound) {
			u, err = tx.CreateUser(ctx, o.user)
		}
		if err != nil {
			return fmt.Errorf("user %q: %w", o.user, err)
		}
		expires := time.Now().Add(o.ttl)
		var prefix string
		var hash []byte
		plaintext, prefix, hash = api.NewToken()
		tok, err = tx.CreateToken(ctx, store.NewToken{
			UserID: u.ID, Name: o.name, Prefix: prefix, Hash: hash, Scopes: o.scopes, ExpiresAt: &expires,
		})
		if err != nil {
			// A prefix collision (48 random bits) is astronomically unlikely;
			// failing loudly beats a silent retry loop.
			return fmt.Errorf("create token: %w", err)
		}
		_, err = tx.RecordAudit(ctx, store.AuditEvent{Actor: cliActor, Action: "token.create", Target: tok.Prefix, Result: store.AuditSuccess})
		return err
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, plaintext)
	fmt.Fprintf(stderr, "Created token %s for user %q (scopes %s, expires %s).\nIt is shown only once: store it now.\n",
		tok.Prefix, o.user, strings.Join(tok.Scopes, ","), tok.ExpiresAt.UTC().Format(time.DateOnly))
	return nil
}

func tokenList(ctx context.Context, s *store.Store, stdout io.Writer) error {
	tokens, err := s.ListTokens(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PREFIX\tNAME\tSCOPES\tEXPIRES\tLAST USED\tSTATUS")
	now := time.Now()
	for _, t := range tokens {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.Prefix, t.Name, strings.Join(t.Scopes, ","),
			day(t.ExpiresAt), day(t.LastUsedAt), tokenStatus(t, now))
	}
	return tw.Flush()
}

func tokenStatus(t store.Token, now time.Time) string {
	switch {
	case t.RevokedAt != nil:
		return "revoked"
	case t.ExpiresAt != nil && !t.ExpiresAt.After(now):
		return "expired"
	default:
		return "active"
	}
}

func day(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.DateOnly)
}

func tokenRevoke(ctx context.Context, s *store.Store, prefix string, stderr io.Writer) error {
	err := s.InTx(ctx, func(tx *store.Store) error {
		if _, err := tx.RevokeToken(ctx, prefix); err != nil {
			return fmt.Errorf("revoke %s: %w", prefix, err)
		}
		_, err := tx.RecordAudit(ctx, store.AuditEvent{Actor: cliActor, Action: "token.revoke", Target: prefix, Result: store.AuditSuccess})
		return err
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "Revoked %s.\n", prefix)
	return nil
}

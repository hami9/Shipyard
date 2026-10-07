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
  shipyard-api token rotate PREFIX [--grace DURATION]

create and rotate print the new token once on stdout; store it immediately.
rotate issues a token with the same user, name, scopes, and lifetime, and
revokes the old one, or lets it work for --grace longer (up to 7d).
  --user    owner, created if missing (default "admin")
  --name    label shown in listings (default "cli")
  --scope   comma-separated: read, deploy, admin (default "admin")
  --ttl     lifetime, e.g. 12h or 90d; 1h to 366d (default "90d")
`

const (
	minTokenTTL = api.MinTokenTTL
	maxTokenTTL = api.MaxTokenTTL
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
	case "rotate":
		return tokenRotate(ctx, s, args[1:], stdout, stderr)
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
		// Bound the days before multiplying: a large count would overflow
		// and wrap into the allowed range.
		n, err := strconv.Atoi(days)
		if err != nil || n < 1 || n > 366 {
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

func parseTokenRotate(args []string) (prefix string, grace time.Duration, err error) {
	fs := flag.NewFlagSet("token rotate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	g := fs.String("grace", "0", "")
	// The prefix may come before or after the flag.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		prefix, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return "", 0, usageError(tokenUsage)
	}
	if prefix == "" && fs.NArg() == 1 {
		prefix = fs.Arg(0)
	} else if fs.NArg() != 0 || prefix == "" {
		return "", 0, usageError(tokenUsage)
	}
	if grace, err = parseGrace(*g); err != nil {
		return "", 0, err
	}
	return prefix, grace, nil
}

// parseGrace accepts 0, Go durations, and whole days, up to 7d.
func parseGrace(s string) (time.Duration, error) {
	if s == "0" {
		return 0, nil
	}
	var d time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 || n > 7 {
			return 0, fmt.Errorf("invalid --grace %q", s)
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("invalid --grace %q", s)
		}
	}
	if d < 0 || d > api.MaxRotationGrace {
		return 0, fmt.Errorf("--grace must be between 0 and 7d")
	}
	return d, nil
}

func tokenRotate(ctx context.Context, s *store.Store, args []string, stdout, stderr io.Writer) error {
	prefix, grace, err := parseTokenRotate(args)
	if err != nil {
		return err
	}
	var plaintext string
	var old, tok store.Token
	err = s.InTx(ctx, func(tx *store.Store) error {
		cur, err := tx.TokenByPrefix(ctx, prefix)
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("no token %s", prefix)
		}
		if err != nil {
			return err
		}
		now := time.Now()
		var n store.NewToken
		plaintext, n = api.Rotation(cur, now)
		old, tok, err = tx.RotateToken(ctx, cur.ID, n, grace)
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("token %s is revoked or expired: create a new one instead", prefix)
		}
		if err != nil {
			return fmt.Errorf("rotate %s: %w", prefix, err)
		}
		_, err = tx.RecordAudit(ctx, store.AuditEvent{Actor: cliActor, Action: "token.rotate",
			Target: old.Prefix + " -> " + tok.Prefix, Result: store.AuditSuccess})
		return err
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, plaintext)
	fmt.Fprintf(stderr, "Rotated %s: new token %s (scopes %s, expires %s).\n%s\nThe new token is shown only once: store it now.\n",
		old.Prefix, tok.Prefix, strings.Join(tok.Scopes, ","), tok.ExpiresAt.UTC().Format(time.DateOnly), oldTokenEnd(old))
	return nil
}

// oldTokenEnd says when a rotated token stops working.
func oldTokenEnd(old store.Token) string {
	if old.RevokedAt != nil {
		return "The old token is revoked."
	}
	return "The old token keeps working until " + old.ExpiresAt.UTC().Format(time.RFC3339) + "."
}

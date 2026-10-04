package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/hami9/shipyard/internal/client"
)

// Token commands (ADR-0011). list and revoke need the admin scope; rotate
// works with any token and replaces the calling one.

func cmdTokenList(ctx context.Context, e env, c *client.Client, args []string) error {
	if _, err := parse(flag.NewFlagSet("token list", flag.ContinueOnError), args, 0); err != nil {
		return err
	}
	tokens, err := c.ListTokens(ctx)
	if err != nil {
		return err
	}
	rows := make([][]string, len(tokens))
	for i, t := range tokens {
		rows[i] = []string{t.Prefix, t.Name, strings.Join(t.Scopes, ","), day(t.ExpiresAt), day(t.LastUsedAt), t.Status}
	}
	return table(e, "PREFIX\tNAME\tSCOPES\tEXPIRES\tLAST USED\tSTATUS", rows)
}

func day(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.DateOnly)
}

func cmdTokenRevoke(ctx context.Context, e env, c *client.Client, args []string) error {
	pos, err := parse(flag.NewFlagSet("token revoke", flag.ContinueOnError), args, 1)
	if err != nil {
		return err
	}
	t, err := c.RevokeToken(ctx, pos[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Revoked %s (%s).\n", t.Prefix, t.Name)
	return nil
}

// cmdTokenRotate replaces the configured token. A token from the config
// file is replaced there; one from SHIPYARD_TOKEN cannot be, so the new
// token is printed for the operator to store (e.g. as a CI secret).
func cmdTokenRotate(ctx context.Context, e env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("token rotate", flag.ContinueOnError)
	grace := fs.Duration("grace", 0, "")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	r, err := c.RotateSelf(ctx, *grace)
	if err != nil {
		return err
	}
	end := "The old token is revoked."
	if r.Old.RevokedAt == nil && r.Old.ExpiresAt != nil {
		end = "The old token keeps working until " + r.Old.ExpiresAt.UTC().Format(time.RFC3339) + "."
	}
	summary := fmt.Sprintf("Rotated %s to %s (scopes %s, expires %s). %s", r.Old.Prefix, r.New.Prefix,
		strings.Join(r.New.Scopes, ","), day(r.New.ExpiresAt), end)
	if e.getenv(envToken) != "" {
		fmt.Fprintln(e.stdout, r.Token)
		fmt.Fprintf(e.stderr, "%s\n%s is set, so the config file was not changed: store the new token above now; it is shown only once.\n", summary, envToken)
		return nil
	}
	path, err := defaultConfigPath(e.getenv)
	if err == nil {
		var cfg config
		if cfg, err = readConfigFile(path); err == nil {
			cfg.Token = r.Token
			err = saveConfig(path, cfg)
		}
	}
	if err != nil {
		// Never lose the only copy of the new token.
		fmt.Fprintln(e.stdout, r.Token)
		return fmt.Errorf("%s\nsaving it failed: %w; the new token is printed above, store it now", summary, err)
	}
	fmt.Fprintf(e.stdout, "%s\nSaved to %s.\n", summary, path)
	return nil
}

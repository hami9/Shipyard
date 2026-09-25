// Package migrations embeds Shipyard's forward-only SQL migrations.
//
// Rules: files are named NNNN_snake_case.sql with contiguous versions; an
// applied file is never edited or renamed; every change is a new file. The
// runner applies all pending files in one transaction (internal/store).
package migrations

import "embed"

// FS holds every migration file.
//
//go:embed *.sql
var FS embed.FS

package app

import (
	"cmp"
	"slices"
	"strings"
)

// MinRedactLength is the shortest secret value that is redacted: shorter
// values would mangle ordinary output (ADR-0008).
const MinRedactLength = 6

// Redacted replaces a secret value in app output.
const Redacted = "[REDACTED]"

// Redactor hides an app's secret values in its output: best effort, since
// an app can print a secret encoded, split, or in part (ARCHITECTURE §6).
type Redactor struct{ r *strings.Replacer }

// NewRedactor redacts each value of at least MinRedactLength characters.
func NewRedactor(secrets []string) Redactor {
	var vals []string
	for _, s := range secrets {
		if len([]rune(s)) >= MinRedactLength {
			vals = append(vals, s)
		}
	}
	// Longest first, so a secret that contains another is hidden whole.
	slices.SortFunc(vals, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	vals = slices.Compact(vals)
	if len(vals) == 0 {
		return Redactor{}
	}
	pairs := make([]string, 0, 2*len(vals))
	for _, v := range vals {
		pairs = append(pairs, v, Redacted)
	}
	return Redactor{strings.NewReplacer(pairs...)}
}

// Redact returns s with every secret value replaced.
func (r Redactor) Redact(s string) string {
	if r.r == nil {
		return s
	}
	return r.r.Replace(s)
}

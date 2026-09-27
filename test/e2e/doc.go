// Package e2e holds end-to-end tests that drive the real shipyard binaries
// against PostgreSQL, Docker Engine, and a local git server. They use the
// `e2e` build tag and run on the owner's machine with `make test-e2e`, not in
// CI (CLAUDE.md §6).
package e2e

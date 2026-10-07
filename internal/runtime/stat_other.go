//go:build !unix

package runtime

import "io/fs"

// groupOf is unknown off Unix; the edge (and the worker) run only on Linux.
func groupOf(fs.FileInfo) int { return -1 }

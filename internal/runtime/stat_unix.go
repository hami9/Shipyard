//go:build unix

package runtime

import (
	"io/fs"
	"syscall"
)

func groupOf(fi fs.FileInfo) int {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Gid)
	}
	return -1
}

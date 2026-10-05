//go:build linux

package monitor

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"syscall"
)

// StatFS reads the usage of the filesystem holding path, as df does: used
// is total minus free blocks, avail what an unprivileged user may write. A
// path not created yet (a backup directory before the first backup) is
// measured at its nearest existing parent.
func StatFS(path string) (Disk, error) {
	var st syscall.Statfs_t
	for p := path; ; p = filepath.Dir(p) {
		err := syscall.Statfs(p, &st)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) || p == filepath.Dir(p) {
			return Disk{}, fmt.Errorf("statfs %s: %w", p, err)
		}
	}
	bs := uint64(st.Bsize)
	return Disk{Path: path, Size: st.Blocks * bs, Avail: st.Bavail * bs, Used: (st.Blocks - st.Bfree) * bs}, nil
}

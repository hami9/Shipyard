//go:build !linux

package monitor

import "errors"

// StatFS is supported on Linux only, where Shipyard runs.
func StatFS(path string) (Disk, error) {
	return Disk{}, errors.New("disk usage is read on Linux only")
}

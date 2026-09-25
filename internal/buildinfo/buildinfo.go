// Package buildinfo reports which version of Shipyard is running.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Set at link time by the Makefile:
//
//	-ldflags "-X github.com/hami9/shipyard/internal/buildinfo.version=v0.1.0
//	          -X github.com/hami9/shipyard/internal/buildinfo.commit=<sha>"
//
// They are never written at runtime.
var (
	version = "dev"
	commit  = ""
)

// Info describes the running binary.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Modified  bool   `json:"modified"`
	GoVersion string `json:"go_version"`
}

// Get returns build information. When the commit was not injected at link
// time, it falls back to the VCS stamp that `go build` records.
func Get() Info {
	info := Info{Version: version, Commit: commit, GoVersion: runtime.Version()}
	if info.Commit != "" {
		return info
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				info.Commit = s.Value
			case "vcs.modified":
				info.Modified = s.Value == "true"
			}
		}
	}
	return info
}

func (i Info) String() string {
	c := i.Commit
	if len(c) > 12 {
		c = c[:12]
	}
	if c == "" {
		c = "unknown"
	}
	if i.Modified {
		c += "-dirty"
	}
	return fmt.Sprintf("%s (commit %s, %s)", i.Version, c, i.GoVersion)
}

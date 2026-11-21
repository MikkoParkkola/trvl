//go:build !windows

package telemetry

import (
	"os"
	"syscall"
)

// soleLink reports whether path is the only name of its file. A link count it
// cannot read counts as shared, so the caller refuses the value.
func soleLink(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Nlink == 1
}

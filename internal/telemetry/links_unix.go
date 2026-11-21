//go:build !windows

package telemetry

import (
	"os"
	"syscall"
)

// linkCount returns how many names the file had when readStateLinks first
// looked at it (before, the Lstat result). The open is bound to that same file
// by os.SameFile, so this is the count of the file being read. It is taken
// before the open on purpose: replacing or unlinking another name afterwards
// lowers the live count, which would let a linked file pass as unlinked.
func linkCount(_ *os.File, before os.FileInfo) (uint64, bool) {
	st, ok := before.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Nlink), true
}

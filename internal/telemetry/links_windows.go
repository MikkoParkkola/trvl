//go:build windows

package telemetry

import (
	"os"
	"syscall"
)

// linkCount returns how many names the open file f has, asked of its own
// handle. os.Open does not grant FILE_SHARE_DELETE, so no other name of the
// file can be removed while it is open and the count cannot drop mid-read.
func linkCount(f *os.File, _ os.FileInfo) (uint64, bool) {
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &d); err != nil {
		return 0, false
	}
	return uint64(d.NumberOfLinks), true
}

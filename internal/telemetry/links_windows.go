//go:build windows

package telemetry

import (
	"os"
	"syscall"
)

// linkCount returns how many names the open file f has, asked of its own
// handle, so the count belongs to the file being read. Another name can still
// be removed while f is open (CI showed DeleteFile succeeding even though
// os.Open grants no FILE_SHARE_DELETE), so readStateLinks asks right after
// the open; a removal in the instant before that question is not seen.
func linkCount(f *os.File, _ os.FileInfo) (uint64, bool) {
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &d); err != nil {
		return 0, false
	}
	return uint64(d.NumberOfLinks), true
}

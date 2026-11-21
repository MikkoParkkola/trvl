//go:build windows

package telemetry

import (
	"os"
	"syscall"
)

// soleLink reports whether path is the only name of its file. A link count it
// cannot read counts as shared, so the caller refuses the value.
func soleLink(path string) bool {
	// #nosec G304 -- path is a fixed telemetry state file the caller just read.
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &d); err != nil {
		return false
	}
	return d.NumberOfLinks == 1
}

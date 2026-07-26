//go:build windows

package safeexec

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createNoWindow keeps a helper from flashing a console window during an
// ordinary search. It is not in the syscall package.
const createNoWindow = 0x08000000

// harden contains a helper process on Windows.
//
// The Unix defect this package exists for has two halves, and Windows shares
// only one. There is no /dev/tty, and the child's stdin is the null device, so
// a helper has no back channel to prompt the user through: the prompt-leak half
// does not reproduce here. What does reproduce is the second half — killing a
// process does not kill what it spawned, so a helper that starts a daemon and is
// then timed out leaves that daemon running.
//
// The containment itself is installed in contain(), after Start, because a job
// object can only be assigned to a process that already exists.
func harden(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | createNoWindow,
	}
}

// jobs maps a running process to the job object holding it, so Cancel can close
// the handle and take the whole tree down with it.
var jobs sync.Map // pid -> windows.Handle

// contain assigns the started process to a kill-on-close job object, the
// Windows equivalent of the Unix process group. Every descendant created after
// the assignment joins the job, and closing the handle terminates all of them.
//
// Failure is deliberately silent: containment is a hardening measure, and a
// helper that cannot be contained is still bounded by the deadline set in
// Command. Losing the job is worse than having it, but far better than failing
// the search that needed the helper.
func contain(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	job, err := createKillOnCloseJob()
	if err != nil {
		return
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	defer windows.CloseHandle(ph)

	if err := windows.AssignProcessToJobObject(job, ph); err != nil {
		_ = windows.CloseHandle(job)
		return
	}

	pid := cmd.Process.Pid
	jobs.Store(pid, job)
	prev := cmd.Cancel
	cmd.Cancel = func() error {
		if h, ok := jobs.LoadAndDelete(pid); ok {
			// Closing the job terminates every process still assigned to it,
			// the direct child included, so no further signal is needed.
			return windows.CloseHandle(h.(windows.Handle))
		}
		if prev != nil {
			return prev()
		}
		return cmd.Process.Kill()
	}
}

func createKillOnCloseJob() (windows.Handle, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(h)
		return 0, err
	}
	return h, nil
}

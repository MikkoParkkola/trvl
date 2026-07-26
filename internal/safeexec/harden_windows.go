//go:build windows

package safeexec

import (
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createNoWindow keeps a helper from flashing a console window during an
// ordinary search. It is not in the syscall package.
const createNoWindow = 0x08000000

// harden configures a helper process on Windows.
//
// The Unix defect this package exists for has two halves, and Windows shares
// only one. There is no /dev/tty, and the child's stdin is the null device, so
// a helper has no back channel to prompt the user through: the prompt-leak half
// does not reproduce here. What does reproduce is the second half — killing a
// process does not kill what it spawned, so a helper that starts a daemon and is
// then timed out leaves that daemon running.
//
// Everything here is set before Start, because exec.Cmd's cancellation watcher
// reads these fields from another goroutine once the process is running.
// Descendant containment needs a live process and so lives in containment,
// which Output drives.
func harden(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | createNoWindow,
	}
}

// containment holds a kill-on-close job object, the Windows equivalent of the
// Unix process group. Every descendant created after assignment joins the job,
// and closing the handle terminates all of them.
type containment struct {
	job windows.Handle
}

func newContainment() *containment {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return &containment{}
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return &containment{}
	}
	return &containment{job: job}
}

// hold assigns a started process to the job.
//
// A failure here is logged rather than returned. Containment is hardening: a
// helper that cannot be contained is still bounded by the deadline set in
// Command, so failing the search that needed it would trade a small resource
// risk for a certain loss of function. Logging is the compromise — silence
// would mean nobody ever learns the containment is not working.
func (c *containment) hold(p *os.Process) {
	if c == nil || p == nil {
		return
	}
	if c.job == 0 {
		slog.Debug("safeexec: no job object; helper descendants will not be contained", "pid", p.Pid)
		return
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		slog.Debug("safeexec: could not open helper process for containment", "pid", p.Pid, "err", err)
		return
	}
	defer windows.CloseHandle(ph)
	if err := windows.AssignProcessToJobObject(c.job, ph); err != nil {
		slog.Debug("safeexec: could not assign helper to job object", "pid", p.Pid, "err", err)
	}
}

// close terminates anything still in the job and releases the handle. It runs on
// every path, success included: the handle is a kernel resource, and one leaked
// per credential lookup would accumulate for the life of an MCP server.
func (c *containment) close() {
	if c == nil || c.job == 0 {
		return
	}
	_ = windows.CloseHandle(c.job)
	c.job = 0
}

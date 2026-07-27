//go:build unix

package safeexec

import (
	"os"
	"path/filepath"
	"testing"
)

// writeHangingSpawner writes a helper that backgrounds a descendant which touches
// startedPath immediately, would touch survivorPath 3s later, and then hangs. It
// returns the argv to run.
//
// The descendant spawns straight away, with no wait ahead of it. The Windows
// fixture has to delay, because its containment starts at job assignment and the
// assignment follows Start (#526). A process group is set through SysProcAttr
// before the process exists, so here there is no window to spawn into and the
// strict version of the test is the correct one.
func writeHangingSpawner(t *testing.T, dir, startedPath, survivorPath string) ([]string, error) {
	t.Helper()
	bin := filepath.Join(dir, "hangs")
	script := "#!/bin/sh\n" +
		"( echo started >> \"" + startedPath + "\"; sleep 3; echo survived >> \"" + survivorPath + "\" ) &\n" +
		"sleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		return nil, err
	}
	return []string{bin}, nil
}

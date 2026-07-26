//go:build unix

package safeexec

import (
	"os"
	"path/filepath"
	"testing"
)

// writeHangingSpawner writes a helper that backgrounds a descendant which would
// touch survivorPath after 3s, then hangs for 30s. It returns the argv to run.
func writeHangingSpawner(t *testing.T, dir, survivorPath string) ([]string, error) {
	t.Helper()
	bin := filepath.Join(dir, "hangs")
	script := "#!/bin/sh\n( sleep 3; echo survived >> \"" + survivorPath + "\" ) &\nsleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		return nil, err
	}
	return []string{bin}, nil
}

//go:build linux

package atomicjson

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
)

func currentBootFingerprint() string {
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

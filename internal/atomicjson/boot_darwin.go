//go:build darwin

package atomicjson

import (
	"crypto/sha256"
	"encoding/hex"

	"golang.org/x/sys/unix"
)

func currentBootFingerprint() string {
	raw, err := unix.Sysctl("kern.boottime")
	if err != nil || raw == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

//go:build !linux && !darwin && !windows

package atomicjson

// Unknown Unix variants retain the legacy conservative behavior until a
// trustworthy boot identity is available for that platform.
func currentBootFingerprint() string { return "" }

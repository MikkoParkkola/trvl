//go:build windows

package atomicjson

// processAlive always reports live on Windows. os.FindProcess there opens a
// process handle and fails for reasons other than "no such process" — an
// access-denied result on another user's process would be read as "gone" and
// could delete a live writer's temp file. Reporting live means Windows never
// reclaims an orphan automatically; detection and reporting still work.
func processAlive(int) bool { return true }

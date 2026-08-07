package atomicjson

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// tempMarker separates a target file name from the temp-file discriminator.
// It is deliberately unchanged from the original scheme so temp files written
// by older builds are still matched by callers globbing "*.tmp-*".
const tempMarker = ".tmp-"

// tempName builds the temp file name for a target base name. The PID is part
// of the name so an interrupted write can later be attributed to a process.
func tempName(base string, pid int, nonce string) string {
	return base + tempMarker + strconv.Itoa(pid) + "-" + nonce
}

// tempNameWithBoot records which boot created a new temp file. PID reuse after
// reboot is then provable without trusting mutable file timestamps. An empty
// fingerprint falls back to the legacy format and therefore to conservative
// liveness handling.
func tempNameWithBoot(base string, pid int, boot, nonce string) string {
	if boot == "" {
		return tempName(base, pid, nonce)
	}
	return base + tempMarker + strconv.Itoa(pid) + "-b" + boot + "-" + nonce
}

// An Orphan is a temp file left behind in a store directory by a write that
// never reached its rename — almost always a process killed mid-write. It is
// a full copy of the data that was being written, so it may be the only
// surviving version of Target if the kill happened at the wrong moment. That
// is why nothing in this package removes an orphan unless a caller explicitly
// asks for it.
type Orphan struct {
	// Path is the absolute-or-as-given path of the temp file itself.
	Path string
	// Target is the path the temp file would have been renamed to.
	Target string
	// Size is the temp file size in bytes.
	Size int64
	// ModTime is the temp file's last modification time.
	ModTime time.Time
	// PID is the process that created the temp file, or 0 when the name
	// predates PID stamping and ownership cannot be established.
	PID int
	// OwnerLive reports whether a process with PID currently exists. It is
	// true whenever liveness cannot be determined, so an indeterminate answer
	// always protects the file rather than the disk space.
	OwnerLive bool
}

// Age returns how long ago the orphan was last written.
func (o Orphan) Age(now time.Time) time.Duration { return now.Sub(o.ModTime) }

// Reclaimable reports whether removing the orphan is provably safe: its owner
// must be known, that process must be gone, and the file must be older than
// minAge. Anything unknown answers false. It is the only rule by which this
// package will delete a file it did not itself create.
func (o Orphan) Reclaimable(now time.Time, minAge time.Duration) bool {
	if o.PID <= 0 || o.OwnerLive {
		return false
	}
	return o.Age(now) >= minAge
}

// FindOrphans reports the temp files in dir left behind by interrupted atomic
// writes. It does not recurse, does not follow the target files, and never
// modifies anything. A missing dir is not an error: it reports no orphans.
func FindOrphans(dir string) ([]Orphan, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("atomicjson: scan %s: %w", dir, err)
	}

	var orphans []Orphan
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		target, pid, ownerBoot, ok := parseTempMetadata(e.Name())
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			// The file vanished between listing and stat, or is unreadable.
			// Either way there is nothing to report and nothing to clean.
			continue
		}
		orphans = append(orphans, Orphan{
			Path:      filepath.Join(dir, e.Name()),
			Target:    filepath.Join(dir, target),
			Size:      info.Size(),
			ModTime:   info.ModTime(),
			PID:       pid,
			OwnerLive: pid > 0 && bootMayMatch(ownerBoot, currentBootFingerprint()) && processAlive(pid, info.ModTime()),
		})
	}
	return orphans, nil
}

func parseTempMetadata(name string) (target string, pid int, boot string, ok bool) {
	i := strings.LastIndex(name, tempMarker)
	if i <= 0 {
		return "", 0, "", false
	}
	target, suffix := name[:i], name[i+len(tempMarker):]
	if suffix == "" {
		return "", 0, "", false
	}
	// Current scheme: "<pid>-<nonce>". Legacy scheme: "<nonce>" only.
	if dash := strings.Index(suffix, "-"); dash > 0 {
		if p, err := strconv.Atoi(suffix[:dash]); err == nil && p > 0 {
			rest := suffix[dash+1:]
			if bootEnd := strings.Index(rest, "-"); bootEnd > 1 && strings.HasPrefix(rest, "b") {
				return target, p, rest[1:bootEnd], true
			}
			return target, p, "", true
		}
	}
	return target, 0, "", true
}

// bootMayMatch is deliberately asymmetric. A recorded fingerprint that differs
// from the current boot proves the PID was reused and returns false. Missing or
// unavailable data is ambiguous and returns true, preserving the file.
func bootMayMatch(recorded, current string) bool {
	return recorded == "" || current == "" || recorded == current
}

// CleanOptions controls Clean. The zero value is a dry run: it reports what
// would be removed and removes nothing.
type CleanOptions struct {
	// Confirm must be true for any file to be deleted. False is a dry run.
	Confirm bool
	// MinAge is the grace period an orphan must exceed before it is eligible.
	// Zero means no grace period; callers exposing this to operators should
	// pass a real one so a writer that died seconds ago is left alone.
	MinAge time.Duration
	// Now overrides the clock for age comparisons. Zero means time.Now.
	Now time.Time
}

// CleanResult reports the outcome of a Clean pass. Eligible lists the orphans
// that satisfy Orphan.Reclaimable; Removed is empty unless Confirm was set,
// and otherwise equals the subset of Eligible that was successfully deleted.
// Retained lists everything left on disk, which on a dry run is all of them.
type CleanResult struct {
	Eligible []Orphan
	Removed  []Orphan
	Retained []Orphan
}

// Clean removes orphaned temp files in dir, but only those whose creating
// process is known and provably gone, and only when opts.Confirm is set.
// Without Confirm it is a pure report. Temps with an unknown owner, a live
// owner, or an age below opts.MinAge are always retained — a cleanup that
// races a live writer, or that discards the last copy of a file whose owner
// cannot be identified, costs more than the disk it frees.
//
// A PID is only meaningful on the machine and boot that wrote it. PID reuse is
// harmless here — a recycled PID reads as live and the file is retained — but
// the reverse is not: a temp written by another host onto a shared store
// directory, or by a process from a previous boot, can read as dead locally.
// Callers that may run against a network-mounted or multi-host store should
// stay in dry-run mode and report rather than delete.
func Clean(dir string, opts CleanOptions) (CleanResult, error) {
	orphans, err := FindOrphans(dir)
	if err != nil {
		return CleanResult{}, err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	var res CleanResult
	for _, o := range orphans {
		if !o.Reclaimable(now, opts.MinAge) {
			res.Retained = append(res.Retained, o)
			continue
		}
		res.Eligible = append(res.Eligible, o)
		if !opts.Confirm {
			res.Retained = append(res.Retained, o)
			continue
		}
		if err := os.Remove(o.Path); err != nil && !os.IsNotExist(err) {
			res.Retained = append(res.Retained, o)
			continue
		}
		res.Removed = append(res.Removed, o)
	}
	return res, nil
}

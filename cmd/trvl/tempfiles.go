package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/MikkoParkkola/trvl/internal/atomicjson"
)

// tempFilesMinAgeDefault is the grace period below which an orphaned temp file
// is left alone even when its creating process is provably gone.
//
// It is not a safety mechanism against racing a live writer -- Orphan.OwnerLive
// does that, and it fails closed. This exists for the case that liveness cannot
// see: a writer that died seconds ago and whose supervisor is about to retry,
// where the temp is still the freshest copy of the data. An hour is long enough
// that anything still on disk is genuinely abandoned, and short enough that an
// operator clearing space does not have to wait a day.
const tempFilesMinAgeDefault = time.Hour

// tempFilesCmd reports orphaned temp files left by interrupted atomic writes,
// and deletes them only when the operator asks in so many words.
//
// The reporting half and the deleting half are one command on purpose: an
// operator who is about to free disk space should have to look at what they are
// freeing. The default is a report, because an orphan can be the only surviving
// copy of the file it was replacing -- the process died between "write the temp"
// and "rename it over the target" -- so deleting one unprompted can destroy the
// data it was protecting.
func tempFilesCmd() *cobra.Command {
	var dir string
	var minAge time.Duration
	var confirm bool

	cmd := &cobra.Command{
		Use:   "tempfiles",
		Short: "Report orphaned temp files left by interrupted writes",
		Long: "Report temp files left behind in the trvl store by writes that were killed " +
			"before their rename completed.\n\n" +
			"Reports by default and deletes nothing. Each orphan is a full copy of the file " +
			"it was about to replace, so it may be the only surviving version of that file. " +
			"Pass --delete to remove the ones whose creating process is provably gone.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("locate home directory: %w", err)
				}
				dir = filepath.Join(home, ".trvl")
			}
			res, err := atomicjson.Clean(dir, atomicjson.CleanOptions{
				Confirm: confirm,
				MinAge:  minAge,
			})
			if err != nil {
				return err
			}
			writeTempFilesReport(cmd, dir, res, confirm, minAge)
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "store directory to scan (default ~/.trvl)")
	cmd.Flags().DurationVar(&minAge, "min-age", tempFilesMinAgeDefault, "leave orphans younger than this alone")
	// Named --delete rather than --force or --yes: the flag should say what it
	// does, so it cannot be pasted from another command's habit.
	cmd.Flags().BoolVar(&confirm, "delete", false, "actually delete reclaimable orphans (default: report only)")

	return cmd
}

// writeTempFilesReport prints what was found and what, if anything, was done.
//
// It reports every orphan with its size and age, including the ones that are not
// reclaimable, and says why each was kept. An operator chasing 149 MB of disk
// needs to know that the files are there and that trvl is deliberately not
// touching them; a report that silently omitted the retained ones would read as
// "nothing to see here" on exactly the machine that has the problem.
func writeTempFilesReport(cmd *cobra.Command, dir string, res atomicjson.CleanResult, confirm bool, minAge time.Duration) {
	out := cmd.OutOrStdout()
	now := time.Now()

	total := len(res.Removed) + len(res.Retained)
	if total == 0 {
		_, _ = fmt.Fprintf(out, "No orphaned temp files in %s\n", dir)
		return
	}

	var bytes int64
	for _, o := range res.Removed {
		bytes += o.Size
	}
	for _, o := range res.Retained {
		bytes += o.Size
	}
	_, _ = fmt.Fprintf(out, "Orphaned temp files in %s: %d, %s total\n\n", dir, total, formatTempSize(bytes))

	for _, o := range res.Removed {
		_, _ = fmt.Fprintf(out, "  deleted  %s  %s  age %s\n", filepath.Base(o.Path), formatTempSize(o.Size), formatTempAge(o.Age(now)))
	}
	for _, o := range res.Retained {
		_, _ = fmt.Fprintf(out, "  kept     %s  %s  age %s  (%s)\n",
			filepath.Base(o.Path), formatTempSize(o.Size), formatTempAge(o.Age(now)), tempRetainReason(o, now, minAge, confirm))
	}

	_, _ = fmt.Fprintln(out)
	switch {
	case confirm:
		_, _ = fmt.Fprintf(out, "Deleted %d of %d.\n", len(res.Removed), total)
	case len(res.Eligible) > 0:
		_, _ = fmt.Fprintf(out, "Nothing was deleted. %d of %d can be reclaimed; re-run with --delete to remove them.\n", len(res.Eligible), total)
	default:
		_, _ = fmt.Fprintln(out, "Nothing was deleted, and nothing here can be reclaimed safely.")
	}
}

// tempRetainReason explains one retained orphan in the operator's terms.
func tempRetainReason(o atomicjson.Orphan, now time.Time, minAge time.Duration, confirm bool) string {
	switch {
	case o.PID <= 0:
		return "owner unknown, may be the only copy"
	case o.OwnerLive:
		return fmt.Sprintf("owner pid %d still running", o.PID)
	case o.Age(now) < minAge:
		return fmt.Sprintf("younger than %s", formatTempAge(minAge))
	case !confirm:
		return "reclaimable, dry run"
	default:
		return "delete failed"
	}
}

func formatTempSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func formatTempAge(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}

func init() {
	rootCmd.AddCommand(tempFilesCmd())
}

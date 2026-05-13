package main

// `trvl self-update` — user-initiated update of the running trvl binary.
//
// Behavior depends on the install method (detected via
// selfupdate.DetectInstallMethod):
//
//   - dev: print informational message, exit 0.
//   - brew / go / npm: print the channel-correct upgrade hint and exit
//     0 without modifying anything. We REFUSE to overwrite a binary
//     under an external package manager's manifest.
//   - standalone: download the latest release tarball, verify SHA-256
//     and ML-DSA-65 signature, atomically replace the running binary.
//   - unclassified: treat exactly like standalone-but-cautious — print
//     advice and abort. The user can re-run with --force-standalone if
//     they're sure the binary is a hand-extracted tarball.
//
// Flags:
//   --check        Print "update available" status; do not modify anything.
//   --version=X    Pin the target version (default: latest from GH).
//   --force-standalone
//                  Treat an unclassified install as standalone. Useful
//                  when the user knows their binary is a hand-extracted
//                  tarball that the detector failed to classify.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/MikkoParkkola/trvl/internal/selfupdate"
)

var (
	selfUpdateCheckOnly       bool
	selfUpdateTargetVersion   string
	selfUpdateForceStandalone bool
)

var selfUpdateCmd = &cobra.Command{
	Use:   "self-update",
	Short: "Update trvl to the latest release",
	Long: `Update trvl to the latest release. Behavior depends on how trvl was
installed:

  - Homebrew    — prints "brew upgrade trvl" and exits.
  - go install  — prints "go install ...@latest" and exits.
  - npm         — prints "npm install -g trvl-mcp@latest" and exits.
  - standalone  — downloads the latest release tarball from GitHub,
                  verifies its SHA-256 against the published checksums.txt
                  AND its ML-DSA-65 (post-quantum) signature against the
                  trust anchor embedded in this binary, then atomically
                  replaces the running binary on disk.
  - dev build   — no-op.

The verify-then-replace path is fail-safe: any verification error aborts
the update and leaves the on-disk binary untouched.`,
	RunE: runSelfUpdate,
}

func init() {
	selfUpdateCmd.Flags().BoolVar(&selfUpdateCheckOnly, "check", false,
		"Only check whether an update is available; do not download or replace.")
	selfUpdateCmd.Flags().StringVar(&selfUpdateTargetVersion, "version", "",
		"Pin the target version (default: latest stable from GitHub).")
	selfUpdateCmd.Flags().BoolVar(&selfUpdateForceStandalone, "force-standalone", false,
		"Treat an unclassified install method as standalone (use only if you know the binary is a hand-extracted tarball).")
	rootCmd.AddCommand(selfUpdateCmd)
}

func runSelfUpdate(cmd *cobra.Command, args []string) error {
	method := selfupdate.DetectInstallMethod(Version)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Install method: %s\n", method)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Current version: %s\n", Version)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Trust anchor (ML-DSA-65 fingerprint): %s\n",
		selfupdate.MLDSA65PubkeyFingerprint())

	// Channel-specific short-circuits: never overwrite a binary tracked
	// by an external package manager.
	switch method {
	case selfupdate.InstallMethodDev:
		_, _ = fmt.Fprintln(cmd.OutOrStdout(),
			"This is a dev build — no self-update available. Rebuild from source with your local toolchain.")
		return nil
	case selfupdate.InstallMethodBrew, selfupdate.InstallMethodGo, selfupdate.InstallMethodNpm:
		hint := method.UpgradeHint()
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"trvl was installed via %s. The %s package manager owns this binary; run:\n\n  %s\n\n",
			method, method, hint)
		return nil
	case selfupdate.InstallMethodUnclassified:
		if !selfUpdateForceStandalone {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
				"Could not classify your install method. Refusing to auto-replace the binary.\n"+
					"If you know your binary is a hand-extracted tarball, re-run with --force-standalone.")
			return fmt.Errorf("self-update refused: install method unclassified")
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(),
			"Proceeding under --force-standalone (caller asserts a hand-extracted tarball install).")
	}

	// At this point method is Standalone (or Unclassified + force-standalone).
	ctx, cancel := context.WithTimeout(cmd.Context(), 90*time.Second)
	defer cancel()

	// Resolve the target version. --version pins; otherwise hit the
	// daily checker and use whatever it tells us is latest.
	target := selfUpdateTargetVersion
	releaseURL := ""
	if target == "" {
		c, err := selfupdate.NewChecker(Version)
		if err != nil {
			return fmt.Errorf("self-update: init checker: %w", err)
		}
		info, err := c.Check(ctx, false)
		if err != nil {
			return fmt.Errorf("self-update: check latest: %w", err)
		}
		if info.LatestVersion == "" {
			return fmt.Errorf("self-update: could not determine latest version (network failure?)")
		}
		target = info.LatestVersion
		releaseURL = info.ReleaseURL
		if !info.UpdateAvailable {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"You're already running the latest version (v%s).\n", Version)
			return nil
		}
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Target version: v%s\n", target)
	if releaseURL != "" {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Release notes: %s\n", releaseURL)
	}

	if selfUpdateCheckOnly {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"--check: an update to v%s is available. Re-run without --check to apply it.\n", target)
		return nil
	}

	// Resolve the binary path. EvalSymlinks so we replace the underlying
	// file rather than the shim.
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("self-update: resolve executable path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Replacing %s ...\n", exePath)
	updater := selfupdate.NewUpdater()
	dst, err := updater.PerformUpdate(ctx, target, exePath)
	if err != nil {
		return fmt.Errorf("self-update failed: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"Updated %s to v%s. Run `trvl version` to confirm.\n", dst, target)
	return nil
}

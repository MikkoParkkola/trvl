package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/MikkoParkkola/trvl/internal/selfupdate"
)

// Version is set at build time via -ldflags. Production releases are
// stamped via goreleaser (.goreleaser.yaml ldflags), so the dev string
// "dev" only ever appears for `go build` / `go run` invocations.
var Version = "dev"

var versionJSON bool

// VersionReport is the machine-readable shape returned by
// `trvl version --json`. Stable contract: tools script against this.
//
// Fields:
//   - version: the build-time Version stamp (e.g. "1.2.0", or "dev").
//   - install_method: how trvl was installed; one of
//     {dev, brew, go, npm, standalone, unclassified}.
//   - upgrade_hint: the channel-correct upgrade command for this user;
//     empty for dev / unclassified.
//   - supports_in_place_replace: whether `trvl self-update` will
//     attempt to overwrite the binary on this install method.
//   - mldsa_trust_anchor: first 16 hex chars of the embedded ML-DSA-65
//     pubkey. Users can spot-check this matches the published value
//     before allowing a self-update swap.
//   - latest_known_version: the most-recent version this binary has
//     seen via the daily background check; empty if no cache yet.
//   - update_available: whether latest_known_version is strictly newer
//     than version (per semver); always false for dev builds.
type VersionReport struct {
	Version                string `json:"version"`
	InstallMethod          string `json:"install_method"`
	UpgradeHint            string `json:"upgrade_hint,omitempty"`
	SupportsInPlaceReplace bool   `json:"supports_in_place_replace"`
	MLDSATrustAnchor       string `json:"mldsa_trust_anchor"`
	LatestKnownVersion     string `json:"latest_known_version,omitempty"`
	UpdateAvailable        bool   `json:"update_available"`
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print trvl version",
	Long: `Print trvl version. By default emits one line with the version string.
Pass --json for a structured report including the install method,
channel-correct upgrade hint, ML-DSA-65 trust-anchor fingerprint, and
the most recent version observed by the daily background update check.`,
	Run: func(cmd *cobra.Command, args []string) {
		if !versionJSON {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "trvl %s\n", Version)
			return
		}
		report := buildVersionReport(Version)
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	},
}

// buildVersionReport composes the structured version report. Pure
// function over Version + the on-disk update-check cache + the embedded
// trust anchor — no network calls.
func buildVersionReport(currentVer string) VersionReport {
	method := selfupdate.DetectInstallMethod(currentVer)
	rep := VersionReport{
		Version:                currentVer,
		InstallMethod:          method.String(),
		UpgradeHint:            method.UpgradeHint(),
		SupportsInPlaceReplace: method.SupportsInPlaceReplace(),
		MLDSATrustAnchor:       selfupdate.MLDSA65PubkeyFingerprint(),
	}
	if info := selfupdate.LoadCachedInfo(); info.LatestVersion != "" {
		rep.LatestKnownVersion = info.LatestVersion
		rep.UpdateAvailable = info.UpdateAvailable
	}
	return rep
}

func init() {
	versionCmd.Flags().BoolVar(&versionJSON, "json", false,
		"Emit a structured JSON report instead of the plain version string.")
	rootCmd.AddCommand(versionCmd)
}

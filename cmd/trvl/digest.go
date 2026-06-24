package main

import (
	"fmt"

	"github.com/MikkoParkkola/trvl/internal/dealradar"
	"github.com/MikkoParkkola/trvl/internal/los"
	"github.com/MikkoParkkola/trvl/internal/mailer"
	"github.com/spf13/cobra"
)

// schedulerLabel is the stable scheduler job identifier (launchd Label,
// systemd unit name, cron tag). It must match the committed launchd plist so
// install/uninstall target the same job.
const schedulerLabel = "com.mikkoparkkola.trvl.dealradar"

// digestCmd wires the daily deal-radar digest into the CLI. The core digest
// build + Gmail send is pure Go and runs on every platform; only the
// `--install`/`--uninstall` scheduler paths are platform-specific (launchd on
// macOS, systemd/cron on Linux, a graceful manual-cron message elsewhere).
func digestCmd() *cobra.Command {
	var (
		dryRun    bool
		install   bool
		uninstall bool
		currency  string
	)

	cmd := &cobra.Command{
		Use:   "digest",
		Short: "Build and email the daily deal-radar digest",
		Long: `Aggregate the savings surfaced by trvl's value engines into a daily
deal-radar digest and email it to yourself over Gmail.

Set TRVL_GMAIL_USER and TRVL_GMAIL_APP_PASSWORD (a Gmail app password) to send.
Use --dry-run to print the digest to stdout without sending.

The digest build and send are cross-platform (pure Go). Scheduling it to run
every morning at 08:00 is platform-specific:

  trvl digest --install      # macOS: launchd · Linux: systemd timer or cron
  trvl digest --uninstall    # remove the scheduled job

On platforms without a built-in installer the command fails gracefully and
prints the manual cron line you can add yourself.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch {
			case install:
				return installScheduler()
			case uninstall:
				return uninstallScheduler()
			default:
				return runDigest(cmd, dryRun, currency)
			}
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the digest to stdout instead of sending")
	cmd.Flags().BoolVar(&install, "install", false, "Install the daily 08:00 scheduler (platform-specific)")
	cmd.Flags().BoolVar(&uninstall, "uninstall", false, "Uninstall the daily scheduler (platform-specific)")
	cmd.Flags().StringVar(&currency, "currency", "EUR", "Currency code for savings amounts")
	return cmd
}

// collectDigest gathers value-engine outputs into a digest. Pure aside from
// the engine calls it delegates to. Today it surfaces length-of-stay flips;
// hotel-value and points engines append dealradar.Item sets here as they are
// wired in.
func collectDigest(currency string) dealradar.Digest {
	// Placeholder data source: in production this reads the user's saved
	// watches/quotes. Kept empty-safe so a no-deal morning still renders a
	// valid (empty) digest rather than erroring.
	var flips []los.Flip
	return dealradar.BuildDigest(dealradar.FromFlips(currency, flips))
}

func runDigest(cmd *cobra.Command, dryRun bool, currency string) error {
	d := collectDigest(currency)
	if dryRun {
		fmt.Fprint(cmd.OutOrStdout(), d.Render())
		return nil
	}
	m, err := mailer.NewFromEnv()
	if err != nil {
		return err
	}
	if _, err := m.SendDigest(d.Subject(), d.Render()); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "deal-radar digest sent (%d deals)\n", len(d.Items))
	return nil
}

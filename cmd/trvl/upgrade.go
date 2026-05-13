package main

import (
	"fmt"
	"os"

	"github.com/MikkoParkkola/trvl/internal/upgrade"
	"github.com/spf13/cobra"
)

func upgradeCmd() *cobra.Command {
	var (
		dryRun bool
		quiet  bool
	)

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Run post-upgrade migrations and show what's new",
		Long: `Run post-upgrade migrations after a version bump.

This command is normally called automatically on first launch after an
upgrade, but you can run it manually to re-apply migrations or inspect
what changed.

Examples:
  trvl upgrade
  trvl upgrade --dry-run
  trvl upgrade --quiet`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := upgrade.RunUpgrade(Version, "", dryRun)
			if err != nil {
				return err
			}

			if quiet {
				return nil
			}

			msg := upgrade.WhatsNew(r)
			if msg != "" {
				if dryRun {
					_, _ = fmt.Fprintln(os.Stderr, "[dry-run]")
				}
				fmt.Println(msg)
			} else if r.FreshInstall {
				fmt.Printf("trvl v%s — fresh install, stamp created.\n", r.NewVersion)
			} else if r.OldVersion == r.NewVersion {
				fmt.Printf("trvl %s — already up to date.\n", r.NewVersion)
			}

			if r.MigrationsApplied > 0 && dryRun {
				_, _ = fmt.Fprintf(os.Stderr, "%d migration(s) would be applied.\n", r.MigrationsApplied)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would change without writing")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress all output (for scripted post_install)")

	return cmd
}

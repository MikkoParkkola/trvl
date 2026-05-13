package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/watch"
	"github.com/spf13/cobra"
)

type watchDaemonTicker interface {
	Chan() <-chan time.Time
	Stop()
}

type realWatchDaemonTicker struct {
	*time.Ticker
}

func (t realWatchDaemonTicker) Chan() <-chan time.Time {
	return t.C
}

func newRealWatchDaemonTicker(interval time.Duration) watchDaemonTicker {
	return realWatchDaemonTicker{Ticker: time.NewTicker(interval)}
}

func watchDaemonCmd() *cobra.Command {
	var (
		interval time.Duration
		runNow   bool
	)

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run watch checks on a schedule until stopped",
		Long: `Run price checks for all saved watches on a fixed interval.

Examples:
  trvl watch daemon
  trvl watch daemon --every 30m
  trvl watch daemon --every 6h --run-now=false`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			notifier := &watch.Notifier{
				Out:      os.Stdout,
				UseColor: models.UseColor,
				Desktop:  true,
			}

			return runWatchDaemon(ctx, os.Stdout, interval, runNow, func(ctx context.Context) (int, error) {
				return runWatchCheckCycleWithRooms(ctx, &liveChecker{}, &liveRoomChecker{}, notifier)
			}, newRealWatchDaemonTicker)
		},
	}

	cmd.Flags().DurationVar(&interval, "every", 6*time.Hour, "Polling interval (e.g. 30m, 6h)")
	cmd.Flags().BoolVar(&runNow, "run-now", true, "Run a check immediately before waiting for the first interval")

	return cmd
}

func runWatchCheckCycleWithRooms(ctx context.Context, checker watch.PriceChecker, roomChecker watch.RoomChecker, notifier *watch.Notifier) (int, error) {
	store, err := watch.DefaultStore()
	if err != nil {
		return 0, err
	}
	if err := store.Load(); err != nil {
		return 0, err
	}

	watches := store.List()
	if len(watches) == 0 {
		return 0, nil
	}

	checkCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	notifier.NotifyAll(watch.CheckAllWithRoomsAndWebhookContext(checkCtx, ctx, store, checker, roomChecker))
	return len(watches), nil
}

func runWatchDaemon(
	ctx context.Context,
	out io.Writer,
	interval time.Duration,
	runNow bool,
	runCycle func(context.Context) (int, error),
	newTicker func(time.Duration) watchDaemonTicker,
) error {
	if interval <= 0 {
		return fmt.Errorf("watch interval must be greater than zero")
	}
	if runCycle == nil {
		return fmt.Errorf("watch daemon requires a check function")
	}
	if newTicker == nil {
		newTicker = newRealWatchDaemonTicker
	}

	_, _ = fmt.Fprintf(out, "Starting watch daemon (every %s). Press Ctrl-C to stop.\n", interval)

	executeCycle := func(prefix string) {
		count, err := runCycle(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(out, "%s watch check failed: %v\n", prefix, err)
			return
		}
		if count == 0 {
			_, _ = fmt.Fprintf(out, "%s no active watches. Waiting for the next interval.\n", prefix)
		}
	}

	if runNow {
		executeCycle("Initial:")
	}

	ticker := newTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_, _ = fmt.Fprintln(out, "Watch daemon stopped.")
			return nil
		case <-ticker.Chan():
			executeCycle("Scheduled:")
		}
	}
}

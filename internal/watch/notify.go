package watch

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/safeexec"
)

// Notifier formats and delivers price check results.
type Notifier struct {
	Out      io.Writer
	UseColor bool
	Desktop  bool // attempt macOS desktop notifications
}

// Notify prints a check result to the writer with color coding.
// Green = price dropped, Red = price increased, bold alert if below threshold.
func (n *Notifier) Notify(r CheckResult) {
	// Round 22: this warning used to sit below the room dispatch, so any
	// room watch returned at the "IsRoomWatch" branch immediately below
	// before ever reaching it -- a currency change that wiped a room
	// watch's thresholds (check_room.go) produced no notice at all. Surface
	// it first, unconditionally, before either dispatch path can return.
	// Found by GPT second-opinion review, 2026-07-30 (round 22).
	if r.AlertsClearedByCurrencyChange {
		route := notifyRoute(r.Watch)
		line := fmt.Sprintf("CURRENCY CHANGED  %s  now quoted in %s -- your price alert threshold was cleared, re-watch to set a new one",
			route, r.Currency)
		_, _ = fmt.Fprintln(n.Out, n.yellow(line))
		if n.Desktop {
			n.desktopNotify(
				"trvl: Alert Threshold Cleared",
				fmt.Sprintf("%s now quoted in %s -- re-watch to set a new price alert", route, r.Currency),
			)
		}
	}

	// Dispatch room watches to their own formatter.
	if r.Watch.IsRoomWatch() {
		n.notifyRoom(r)
		return
	}

	if r.Error != nil {
		_, _ = fmt.Fprintf(n.Out, "%s  %s -> %s  %s\n",
			n.red("ERR"),
			r.Watch.Origin, r.Watch.Destination,
			r.Error,
		)
		return
	}

	if r.NewPrice == 0 {
		_, _ = fmt.Fprintf(n.Out, "%s  %s -> %s  no price data\n",
			n.yellow("---"),
			r.Watch.Origin, r.Watch.Destination,
		)
		return
	}

	route := notifyRoute(r.Watch)
	priceStr := fmt.Sprintf("%.0f %s", r.NewPrice, r.Currency)

	if r.LastMinuteDeal {
		line := fmt.Sprintf("LAST-MINUTE  %s  %s (%.1f%% below last seen)",
			route, priceStr, r.LastMinuteDiscountPercent)
		_, _ = fmt.Fprintln(n.Out, n.green(line))
		if url := buildBookingURL(r.Watch); url != "" {
			_, _ = fmt.Fprintf(n.Out, "      Book: %s\n", url)
		}
		if n.Desktop {
			n.desktopNotify(
				"trvl: Last-Minute Hotel Deal!",
				fmt.Sprintf("%s %s — %.1f%% below last seen", route, priceStr, r.LastMinuteDiscountPercent),
			)
		}
		return
	}

	// Below-threshold alert.
	if r.BelowGoal {
		line := fmt.Sprintf("DEAL  %s  %s (below %.0f %s goal!)",
			route, priceStr, r.Watch.BelowPrice, r.Currency)
		_, _ = fmt.Fprintln(n.Out, n.green(line))

		if r.Watch.DepartDate != "" {
			url := buildBookingURL(r.Watch)
			_, _ = fmt.Fprintf(n.Out, "      Book: %s\n", url)
		}

		if n.Desktop {
			n.desktopNotify(
				"trvl: Price Alert!",
				fmt.Sprintf("%s %s — below your %.0f %s goal",
					route, priceStr, r.Watch.BelowPrice, r.Currency),
			)
		}
		return
	}

	// Proactive price-drop alert (innovation #6: pull -> push). Fires when the
	// fare falls past the configured threshold below the captured baseline.
	if r.PriceDropAlert {
		line := fmt.Sprintf("PRICE DROP  %s  %s (%.1f%% below baseline %.0f %s)",
			route, priceStr, r.AlertDropPercent, r.AlertBaseline, r.Currency)
		_, _ = fmt.Fprintln(n.Out, n.green(line))

		if r.Watch.DepartDate != "" {
			if url := buildBookingURL(r.Watch); url != "" {
				_, _ = fmt.Fprintf(n.Out, "      Book: %s\n", url)
			}
		}

		if n.Desktop {
			n.desktopNotify(
				"trvl: Price Drop!",
				fmt.Sprintf("%s %s — %.1f%% below baseline", route, priceStr, r.AlertDropPercent),
			)
		}
		return
	}

	// Regular price report with change indicator.
	var changeStr string
	if r.PrevPrice > 0 {
		diff := r.PriceDrop
		if diff < 0 {
			changeStr = n.green(fmt.Sprintf(" (%.0f)", diff))
		} else if diff > 0 {
			changeStr = n.red(fmt.Sprintf(" (+%.0f)", diff))
		} else {
			changeStr = " (unchanged)"
		}
	}

	lowest := ""
	if r.Watch.LowestPrice > 0 && r.Watch.LowestPrice < r.NewPrice {
		lowest = fmt.Sprintf("  lowest: %.0f", r.Watch.LowestPrice)
	}

	// Actionable advice based on price movement.
	advice := ""
	if r.PrevPrice > 0 {
		if r.PriceDrop < -r.PrevPrice*0.3 {
			// 30%+ drop — likely error fare or flash sale.
			advice = n.green("  ⚡ big drop — error fare or flash sale? Book fast!")
		} else if r.PriceDrop < 0 {
			// Normal drop — campaign, competition, demand shift.
			advice = n.green("  ↓ price dropped — good time to book")
		} else if r.PriceDrop > 0 && r.Watch.Type == "flight" {
			// Flight prices trending up — normal closer to departure.
			advice = n.red("  ↑ trending up — consider booking soon")
		}
	}

	_, _ = fmt.Fprintf(n.Out, " %s  %s  %s%s%s%s\n",
		strings.ToUpper(r.Watch.Type[:1])+r.Watch.Type[1:],
		route, priceStr, changeStr, lowest, advice,
	)
}

func notifyRoute(w Watch) string {
	if w.Type == "hotel" {
		if w.HotelName != "" {
			return w.HotelName
		}
		return w.Destination
	}
	return fmt.Sprintf("%s -> %s", w.Origin, w.Destination)
}

// notifyRoom prints a room availability check result.
func (n *Notifier) notifyRoom(r CheckResult) {
	hotel := r.Watch.HotelName
	keywords := strings.Join(r.Watch.RoomKeywords, ", ")

	if r.Error != nil {
		_, _ = fmt.Fprintf(n.Out, "%s  Room watch %s [%s]  %s\n",
			n.red("ERR"), hotel, keywords, r.Error)
		return
	}

	if !r.RoomFound {
		_, _ = fmt.Fprintf(n.Out, "%s  Room watch %s [%s]  no matching rooms available\n",
			n.yellow("---"), hotel, keywords)
		return
	}

	// Room found.
	for _, m := range r.RoomMatches {
		priceStr := ""
		if m.Price > 0 {
			priceStr = fmt.Sprintf("  %.0f %s", m.Price, m.Currency)
		}
		provider := ""
		if m.Provider != "" {
			provider = fmt.Sprintf(" via %s", m.Provider)
		}

		line := fmt.Sprintf("ROOM AVAILABLE  %s: %s%s%s",
			hotel, m.Name, priceStr, provider)
		_, _ = fmt.Fprintln(n.Out, n.green(line))
	}

	if n.Desktop {
		msg := fmt.Sprintf("%s: %d matching room(s) found [%s]",
			hotel, len(r.RoomMatches), keywords)
		if r.NewPrice > 0 {
			msg += fmt.Sprintf(" from %.0f %s", r.NewPrice, r.Currency)
		}
		n.desktopNotify("trvl: Room Available!", msg)
	}
}

// NotifyAll prints results for all checks.
func (n *Notifier) NotifyAll(results []CheckResult) {
	for _, r := range results {
		n.Notify(r)
	}
}

func buildBookingURL(w Watch) string {
	switch w.Type {
	case "flight":
		return fmt.Sprintf("https://www.google.com/travel/flights?q=Flights+to+%s+from+%s+on+%s",
			w.Destination, w.Origin, w.DepartDate)
	case "hotel":
		dates := w.DepartDate
		if w.ReturnDate != "" {
			dates += "," + w.ReturnDate
		}
		return fmt.Sprintf("https://www.google.com/travel/hotels/%s?dates=%s",
			w.Destination, dates)
	default:
		return ""
	}
}

func (n *Notifier) green(s string) string {
	if !n.UseColor {
		return s
	}
	return "\033[32m" + s + "\033[0m"
}

func (n *Notifier) red(s string) string {
	if !n.UseColor {
		return s
	}
	return "\033[31m" + s + "\033[0m"
}

func (n *Notifier) yellow(s string) string {
	if !n.UseColor {
		return s
	}
	return "\033[33m" + s + "\033[0m"
}

// desktopNotify sends a native desktop notification. Best-effort by contract:
// callers ignore failures, so it never returns an error or blocks. When the
// platform's native channel is unavailable it logs at debug level rather than
// failing silently, so the degradation is observable in logs.
func (n *Notifier) desktopNotify(title, message string) {
	desktopNotifyDispatch(runtime.GOOS, title, message)
}

// desktopNotifyDispatch routes a desktop notification to the platform-native
// channel. It is factored out (taking goos explicitly) so the dispatch logic is
// testable without depending on the host OS, mirroring providers.defaultOpenURL.
func desktopNotifyDispatch(goos, title, message string) {
	// Bounded and detached like every other helper trvl shells out to (#507).
	// This one runs inside the price-watch daemon, where the cost of a hang is
	// not a slow search but a watch loop that silently stops checking: on macOS
	// `osascript` can block on a notification-permission dialog or a locked
	// display, and nothing here is waiting to answer it. A notification is
	// fire-and-forget, so a few seconds is generous.
	ctx := context.Background()
	switch goos {
	case "darwin":
		script := fmt.Sprintf(`display notification %q with title %q`, message, title)
		if err := runNotifier(ctx, "osascript", "-e", script); err != nil {
			slog.Debug("desktop notification unavailable", "goos", goos, "channel", "osascript", "err", err)
		}
	case "linux":
		// notify-send is the standard libnotify CLI on Linux desktops.
		if err := runNotifier(ctx, "notify-send", title, message); err != nil {
			slog.Debug("desktop notification unavailable", "goos", goos, "channel", "notify-send", "err", err)
		}
	case "windows":
		// Best-effort balloon via PowerShell, no third-party module required.
		ps := fmt.Sprintf(
			`[reflection.assembly]::LoadWithPartialName('System.Windows.Forms') | Out-Null; `+
				`$n = New-Object System.Windows.Forms.NotifyIcon; `+
				`$n.Icon = [System.Drawing.SystemIcons]::Information; `+
				`$n.BalloonTipTitle = %q; $n.BalloonTipText = %q; `+
				`$n.Visible = $true; $n.ShowBalloonTip(5000)`,
			title, message)
		if err := runNotifier(ctx, "powershell", "-NoProfile", "-Command", ps); err != nil {
			slog.Debug("desktop notification unavailable", "goos", goos, "channel", "powershell", "err", err)
		}
	default:
		slog.Debug("desktop notification unavailable", "goos", goos, "channel", "none")
	}
}

// notifyTimeout bounds a desktop-notification helper. Generous for a call that
// should return immediately, short enough that a wedged one cannot pause the
// watch loop for long.
const notifyTimeout = 5 * time.Second

// runNotifier runs a notification helper bounded and terminal-detached.
func runNotifier(ctx context.Context, name string, args ...string) error {
	cmd, _, cancel := safeexec.Command(ctx, notifyTimeout, name, args...)
	defer cancel()
	_, err := safeexec.Output(cmd)
	return err
}

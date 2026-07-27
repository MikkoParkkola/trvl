package ground

// The Trainline helper-assisted fallback: when the ordinary HTTP client is met
// with a bot wall, this replays the request through an external helper carrying
// the user's own browser cookies. Split out of trainline.go to keep that file
// under the repo's 800-line limit; it is a self-contained fallback path with its
// own budget and its own failure modes.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// trainlineViaCurl calls /api/journey-search/ using the system curl binary with
// a cookie jar. macOS curl uses BoringSSL / Secure Transport which produces a
// browser-like TLS ClientHello that often passes Datadome's TLS fingerprint
// check. We first visit the homepage with curl to seed the cookie jar (so the
// datadome cookie is associated with the same TLS session), then POST to the API.
func trainlineViaCurl(ctx context.Context, fromID, toID, date, currency string) ([]models.GroundRoute, error) {
	// Bound the whole helper-assisted attempt. The helper applies no timeout of
	// its own unless told to, so an unbounded context meant a stalled connection
	// could hang a rail search indefinitely — the same defect as #507 wearing a
	// different hat. sncf.go already bounds its equivalent at 35s; this path was
	// missed. The per-invocation --max-time below is belt and braces for a
	// process that ignores cancellation.
	ctx, cancelBudget := context.WithTimeout(ctx, trainlineHelperBudget)
	defer cancelBudget()

	dateTime, err := models.ParseDate(date)
	if err != nil {
		return nil, fmt.Errorf("trainlineViaCurl invalid date %q: %w", date, err)
	}
	departureISO := dateTime.Add(6 * time.Hour).Format("2006-01-02T15:04:05")

	originURN := trainlineURN(fromID)
	destURN := trainlineURN(toID)

	reqBody := trainlineJourneySearchRequest{
		Passengers:              []trainlinePassenger{{DateOfBirth: "1996-01-01", CardIDs: []any{}}},
		IsEurope:                true,
		Cards:                   []any{},
		Type:                    "single",
		MaximumJourneys:         5,
		IncludeRealtime:         true,
		TransportModes:          []string{"mixed"},
		DirectSearch:            false,
		Composition:             []string{"through", "interchangeSplit"},
		AutoApplyCorporateCodes: false,
		Origin:                  originURN,
		Destination:             destURN,
		TransitDefinitions: []trainlineTransitDef{
			{
				Direction:   "outward",
				Origin:      originURN,
				Destination: destURN,
				JourneyDate: trainlineJourneyDate{
					Type: "departAfter",
					Time: departureISO,
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("trainlineViaCurl marshal: %w", err)
	}

	// Common browser-like headers shared between the seed and API requests.
	commonHeaders := []string{
		"-H", "Accept-Language: en-GB,en;q=0.9",
		"-H", `sec-ch-ua: "Chromium";v="133", "Not(A:Brand";v="99"`,
		"-H", "sec-ch-ua-mobile: ?0",
		"-H", `sec-ch-ua-platform: "macOS"`,
		"-H", "User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36",
	}

	// Step 1: Seed the cookie jar by visiting the homepage so Datadome sets its
	// cookie bound to this exact curl TLS session.
	cookieJarFile := fmt.Sprintf("/tmp/trainline-cookies-%d.txt", time.Now().UnixNano())
	defer func() { _ = os.Remove(cookieJarFile) }()
	seedArgs := append([]string{
		"-s", "--http2",
		"--max-time", trainlineHelperMaxTime,
		"-L",                // follow redirects
		"-c", cookieJarFile, // write cookies
		"-b", cookieJarFile, // send cookies
		"-H", "Accept: text/html,application/xhtml+xml",
		"-H", "sec-fetch-dest: document",
		"-H", "sec-fetch-mode: navigate",
		"-H", "sec-fetch-site: none",
		"https://www.thetrainline.com",
		"-o", "/dev/null",
	}, commonHeaders...)

	seedCmd := exec.CommandContext(ctx, "curl", seedArgs...)
	if seedErr := seedCmd.Run(); seedErr != nil {
		slog.Debug("trainlineViaCurl: seed request failed", "err", seedErr)
		// Continue anyway — the API call may still work.
	} else {
		slog.Debug("trainlineViaCurl: homepage seed complete", "jar", cookieJarFile)
	}

	// Step 2: POST to the journey-search API using the seeded cookie jar.
	apiArgs := append([]string{
		"-s", "--http2",
		"--max-time", trainlineHelperMaxTime,
		"-X", "POST",
		"-c", cookieJarFile,
		"-b", cookieJarFile,
		trainlineSearchURL,
		"-H", "Content-Type: application/json",
		"-H", "Accept: application/json",
		"-H", "sec-fetch-dest: empty",
		"-H", "sec-fetch-mode: cors",
		"-H", "sec-fetch-site: same-origin",
		"-H", "x-version: 4.46.32109",
		"-H", "Origin: https://www.thetrainline.com",
		"-H", "Referer: https://www.thetrainline.com/",
		"-d", string(bodyBytes),
	}, commonHeaders...)

	apiCmd := exec.CommandContext(ctx, "curl", apiArgs...)
	curlOut, err := apiCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("trainlineViaCurl curl: %w", err)
	}

	trimmed := strings.TrimSpace(string(curlOut))
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return nil, fmt.Errorf("trainlineViaCurl: non-JSON response (%.80s)", trimmed)
	}

	return readAndParseTrainlineResponse(strings.NewReader(trimmed), "", "", date, currency)
}

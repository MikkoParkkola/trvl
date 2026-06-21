package ground

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

// TestDumpRome2RioAnchors is a live diagnostic (opt-in via R2R_DUMP=1). It uses
// trvl's real fetch path and prints, per route anchor: the route= URL param and
// the full normalized anchor text, so we can see whether ferry segments that the
// name-only parser drops are actually present in the source.
func TestDumpRome2RioAnchors(t *testing.T) {
	if os.Getenv("R2R_DUMP") == "" {
		t.Skip("set R2R_DUMP=1 to run the live Rome2Rio anchor dump")
	}
	from, to := "Helsinki", "Tallinn"
	if v := os.Getenv("R2R_FROM"); v != "" {
		from = v
	}
	if v := os.Getenv("R2R_TO"); v != "" {
		to = v
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var body string
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		body, err = fetchRome2Rio(ctx, from, to, true)
		if err == nil && strings.Contains(body, rome2rioMarker) {
			break
		}
		t.Logf("attempt %d: err=%v marker=%v", attempt, err, strings.Contains(body, rome2rioMarker))
		time.Sleep(3 * time.Second)
	}
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	t.Logf("body bytes=%d marker=%v", len(body), strings.Contains(body, rome2rioMarker))

	doc, perr := html.Parse(strings.NewReader(body))
	if perr != nil {
		t.Fatal(perr)
	}
	n := 0
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" {
			href := attr(node, "href")
			if m := r2rRouteHref.FindStringSubmatch(href); m != nil {
				n++
				text := normalizeWS(textOf(node))
				fmt.Printf("\n=== ROUTE %d ===\n  route_param=%q\n  derived_modes=%v\n  anchor_text=%q\n", n, m[1], rome2rioModes(m[1]), text)
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	fmt.Printf("\nTOTAL ROUTE ANCHORS: %d\n", n)

	// Also dump any JSON-ish blobs that might carry richer segment data.
	for _, kw := range []string{"\"segments\"", "\"hops\"", "\"vehicle\"", "ferry", "Ferry"} {
		fmt.Printf("source contains %q: %v\n", kw, strings.Contains(body, kw))
	}
}

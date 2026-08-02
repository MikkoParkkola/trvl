package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// WATCHID.8 -- the response describes the STORED watch, not the request.
//
// Add is idempotent and a re-watch may legitimately omit fields. Adjusting only
// the alert settings leaves BelowPrice and Currency untouched on the stored
// record, but the request carries them as zero. Echoing the request told the
// agent "target_price: 0, currency: ”" about a watch that still had 200 EUR --
// and the agent repeats that to the user. Found by grok second-opinion review,
// 2026-08-02.
func TestHandleWatchPrice_ResponseReflectsStoredWatch(t *testing.T) {
	watchHome := t.TempDir()
	t.Setenv("HOME", watchHome)
	t.Setenv("USERPROFILE", watchHome)

	call := func(args map[string]any) map[string]any {
		t.Helper()
		_, structured, err := handleWatchPrice(context.Background(), args, nil, nil, nil)
		if err != nil {
			t.Fatalf("handleWatchPrice: %v", err)
		}
		raw, err := json.Marshal(structured)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return out
	}

	first := call(map[string]any{
		"type": "flight", "origin": "AMS", "destination": "VLC",
		"depart_date": "2027-05-01", "target_price": 200.0, "currency": "EUR",
	})
	if first["target_price"] != 200.0 {
		t.Fatalf("setup: target_price = %v, want 200", first["target_price"])
	}

	// Re-watch changing ONLY the alert setting: no target_price, no currency.
	repeat := call(map[string]any{
		"type": "flight", "origin": "AMS", "destination": "VLC",
		"depart_date": "2027-05-01", "alert_drop": 12.0,
	})

	if repeat["watch_id"] != first["watch_id"] {
		t.Fatalf("WATCHID.8: settings-only re-watch forked watch_id %v from %v",
			repeat["watch_id"], first["watch_id"])
	}
	if repeat["target_price"] != 200.0 {
		t.Errorf("WATCHID.8: response reports target_price %v, but the stored watch still has 200 -- "+
			"the response is echoing the request, not the record", repeat["target_price"])
	}
	if repeat["currency"] != "EUR" {
		t.Errorf("WATCHID.8: response reports currency %q, but the stored watch still has EUR",
			repeat["currency"])
	}
}

// WATCHID.9 -- the response DTO must never carry the webhook URL. The URL is
// the credential; MCP structured output is exactly where it must not appear.
// (The storage file and the CLI keep it by design; this is the machine-readable
// surface, which does not.)
func TestHandleWatchPrice_ResponseOmitsWebhookURL(t *testing.T) {
	watchHome := t.TempDir()
	t.Setenv("HOME", watchHome)
	t.Setenv("USERPROFILE", watchHome)

	const token = "T4K9m2QpR7vN1sW6xL8b"
	secret := "https://hooks.slack.com/services/T00000000/B11111111/" + token

	_, structured, err := handleWatchPrice(context.Background(), map[string]any{
		"type": "flight", "origin": "HEL", "destination": "BCN",
		"depart_date": "2027-06-01", "target_price": 250.0, "currency": "EUR",
		"webhook": secret,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleWatchPrice: %v", err)
	}
	raw, err := json.Marshal(structured)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), token) {
		t.Errorf("WATCHID.9: webhook credential leaked into MCP structured output:\n%s", raw)
	}
}

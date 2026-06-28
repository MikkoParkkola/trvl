// Command trvl-telemetry is the receiver for the trvl CLI daily heartbeat
// (MIK-6565). The CLI emits at most one anonymous heartbeat per install per day
// (see internal/telemetry/heartbeat.go); this standalone binary accepts those
// POSTs, validates them against the exact wire contract, and appends each
// accepted heartbeat to a newline-delimited JSON (NDJSON) file.
//
// Privacy is the whole point: the collector stores only the documented payload
// fields. It never records the client IP, hostname, User-Agent, or any other
// request header — there is no code path that reads them.
package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"sync"
	"time"
)

// maxPayloadSize mirrors the client cap (internal/telemetry/heartbeat.go). A
// body larger than this is rejected before any parsing.
const maxPayloadSize = 2048

// allowedFields is the entire wire contract, mirroring the `allowed` map in
// internal/telemetry/heartbeat_test.go. Any key outside this set is rejected as
// a possible identity leak — the collector persists only these fields.
var allowedFields = map[string]bool{
	"project":    true,
	"event":      true,
	"version":    true,
	"runtime":    true,
	"install_id": true,
}

// heartbeat is the only shape the collector accepts or stores. It carries no
// IP, host, or identity field by construction.
type heartbeat struct {
	Project   string `json:"project"`
	Event     string `json:"event"`
	Version   string `json:"version"`
	Runtime   string `json:"runtime"`
	InstallID string `json:"install_id,omitempty"`
}

// collector accepts heartbeats and appends accepted ones to out.
//
// ponytail: NDJSON append is the right amount of machinery for a daily,
// low-volume, single-writer heartbeat — one record per install per day. Move to
// a real store (object storage, a columnar warehouse) only if volume warrants;
// until then a flat append-only file is auditable, greppable, and dependency-free.
type collector struct {
	mu  sync.Mutex
	out io.Writer
}

func newCollector(out io.Writer) *collector {
	return &collector{out: out}
}

// ServeHTTP validates the request against the wire contract and persists it.
// Each failure mode maps to the narrowest correct HTTP status; success is 204.
// No request metadata (remote address, headers beyond Content-Type) is read.
func (c *collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Content-Type must be application/json; tolerate a charset parameter.
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
		return
	}

	// Cap the read at one byte over the limit so we can detect oversize without
	// buffering an unbounded body.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPayloadSize+1))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	if len(body) > maxPayloadSize {
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}

	// Identity-leak guard: decode generically first and reject any field outside
	// the allowed set. This also catches malformed JSON.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		http.Error(w, "malformed json", http.StatusBadRequest)
		return
	}
	for k := range raw {
		if !allowedFields[k] {
			http.Error(w, "unexpected field", http.StatusBadRequest)
			return
		}
	}

	var hb heartbeat
	if err := json.Unmarshal(body, &hb); err != nil {
		http.Error(w, "malformed json", http.StatusBadRequest)
		return
	}
	if hb.Project == "" || hb.Event == "" {
		http.Error(w, "missing required field", http.StatusBadRequest)
		return
	}

	if err := c.append(hb); err != nil {
		// Log the failure without any client identity.
		log.Printf("telemetry: append failed: %v", err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// append writes one canonical NDJSON line. Re-marshaling the typed struct (not
// the raw body) guarantees only allowed fields ever reach disk.
func (c *collector) append(hb heartbeat) error {
	line, err := json.Marshal(hb)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.out.Write(line)
	return err
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	out := flag.String("out", "heartbeats.ndjson", "NDJSON output file for accepted heartbeats")
	flag.Parse()

	f, err := os.OpenFile(*out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Fatalf("telemetry: open output: %v", err)
	}
	defer func() { _ = f.Close() }()

	mux := http.NewServeMux()
	mux.Handle("/v1/heartbeat", newCollector(f))

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("telemetry collector listening on %s, appending to %s", *addr, *out)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("telemetry: server: %v", err)
	}
}

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validBody is a well-formed heartbeat matching the wire contract exactly.
const validBody = `{"project":"trvl","event":"heartbeat","version":"1.2.3","runtime":"linux/amd64/go1.26.4","install_id":"deadbeef"}`

// TestServeHTTP_Validation covers every accept/reject path of the collector.
func TestServeHTTP_Validation(t *testing.T) {
	cases := []struct {
		name        string
		method      string
		contentType string
		body        string
		wantStatus  int
	}{
		{
			name:        "happy path returns 204",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        validBody,
			wantStatus:  http.StatusNoContent,
		},
		{
			name:        "content type with charset is accepted",
			method:      http.MethodPost,
			contentType: "application/json; charset=utf-8",
			body:        validBody,
			wantStatus:  http.StatusNoContent,
		},
		{
			name:        "wrong method rejected",
			method:      http.MethodGet,
			contentType: "application/json",
			body:        validBody,
			wantStatus:  http.StatusMethodNotAllowed,
		},
		{
			name:        "wrong content type rejected",
			method:      http.MethodPost,
			contentType: "text/plain",
			body:        validBody,
			wantStatus:  http.StatusUnsupportedMediaType,
		},
		{
			name:        "missing content type rejected",
			method:      http.MethodPost,
			contentType: "",
			body:        validBody,
			wantStatus:  http.StatusUnsupportedMediaType,
		},
		{
			name:        "oversize body rejected",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        `{"project":"trvl","event":"heartbeat","version":"` + strings.Repeat("x", maxPayloadSize) + `"}`,
			wantStatus:  http.StatusRequestEntityTooLarge,
		},
		{
			name:        "unknown field rejected as identity leak",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        `{"project":"trvl","event":"heartbeat","version":"1.2.3","ip":"203.0.113.7"}`,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "malformed json rejected",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        `{"project":"trvl",`,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "missing required field rejected",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        `{"version":"1.2.3","runtime":"linux/amd64/go1.26.4"}`,
			wantStatus:  http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sink bytes.Buffer
			srv := httptest.NewServer(newCollector(&sink))
			defer srv.Close()

			req, err := http.NewRequest(tc.method, srv.URL, strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}

			// Only the happy paths may persist anything.
			if tc.wantStatus != http.StatusNoContent && sink.Len() != 0 {
				t.Fatalf("rejected request must not persist, got %q", sink.String())
			}
		})
	}
}

// TestServeHTTP_PersistsToFile proves an accepted heartbeat is appended to the
// NDJSON file with exactly the wire-contract fields and nothing else.
func TestServeHTTP_PersistsToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heartbeats.ndjson")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer func() { _ = f.Close() }()

	srv := httptest.NewServer(newCollector(f))
	defer srv.Close()

	// Send two heartbeats to confirm append semantics (one record per line).
	bodies := []string{
		validBody,
		`{"project":"trvl","event":"heartbeat","version":"9.9.9","runtime":"darwin/arm64/go1.26.4","install_id":"cafef00d"}`,
	}
	for _, b := range bodies {
		resp, err := http.Post(srv.URL, "application/json", strings.NewReader(b))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("got %d NDJSON lines, want 2: %q", len(lines), string(data))
	}

	// First record must round-trip to exactly the sent fields.
	var got heartbeat
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("unmarshal stored line: %v", err)
	}
	want := heartbeat{
		Project:   "trvl",
		Event:     "heartbeat",
		Version:   "1.2.3",
		Runtime:   "linux/amd64/go1.26.4",
		InstallID: "deadbeef",
	}
	if got != want {
		t.Fatalf("stored record = %+v, want %+v", got, want)
	}

	// Defense in depth: the stored line must contain only allowed keys.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[0]), &raw); err != nil {
		t.Fatalf("unmarshal stored line to map: %v", err)
	}
	for k := range raw {
		if !allowedFields[k] {
			t.Fatalf("stored record leaked field %q", k)
		}
	}
}

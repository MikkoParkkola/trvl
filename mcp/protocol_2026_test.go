package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProtocol2026Discover(t *testing.T) {
	s := NewServer()
	resp := s.HandleRequest(jsonRequest(t, "server/discover", 1, map[string]any{
		"_meta": map[string]any{metaProtocolVersion: protocolVersion20260728},
	}))
	if resp.Error != nil {
		t.Fatalf("discover: %s", resp.Error.Message)
	}
	m := resultMap(t, resp.Result)
	if m["resultType"] != "complete" {
		t.Fatalf("resultType = %#v, want complete", m["resultType"])
	}
	got := stringList(t, m["supportedVersions"])
	if strings.Join(got, ",") != strings.Join(supportedProtocolVersions, ",") {
		t.Fatalf("supportedVersions = %v, want %v", got, supportedProtocolVersions)
	}
	if m["cacheScope"] != "public" {
		t.Fatalf("cacheScope = %#v, want public", m["cacheScope"])
	}
	meta := mapField(t, m, "_meta")
	info := mapField(t, meta, metaServerInfo)
	if info["name"] != serverName {
		t.Fatalf("serverInfo.name = %#v", info["name"])
	}
}

func TestProtocol2026ListEndpointsCarryCacheHints(t *testing.T) {
	s := NewServer()
	cases := []struct {
		method string
		scope  string
	}{
		{"tools/list", "public"},
		{"prompts/list", "public"},
		{"resources/templates/list", "public"},
		{"resources/list", "private"},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			resp := s.HandleRequest(jsonRequest(t, tc.method, 1, map[string]any{
				"_meta": map[string]any{metaProtocolVersion: protocolVersion20260728},
			}))
			if resp.Error != nil {
				t.Fatalf("%s: %s", tc.method, resp.Error.Message)
			}
			m := resultMap(t, resp.Result)
			if m["resultType"] != "complete" {
				t.Fatalf("resultType = %#v", m["resultType"])
			}
			if m["cacheScope"] != tc.scope {
				t.Fatalf("cacheScope = %#v, want %s", m["cacheScope"], tc.scope)
			}
			if _, ok := m["ttlMs"]; !ok {
				t.Fatal("ttlMs missing")
			}
		})
	}

	read := s.HandleRequest(jsonRequest(t, "resources/read", 2, map[string]any{
		"_meta": map[string]any{metaProtocolVersion: protocolVersion20260728},
		"uri":   "trvl://onboarding",
	}))
	if read.Error != nil {
		t.Fatalf("resources/read: %s", read.Error.Message)
	}
	if resultMap(t, read.Result)["cacheScope"] != "private" {
		t.Fatal("resources/read cacheScope must be private")
	}
}

func TestProtocol2026PublicListIgnoresCallerState(t *testing.T) {
	s := NewServer()
	before := s.HandleRequest(modernRequest(t, "tools/list", 1, nil))
	s.recordSearch("flight", "HEL->BCN 2026-10-01", 100, "EUR")
	after := s.HandleRequest(modernRequest(t, "tools/list", 2, nil))
	if resultMap(t, before.Result)["cacheScope"] != "public" || resultMap(t, after.Result)["cacheScope"] != "public" {
		t.Fatal("tools/list became caller-scoped")
	}
	b, _ := json.Marshal(resultMap(t, before.Result)["tools"])
	a, _ := json.Marshal(resultMap(t, after.Result)["tools"])
	if !bytes.Equal(b, a) {
		t.Fatal("tools/list changed after session state was recorded")
	}

	listed := resultMap(t, s.HandleRequest(modernRequest(t, "resources/list", 3, nil)).Result)
	if listed["cacheScope"] != "private" {
		t.Fatal("resources/list includes session state and must be private")
	}
	raw, _ := json.Marshal(listed["resources"])
	if !bytes.Contains(raw, []byte("trvl://watch/HEL-BCN-2026-10-01")) {
		t.Fatalf("resources/list did not include the caller-specific watch URI: %s", raw)
	}
}

func TestProtocol2026LegacySessionsUnchanged(t *testing.T) {
	for _, version := range []string{protocolVersion20251125, protocolVersion20250326} {
		t.Run(version, func(t *testing.T) {
			s := NewServer()
			initResp := s.HandleRequest(jsonRequest(t, "initialize", 1, map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "legacy", "version": "1"},
			}))
			if initResp.Error != nil {
				t.Fatalf("initialize: %s", initResp.Error.Message)
			}
			initJSON := mustJSON(t, initResp.Result)
			if bytes.Contains(initJSON, []byte("resultType")) || bytes.Contains(initJSON, []byte("ttlMs")) {
				t.Fatalf("legacy initialize gained 2026 fields: %s", initJSON)
			}
			var init InitializeResult
			if err := json.Unmarshal(initJSON, &init); err != nil {
				t.Fatal(err)
			}
			if init.ProtocolVersion != protocolVersion20251125 {
				t.Fatalf("protocolVersion = %q, want %q", init.ProtocolVersion, protocolVersion20251125)
			}

			tools := s.HandleRequest(jsonRequest(t, "tools/list", 2, nil))
			toolsJSON := mustJSON(t, tools.Result)
			if bytes.Contains(toolsJSON, []byte("resultType")) || bytes.Contains(toolsJSON, []byte("ttlMs")) {
				t.Fatalf("legacy tools/list gained 2026 fields: %s", toolsJSON)
			}
			ping := s.HandleRequest(jsonRequest(t, "ping", 3, nil))
			if ping.Error != nil {
				t.Fatalf("ping: %s", ping.Error.Message)
			}
			sub := s.HandleRequest(jsonRequest(t, "resources/subscribe", 4, map[string]any{"uri": "trvl://watches"}))
			if sub.Error != nil {
				t.Fatalf("resources/subscribe: %s", sub.Error.Message)
			}
		})
	}
}

func TestProtocol2026ListenReplacesSubscribeAndHonoursOptIn(t *testing.T) {
	s := NewServer()
	var notes bytes.Buffer
	s.notifyWriter = &notes

	gone := s.HandleRequest(modernRequest(t, "resources/subscribe", 7, map[string]any{"uri": "trvl://watches"}))
	if gone.Error == nil || gone.Error.Code != -32601 {
		t.Fatalf("2026 resources/subscribe = %#v, want method not found", gone.Error)
	}
	ping := s.HandleRequest(modernRequest(t, "ping", 8, nil))
	if ping.Error == nil || ping.Error.Code != -32601 {
		t.Fatalf("2026 ping = %#v, want method not found", ping.Error)
	}

	const watched = "trvl://watches"
	const other = "trvl://onboarding"
	opened := s.HandleRequest(modernRequest(t, "subscriptions/listen", 9, map[string]any{
		"notifications": map[string]any{
			"toolsListChanged":      true,
			"resourceSubscriptions": []string{watched},
		},
	}))
	if opened != nil {
		t.Fatalf("listen returned a result before close: %#v", opened)
	}
	ack := notes.String()
	if !strings.Contains(ack, "notifications/subscriptions/acknowledged") || !strings.Contains(ack, watched) {
		t.Fatalf("ack = %s", ack)
	}
	if strings.Contains(ack, "promptsListChanged") || strings.Contains(ack, other) {
		t.Fatalf("ack included a type the client did not opt into: %s", ack)
	}

	notes.Reset()
	s.SendResourceUpdated(other)
	if notes.Len() != 0 {
		t.Fatalf("update for a non-opted-in URI was delivered: %s", notes.String())
	}
	s.SendResourceUpdated(watched)
	if !strings.Contains(notes.String(), watched) || !strings.Contains(notes.String(), metaSubscriptionID) {
		t.Fatalf("opted-in update missing: %s", notes.String())
	}

	closed := s.HandleRequest(modernRequest(t, "notifications/cancelled", nil, map[string]any{"requestId": 9}))
	if closed == nil || closed.Error != nil {
		t.Fatalf("cancel = %#v", closed)
	}
	if resultMap(t, closed.Result)["resultType"] != "complete" {
		t.Fatal("subscription close was not resultType complete")
	}
}

func TestProtocol2026HeaderMismatch(t *testing.T) {
	req := modernRequest(t, "tools/call", 1, map[string]any{"name": "travel"})
	if err := headerContractError("", "", "", req); err == nil || err.Code != codeHeaderMismatch {
		t.Fatalf("missing Mcp-Method = %#v", err)
	}
	if err := headerContractError("", "tools/list", "", req); err == nil || !strings.Contains(err.Message, "Mcp-Method") {
		t.Fatalf("wrong method = %#v", err)
	}
	if err := headerContractError("", "tools/call", "other", req); err == nil || !strings.Contains(err.Message, "Mcp-Name") {
		t.Fatalf("wrong name = %#v", err)
	}
	if err := headerContractError("", "tools/call", "travel", req); err != nil {
		t.Fatalf("matching headers rejected: %s", err.Message)
	}
	encoded := "=?base64?" + "dHJhdmVs" + "?=" // "travel"
	if err := headerContractError(protocolVersion20260728, "tools/call", encoded, req); err != nil {
		t.Fatalf("encoded name rejected: %s", err.Message)
	}

	legacy := jsonRequest(t, "tools/list", 2, nil)
	if err := headerContractError("", "", "", legacy); err != nil {
		t.Fatalf("legacy request required headers: %s", err.Message)
	}
}

func TestHTTPHeaderMismatchAndLegacyPass(t *testing.T) {
	h := NewHTTPServer(0)

	bad := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bad)
	req.Header.Set("Mcp-Method", "tools/call")
	rec := httptest.NewRecorder()
	h.handleMCP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`-32020`)) {
		t.Fatalf("body = %s", rec.Body.String())
	}

	okBody := bytes.NewBufferString(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	okReq := httptest.NewRequest(http.MethodPost, "/mcp", okBody)
	okRec := httptest.NewRecorder()
	h.handleMCP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("legacy status = %d body %s", okRec.Code, okRec.Body.String())
	}
	if bytes.Contains(okRec.Body.Bytes(), []byte("ttlMs")) {
		t.Fatalf("legacy HTTP tools/list gained ttlMs: %s", okRec.Body.String())
	}
}

func TestHTTPSubscriptionsListenAcksThenCloses(t *testing.T) {
	h := NewHTTPServer(0)
	srv := httptest.NewServer(h.newMux())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := []byte(`{"jsonrpc":"2.0","id":4,"method":"subscriptions/listen","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"},"notifications":{"resourceSubscriptions":["trvl://watches"]}}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Method", "subscriptions/listen")
	req.Header.Set("MCP-Protocol-Version", protocolVersion20260728)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content-type = %q", resp.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(resp.Body)
	ack, err := readSSE(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(ack, []byte("notifications/subscriptions/acknowledged")) || !bytes.Contains(ack, []byte("trvl://watches")) {
		t.Fatalf("ack = %s", ack)
	}
	// Closing the client context ends the stream. The completion result is
	// asserted on the stdio path, where cancellation is a JSON-RPC message
	// rather than a dropped TCP read.
	cancel()
}

func modernRequest(t *testing.T, method string, id any, extra map[string]any) *Request {
	t.Helper()
	params := map[string]any{
		"_meta": map[string]any{metaProtocolVersion: protocolVersion20260728},
	}
	for k, v := range extra {
		params[k] = v
	}
	return jsonRequest(t, method, id, params)
}

func jsonRequest(t *testing.T, method string, id any, params map[string]any) *Request {
	t.Helper()
	req := &Request{JSONRPC: "2.0", ID: id, Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		req.Params = raw
	}
	return req
}

func resultMap(t *testing.T, result any) map[string]any {
	t.Helper()
	raw := mustJSON(t, result)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mapField(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, _ := m[key].(map[string]any)
	if v == nil {
		t.Fatalf("missing object field %s in %#v", key, m)
	}
	return v
}

func stringList(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func readSSE(r *bufio.Reader) ([]byte, error) {
	var buf bytes.Buffer
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		if bytes.Equal(line, []byte("\n")) && buf.Len() > 0 {
			return buf.Bytes(), nil
		}
		buf.Write(line)
	}
}

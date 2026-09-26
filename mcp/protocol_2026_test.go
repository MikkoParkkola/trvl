package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
	if len(got) != 2 || got[0] != "2026-07-28" || got[1] != "2025-11-25" {
		t.Fatalf("supportedVersions = %v, want [2026-07-28 2025-11-25]", got)
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

func TestProtocolSupportIsTheTwoLatestRevisions(t *testing.T) {
	if len(supportedProtocolVersions) != 2 ||
		supportedProtocolVersions[0] != "2026-07-28" ||
		supportedProtocolVersions[1] != "2025-11-25" {
		t.Fatalf("supported versions = %v, want 2026-07-28 then 2025-11-25", supportedProtocolVersions)
	}
	for _, version := range supportedProtocolVersions {
		if version == protocolVersion20250326 {
			t.Fatal("2025-03-26 is older than the two latest revisions")
		}
	}

	s := NewServer()
	rejected := s.HandleRequest(jsonRequest(t, "tools/list", 1, map[string]any{
		"_meta": map[string]any{metaProtocolVersion: protocolVersion20250326},
	}))
	if rejected.Error == nil || rejected.Error.Code != -32022 {
		t.Fatalf("2025-03-26 _meta = %#v, want unsupported protocol version", rejected.Error)
	}
}

func TestProtocol2026LegacySessionsUnchanged(t *testing.T) {
	// 2025-11-25 is the older supported revision. 2025-03-26 is not supported;
	// an initialize that names it is answered as 2025-11-25.
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
			if init.ProtocolVersion != "2025-11-25" {
				t.Fatalf("protocolVersion = %q, want 2025-11-25", init.ProtocolVersion)
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
	if ping := s.HandleRequest(modernRequest(t, "ping", 8, nil)); ping.Error == nil || ping.Error.Code != -32601 {
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
	if err := headerContractError(protocolVersion20260728, "", "", req); err == nil || err.Code != codeHeaderMismatch {
		t.Fatalf("missing Mcp-Method = %#v", err)
	}
	if err := headerContractError(protocolVersion20260728, "tools/list", "", req); err == nil || !strings.Contains(err.Message, "Mcp-Method") {
		t.Fatalf("wrong method = %#v", err)
	}
	if err := headerContractError(protocolVersion20260728, "tools/call", "other", req); err == nil || !strings.Contains(err.Message, "Mcp-Name") {
		t.Fatalf("wrong name = %#v", err)
	}
	if err := headerContractError("", "tools/call", "travel", req); err == nil || err.Code != codeHeaderMismatch {
		t.Fatalf("2026 body without MCP-Protocol-Version = %#v, want header mismatch", err)
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

func TestMCPWindow(t *testing.T) {
	t.Run("S1", func(t *testing.T) {
		s := NewServer()
		resp := s.HandleRequest(jsonRequest(t, "server/discover", 1, map[string]any{
			"_meta": map[string]any{metaProtocolVersion: "2026-07-28"},
		}))
		if resp.Error != nil {
			t.Fatalf("discover: %s", resp.Error.Message)
		}
		got := stringList(t, resultMap(t, resp.Result)["supportedVersions"])
		if len(got) != 2 || got[0] != "2026-07-28" || got[1] != "2025-11-25" {
			t.Fatalf("supportedVersions = %v, want [2026-07-28 2025-11-25]", got)
		}
	})

	t.Run("S4", func(t *testing.T) {
		resp := NewServer().HandleRequest(jsonRequest(t, "tools/list", 1, map[string]any{
			"_meta": map[string]any{metaProtocolVersion: "2025-03-26"},
		}))
		assertUnsupported(t, mustJSON(t, resp), "2025-03-26")
	})

	t.Run("S4e", func(t *testing.T) {
		resp := NewServer().HandleRequest(jsonRequest(t, "initialize", 1, map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "legacy", "version": "1"},
			"_meta":           map[string]any{metaProtocolVersion: "2025-03-26"},
		}))
		assertUnsupported(t, mustJSON(t, resp), "2025-03-26")
	})

	t.Run("S4g", func(t *testing.T) {
		resp := NewServer().HandleRequest(jsonRequest(t, "tools/list", 1, map[string]any{
			"_meta": map[string]any{metaProtocolVersion: "1900-01-01"},
		}))
		assertUnsupported(t, mustJSON(t, resp), "1900-01-01")
	})

	t.Run("S5", func(t *testing.T) {
		s := NewServer()
		resp := s.HandleRequest(jsonRequest(t, "initialize", 1, map[string]any{
			"protocolVersion": "2026-07-28",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "legacy", "version": "1"},
		}))
		if resp.Error != nil {
			t.Fatalf("initialize: %s", resp.Error.Message)
		}
		raw := mustJSON(t, resp.Result)
		if bytes.Contains(raw, []byte("resultType")) {
			t.Fatalf("handshake gained resultType: %s", raw)
		}
		var init InitializeResult
		if err := json.Unmarshal(raw, &init); err != nil {
			t.Fatal(err)
		}
		if init.ProtocolVersion != "2025-11-25" {
			t.Fatalf("protocolVersion = %q, want 2025-11-25", init.ProtocolVersion)
		}
		if init.Capabilities.Resources == nil || !init.Capabilities.Resources.Subscribe {
			t.Fatalf("subscribe = %#v, want true", init.Capabilities.Resources)
		}
	})

	t.Run("S5c", func(t *testing.T) {
		resp := NewServer().HandleRequest(jsonRequest(t, "initialize", 1, map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "legacy", "version": "1"},
			"_meta":           map[string]any{metaProtocolVersion: "2026-07-28"},
		}))
		if resp.Error != nil {
			t.Fatalf("initialize: %s", resp.Error.Message)
		}
		raw := mustJSON(t, resp.Result)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if body["resultType"] != "complete" {
			t.Fatalf("resultType = %#v, want complete", body["resultType"])
		}
		if body["protocolVersion"] != "2026-07-28" {
			t.Fatalf("protocolVersion = %#v, want 2026-07-28", body["protocolVersion"])
		}
		resources, _ := body["capabilities"].(map[string]any)["resources"].(map[string]any)
		if resources["subscribe"] != false {
			t.Fatalf("subscribe = %#v, want false", resources["subscribe"])
		}
	})

	t.Run("S5e", func(t *testing.T) {
		resp := NewServer().HandleRequest(jsonRequest(t, "initialize", 1, map[string]any{
			"protocolVersion": "2026-07-28",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "legacy", "version": "1"},
			"_meta":           map[string]any{metaProtocolVersion: "2025-11-25"},
		}))
		if resp.Error != nil {
			t.Fatalf("initialize: %s", resp.Error.Message)
		}
		raw := mustJSON(t, resp.Result)
		if bytes.Contains(raw, []byte("resultType")) {
			t.Fatalf("legacy _meta initialize gained resultType: %s", raw)
		}
		var init InitializeResult
		if err := json.Unmarshal(raw, &init); err != nil {
			t.Fatal(err)
		}
		if init.ProtocolVersion != "2025-11-25" {
			t.Fatalf("protocolVersion = %q, want 2025-11-25", init.ProtocolVersion)
		}
		if init.Capabilities.Resources == nil || !init.Capabilities.Resources.Subscribe {
			t.Fatalf("subscribe = %#v, want true", init.Capabilities.Resources)
		}
	})

	t.Run("S5f", func(t *testing.T) {
		rec := postWindow(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"c","version":"1"},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`, map[string]string{
			"MCP-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "initialize",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		result, _ := body["result"].(map[string]any)
		if result["resultType"] != "complete" || result["protocolVersion"] != "2026-07-28" {
			t.Fatalf("result = %#v", result)
		}
		resources, _ := result["capabilities"].(map[string]any)["resources"].(map[string]any)
		if resources["subscribe"] != false {
			t.Fatalf("subscribe = %#v, want false", resources["subscribe"])
		}
	})

	t.Run("S5g", func(t *testing.T) {
		rec := postWindow(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`, map[string]string{
			"MCP-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "initialize",
		})
		assertHTTPCode(t, rec, http.StatusBadRequest, -32020)
	})

	t.Run("R2c", func(t *testing.T) {
		rec := postWindow(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"c","version":"1"},"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-11-25"}}}`, map[string]string{
			"MCP-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "initialize",
		})
		assertHTTPCode(t, rec, http.StatusBadRequest, -32020)
	})

	t.Run("S5d", func(t *testing.T) {
		resp := NewServer().HandleRequest(jsonRequest(t, "initialize", 1, map[string]any{
			"protocolVersion": "1900-01-01",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "legacy", "version": "1"},
		}))
		if resp.Error != nil {
			t.Fatalf("initialize: %s", resp.Error.Message)
		}
		raw := mustJSON(t, resp.Result)
		if bytes.Contains(raw, []byte("resultType")) {
			t.Fatalf("unknown handshake gained resultType: %s", raw)
		}
		var init InitializeResult
		if err := json.Unmarshal(raw, &init); err != nil {
			t.Fatal(err)
		}
		if init.ProtocolVersion != "2025-11-25" {
			t.Fatalf("protocolVersion = %q, want 2025-11-25", init.ProtocolVersion)
		}
	})

	t.Run("S3d", func(t *testing.T) {
		s := NewServer()
		shapes := []struct {
			meta string
			want bool
		}{
			{"2026-07-28", true},
			{"2025-11-25", false},
			{"", false},
			{"2026-07-28", true},
		}
		for i, shape := range shapes {
			params := map[string]any{}
			if shape.meta != "" {
				params["_meta"] = map[string]any{metaProtocolVersion: shape.meta}
			}
			resp := s.HandleRequest(jsonRequest(t, "tools/list", i+1, params))
			if resp.Error != nil {
				t.Fatalf("step %d: %s", i, resp.Error.Message)
			}
			raw := mustJSON(t, resp.Result)
			has := bytes.Contains(raw, []byte(`"resultType"`))
			if has != shape.want {
				t.Fatalf("step %d resultType present = %v, want %v: %s", i, has, shape.want, raw)
			}
		}
	})

	t.Run("S3b", func(t *testing.T) {
		s := NewServer()
		list := s.HandleRequest(jsonRequest(t, "tools/list", 1, map[string]any{
			"_meta": map[string]any{metaProtocolVersion: "2025-11-25"},
		}))
		if list.Error != nil {
			t.Fatalf("tools/list: %s", list.Error.Message)
		}
		raw := mustJSON(t, list.Result)
		if bytes.Contains(raw, []byte("resultType")) || bytes.Contains(raw, []byte("ttlMs")) || bytes.Contains(raw, []byte("cacheScope")) {
			t.Fatalf("legacy _meta gained 2026 fields: %s", raw)
		}
		if ping := s.HandleRequest(jsonRequest(t, "ping", 2, map[string]any{
			"_meta": map[string]any{metaProtocolVersion: "2025-11-25"},
		})); ping.Error != nil {
			t.Fatalf("ping: %s", ping.Error.Message)
		}
		if sub := s.HandleRequest(jsonRequest(t, "resources/subscribe", 3, map[string]any{
			"_meta": map[string]any{metaProtocolVersion: "2025-11-25"},
			"uri":   "trvl://watches",
		})); sub.Error != nil {
			t.Fatalf("resources/subscribe: %s", sub.Error.Message)
		}
	})

	t.Run("S2c", func(t *testing.T) {
		rec := postWindow(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{
			"MCP-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "tools/list",
		})
		assertHTTPCode(t, rec, http.StatusBadRequest, -32020)
	})

	t.Run("S2d", func(t *testing.T) {
		rec := postWindow(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`, map[string]string{
			"Mcp-Method": "tools/list",
		})
		assertHTTPCode(t, rec, http.StatusBadRequest, -32020)
	})

	t.Run("S4b", func(t *testing.T) {
		rec := postWindow(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-03-26"}}}`, map[string]string{
			"MCP-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "tools/list",
		})
		assertHTTPCode(t, rec, http.StatusBadRequest, -32020)
	})

	t.Run("S4c", func(t *testing.T) {
		rec := postWindow(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{
			"MCP-Protocol-Version": "2025-03-26",
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
		}
		assertUnsupported(t, rec.Body.Bytes(), "2025-03-26")
	})

	t.Run("S4d", func(t *testing.T) {
		rec := postWindow(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-03-26"}}}`, map[string]string{
			"MCP-Protocol-Version": "2025-06-18",
		})
		assertHTTPCode(t, rec, http.StatusBadRequest, -32020)
	})

	t.Run("S4f", func(t *testing.T) {
		rec := postWindow(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`, map[string]string{
			"MCP-Protocol-Version": "2025-03-26",
			"Mcp-Method":           "tools/list",
		})
		assertHTTPCode(t, rec, http.StatusBadRequest, -32020)
	})

	t.Run("S4h", func(t *testing.T) {
		rec := postWindow(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-03-26"}}}`, map[string]string{
			"MCP-Protocol-Version": "2025-03-26",
			"Mcp-Method":           "tools/list",
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
		}
		assertUnsupported(t, rec.Body.Bytes(), "2025-03-26")
	})

	t.Run("R2", func(t *testing.T) {
		rec := postWindow(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-11-25"}}}`, map[string]string{
			"MCP-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "tools/list",
		})
		assertHTTPCode(t, rec, http.StatusBadRequest, -32020)
	})

	t.Run("R2b", func(t *testing.T) {
		rec := postWindow(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`, map[string]string{
			"MCP-Protocol-Version": "2025-11-25",
			"Mcp-Method":           "tools/list",
		})
		assertHTTPCode(t, rec, http.StatusBadRequest, -32020)
	})

	t.Run("S3c", func(t *testing.T) {
		list := postWindow(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{
			"MCP-Protocol-Version": "2025-11-25",
		})
		if list.Code != http.StatusOK {
			t.Fatalf("list status = %d: %s", list.Code, list.Body.String())
		}
		var listBody struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(list.Body.Bytes(), &listBody); err != nil {
			t.Fatal(err)
		}
		if listBody.Error != nil || len(listBody.Result) == 0 {
			t.Fatalf("list = %s", list.Body.String())
		}
		if bytes.Contains(listBody.Result, []byte("resultType")) || bytes.Contains(listBody.Result, []byte("ttlMs")) || bytes.Contains(listBody.Result, []byte("cacheScope")) {
			t.Fatalf("legacy header gained 2026 fields: %s", listBody.Result)
		}
		sub := postWindow(t, `{"jsonrpc":"2.0","id":2,"method":"resources/subscribe","params":{"uri":"trvl://watches"}}`, map[string]string{
			"MCP-Protocol-Version": "2025-11-25",
		})
		if sub.Code != http.StatusOK || bytes.Contains(sub.Body.Bytes(), []byte(`"error"`)) {
			t.Fatalf("subscribe = %d %s", sub.Code, sub.Body.String())
		}
	})

	t.Run("R3", func(t *testing.T) {
		modern := postWindow(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`, map[string]string{
			"MCP-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "tools/list",
		})
		if modern.Code != http.StatusOK || !bytes.Contains(modern.Body.Bytes(), []byte(`"resultType":"complete"`)) {
			t.Fatalf("modern = %d %s", modern.Code, modern.Body.String())
		}
		legacy := postWindow(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-11-25"}}}`, map[string]string{
			"MCP-Protocol-Version": "2025-11-25",
		})
		if legacy.Code != http.StatusOK || bytes.Contains(legacy.Body.Bytes(), []byte("resultType")) || bytes.Contains(legacy.Body.Bytes(), []byte(`"error"`)) {
			t.Fatalf("legacy = %d %s", legacy.Code, legacy.Body.String())
		}
	})

	t.Run("R5", func(t *testing.T) {
		s := NewServer()
		list := s.HandleRequest(jsonRequest(t, "tools/list", 1, nil))
		if list.Error != nil {
			t.Fatalf("tools/list: %s", list.Error.Message)
		}
		raw := mustJSON(t, list.Result)
		if bytes.Contains(raw, []byte("resultType")) || bytes.Contains(raw, []byte("ttlMs")) {
			t.Fatalf("undeclared tools/list gained 2026 fields: %s", raw)
		}
		if ping := s.HandleRequest(jsonRequest(t, "ping", 2, nil)); ping.Error != nil {
			t.Fatalf("ping: %s", ping.Error.Message)
		}
	})

	t.Run("D1", func(t *testing.T) {
		raw, err := os.ReadFile("../CHANGELOG.md")
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		unreleased, historic, ok := strings.Cut(text, "## [1.22.0]")
		if !ok {
			t.Fatal("missing ## [1.22.0]")
		}
		if !strings.Contains(unreleased, "2026-07-28") || !strings.Contains(unreleased, "2025-11-25") {
			t.Fatal("Unreleased does not name both revisions")
		}
		if !strings.Contains(historic, "with **2025-11-25** or **2025-03-26**") {
			t.Fatal("1.22.0 section lost the shipped handshake sentence")
		}
	})
}

func postWindow(t *testing.T, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHTTPServer(0)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.handleMCP(rec, req)
	return rec
}

func assertHTTPCode(t *testing.T, rec *httptest.ResponseRecorder, status, code int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	var body struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error == nil || body.Error.Code != code {
		t.Fatalf("error = %#v, want %d", body.Error, code)
	}
	if len(body.Error.Data) > 0 {
		t.Fatalf("data = %s, want absent", body.Error.Data)
	}
	if len(body.Result) > 0 && !bytes.Equal(body.Result, []byte("null")) {
		t.Fatalf("result = %s, want absent", body.Result)
	}
}

func assertUnsupported(t *testing.T, raw []byte, requested string) {
	t.Helper()
	var body struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code int `json:"code"`
			Data *struct {
				Supported []string `json:"supported"`
				Requested string   `json:"requested"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	if body.Error == nil || body.Error.Code != -32022 || body.Error.Data == nil {
		t.Fatalf("error = %#v, want -32022 with data", body.Error)
	}
	if body.Error.Data.Requested != requested {
		t.Fatalf("requested = %q, want %q", body.Error.Data.Requested, requested)
	}
	got := body.Error.Data.Supported
	if len(got) != 2 || got[0] != "2026-07-28" || got[1] != "2025-11-25" {
		t.Fatalf("supported = %v, want [2026-07-28 2025-11-25]", got)
	}
	if len(body.Result) > 0 && !bytes.Equal(body.Result, []byte("null")) {
		t.Fatalf("result = %s, want absent", body.Result)
	}
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

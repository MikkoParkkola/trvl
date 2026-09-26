package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

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

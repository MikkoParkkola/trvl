package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// MCP 2026-07-28 is implemented on trvl's existing server. The official Go SDK
// owns the HTTP server it serves, and trvl's bearer, scope, and protected-resource
// metadata handling lives in http.go and http_auth.go. Handing that transport to
// the SDK would replace the auth boundary. The revision is therefore implemented
// here. Compatibility is the two latest revisions: 2026-07-28 and 2025-11-25.
// An initialize handshake with no _meta is answered as 2025-11-25, including
// a handshake that names 2026-07-28. _meta of 2026-07-28 selects that
// revision. A _meta version outside the two supported revisions is rejected.

const (
	protocolVersion20250326 = "2025-03-26"
	protocolVersion20251125 = "2025-11-25"
	protocolVersion20260728 = "2026-07-28"

	// HeaderMismatch and UnsupportedProtocolVersion are the 2026-07-28
	// allocations. -32020 and -32022 are in the spec-reserved range.
	codeHeaderMismatch             = -32020
	codeUnsupportedProtocolVersion = -32022

	metaProtocolVersion = "io.modelcontextprotocol/protocolVersion"
	metaServerInfo      = "io.modelcontextprotocol/serverInfo"
	metaSubscriptionID  = "io.modelcontextprotocol/subscriptionId"

	// Static lists are safe to share. Caller-specific resources are not.
	cacheTTLStaticMs = 300000
	cacheTTLDiscover = 3600000
)

// supportedProtocolVersions is the two latest published revisions a client may
// select. Latest first so a client that picks the first entry speaks the
// current revision. 2025-03-26 is not in this set.
var supportedProtocolVersions = []string{
	protocolVersion20260728,
	protocolVersion20251125,
}

type cacheHint struct {
	ttlMs int
	scope string
}

type listenFilter struct {
	toolsListChanged     bool
	promptsListChanged   bool
	resourcesListChanged bool
	resourceURIs         map[string]bool
}

type listenSub struct {
	id     any
	filter listenFilter
	emit   func(method string, params any)
	// ready is set after the acknowledgement has been written. Updates must
	// not go out before that acknowledgement.
	ready bool
}

func supportedProtocol(version string) bool {
	for _, v := range supportedProtocolVersions {
		if v == version {
			return true
		}
	}
	return false
}

// metaProtocolVersionOf reads the per-request protocol version. 2026 clients
// send it on every call; 2025 clients do not, and stay on the legacy shape.
func metaProtocolVersionOf(req *Request) (string, bool) {
	if req == nil || len(req.Params) == 0 {
		return "", false
	}
	var params struct {
		Meta map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Meta == nil {
		return "", false
	}
	v, _ := params.Meta[metaProtocolVersion].(string)
	v = strings.TrimSpace(v)
	if v == "" {
		return "", false
	}
	return v, true
}

// protocolOf classifies one request. An explicit _meta version outside the two
// supported revisions is an error. Absence means the 2025-11-25 shape. The
// initialize handshake does not select 2026-07-28; that revision is selected
// by _meta, and on HTTP by a header that matches it.
func protocolOf(req *Request) (string, *Error) {
	if v, ok := metaProtocolVersionOf(req); ok {
		if !supportedProtocol(v) {
			return "", unsupportedProtocol(v)
		}
		return v, nil
	}
	return protocolVersion, nil
}

func unsupportedProtocol(requested string) *Error {
	return &Error{
		Code:    codeUnsupportedProtocolVersion,
		Message: "unsupported protocol version: " + requested,
		Data: map[string]any{
			"supported": []string{protocolVersion20260728, protocolVersion20251125},
			"requested": requested,
		},
	}
}

func isProtocol2026(version string) bool {
	return version == protocolVersion20260728
}

func removedIn2026(method string) bool {
	switch method {
	case "ping", "logging/setLevel", "resources/subscribe", "resources/unsubscribe":
		return true
	default:
		return false
	}
}

func cacheHintFor(method string) *cacheHint {
	switch method {
	case "server/discover":
		return &cacheHint{ttlMs: cacheTTLDiscover, scope: "public"}
	case "tools/list", "prompts/list", "resources/templates/list":
		return &cacheHint{ttlMs: cacheTTLStaticMs, scope: "public"}
	case "resources/list", "resources/read":
		// resources/list mixes session searches and the local watch store into
		// the same payload as the static guides, so the whole result is private.
		return &cacheHint{ttlMs: 0, scope: "private"}
	default:
		return nil
	}
}

func (s *Server) finish(req *Request, resp *Response) *Response {
	if resp == nil || resp.Error != nil || resp.Result == nil || req == nil {
		return resp
	}
	version, err := protocolOf(req)
	if err != nil || !isProtocol2026(version) {
		return resp
	}
	resp.Result = envelope2026(resp.Result, cacheHintFor(req.Method))
	return resp
}

func envelope2026(result any, hint *cacheHint) any {
	raw, err := json.Marshal(result)
	if err != nil {
		return result
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return result
	}
	if m["resultType"] == nil || m["resultType"] == "" {
		m["resultType"] = "complete"
	}
	meta, _ := m["_meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	if _, ok := meta[metaServerInfo]; !ok {
		meta[metaServerInfo] = map[string]any{"name": serverName, "version": serverVersion}
	}
	m["_meta"] = meta
	if hint != nil {
		m["ttlMs"] = hint.ttlMs
		m["cacheScope"] = hint.scope
	}
	return m
}

func (s *Server) handleDiscover(req *Request) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"supportedVersions": supportedProtocolVersions,
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": false},
				"prompts":   map[string]any{"listChanged": false},
				"resources": map[string]any{"listChanged": true},
			},
			"instructions": "trvl searches flights, hotels, ground transport, and trip state. " +
				"Prefer the travel tool. 2025 clients keep using initialize.",
		},
	}
}

func (s *Server) handleResourceTemplatesList(req *Request) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]any{"resourceTemplates": []any{}},
	}
}

func (s *Server) openListen(req *Request, emit func(method string, params any)) (*listenSub, any, *Error) {
	filter, err := parseListenFilter(req)
	if err != nil {
		return nil, nil, err
	}
	sub := &listenSub{id: req.ID, filter: filter, emit: emit}
	s.listenMu.Lock()
	s.listens = append(s.listens, sub)
	s.listenMu.Unlock()
	return sub, ackParams(req.ID, filter), nil
}

func parseListenFilter(req *Request) (listenFilter, *Error) {
	var filter listenFilter
	if req == nil || len(req.Params) == 0 {
		filter.resourceURIs = map[string]bool{}
		return filter, nil
	}
	var params struct {
		Notifications map[string]json.RawMessage `json:"notifications"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return filter, &Error{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)}
	}
	filter.resourceURIs = map[string]bool{}
	if raw, ok := params.Notifications["toolsListChanged"]; ok {
		_ = json.Unmarshal(raw, &filter.toolsListChanged)
	}
	if raw, ok := params.Notifications["promptsListChanged"]; ok {
		_ = json.Unmarshal(raw, &filter.promptsListChanged)
	}
	if raw, ok := params.Notifications["resourcesListChanged"]; ok {
		_ = json.Unmarshal(raw, &filter.resourcesListChanged)
	}
	if raw, ok := params.Notifications["resourceSubscriptions"]; ok {
		var uris []string
		if err := json.Unmarshal(raw, &uris); err != nil {
			return filter, &Error{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)}
		}
		for _, uri := range uris {
			if uri != "" {
				filter.resourceURIs[uri] = true
			}
		}
	}
	return filter, nil
}

func ackParams(id any, filter listenFilter) map[string]any {
	notifications := map[string]any{}
	if filter.toolsListChanged {
		notifications["toolsListChanged"] = true
	}
	if filter.promptsListChanged {
		notifications["promptsListChanged"] = true
	}
	if filter.resourcesListChanged {
		notifications["resourcesListChanged"] = true
	}
	if len(filter.resourceURIs) > 0 {
		uris := make([]string, 0, len(filter.resourceURIs))
		for uri := range filter.resourceURIs {
			uris = append(uris, uri)
		}
		// Stable order so the ack is deterministic.
		slices.Sort(uris)
		notifications["resourceSubscriptions"] = uris
	}
	return map[string]any{
		"_meta":         map[string]any{metaSubscriptionID: id},
		"notifications": notifications,
	}
}

func (s *Server) handleSubscriptionsListen(req *Request) *Response {
	sub, ack, err := s.openListen(req, nil)
	if err != nil {
		return &Response{JSONRPC: "2.0", ID: req.ID, Error: err}
	}
	// The ack is a notification. The JSON-RPC result is sent only when the
	// subscription closes (notifications/cancelled, or the HTTP stream ending).
	_ = s.SendNotification("notifications/subscriptions/acknowledged", ack)
	s.markListenReady(sub)
	return nil
}

func (s *Server) handleCancelled(req *Request) *Response {
	var params struct {
		RequestID any `json:"requestId"`
	}
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &params)
	}
	if params.RequestID == nil {
		return nil
	}
	sub := s.takeListen(params.RequestID)
	if sub == nil {
		return nil
	}
	return &Response{
		JSONRPC: "2.0",
		ID:      sub.id,
		Result: map[string]any{
			"resultType": "complete",
			"_meta":      map[string]any{metaSubscriptionID: sub.id},
		},
	}
}

func (s *Server) markListenReady(sub *listenSub) {
	if sub == nil {
		return
	}
	s.listenMu.Lock()
	sub.ready = true
	s.listenMu.Unlock()
}

func (s *Server) takeListen(id any) *listenSub {
	key := idKey(id)
	s.listenMu.Lock()
	defer s.listenMu.Unlock()
	for i, sub := range s.listens {
		if idKey(sub.id) == key {
			s.listens = append(s.listens[:i], s.listens[i+1:]...)
			return sub
		}
	}
	return nil
}

func (s *Server) closeListen(sub *listenSub) *Response {
	if sub == nil {
		return nil
	}
	s.listenMu.Lock()
	for i, existing := range s.listens {
		if existing == sub {
			s.listens = append(s.listens[:i], s.listens[i+1:]...)
			break
		}
	}
	s.listenMu.Unlock()
	return &Response{
		JSONRPC: "2.0",
		ID:      sub.id,
		Result: map[string]any{
			"resultType": "complete",
			"_meta":      map[string]any{metaSubscriptionID: sub.id},
		},
	}
}

func (s *Server) fanoutResourceUpdated(uri string) {
	s.listenMu.Lock()
	targets := make([]*listenSub, 0, len(s.listens))
	for _, sub := range s.listens {
		if sub.ready && sub.filter.resourceURIs[uri] {
			targets = append(targets, sub)
		}
	}
	s.listenMu.Unlock()
	for _, sub := range targets {
		params := map[string]any{
			"_meta": map[string]any{metaSubscriptionID: sub.id},
			"uri":   uri,
		}
		if sub.emit != nil {
			sub.emit("notifications/resources/updated", params)
			continue
		}
		_ = s.SendNotification("notifications/resources/updated", params)
	}
}

func idKey(id any) string {
	raw, err := json.Marshal(id)
	if err != nil {
		return fmt.Sprint(id)
	}
	return string(raw)
}

// headerContractError reports HeaderMismatch when a 2026 HTTP request's
// Mcp-Method / Mcp-Name / MCP-Protocol-Version headers disagree with the body.
// 2025 requests are not required to send the headers; a session that omits
// them stays valid.
func headerContractError(protocolHeader, methodHeader, nameHeader string, req *Request) *Error {
	protocolHeader = strings.TrimSpace(protocolHeader)
	meta, hasMeta := metaProtocolVersionOf(req)
	if protocolHeader != "" && hasMeta && protocolHeader != meta {
		return &Error{Code: codeHeaderMismatch, Message: "MCP-Protocol-Version header disagrees with the request protocol version"}
	}
	if (protocolHeader == protocolVersion20260728 && !hasMeta) || (hasMeta && meta == protocolVersion20260728 && protocolHeader == "") {
		return &Error{Code: codeHeaderMismatch, Message: "MCP-Protocol-Version header disagrees with the request protocol version"}
	}
	if protocolHeader != "" && !supportedProtocol(protocolHeader) && (!hasMeta || protocolHeader == meta) {
		return unsupportedProtocol(protocolHeader)
	}
	if hasMeta && !supportedProtocol(meta) && (protocolHeader == "" || protocolHeader == meta) {
		return unsupportedProtocol(meta)
	}
	version, verr := protocolOf(req)
	if verr != nil {
		return verr
	}
	if !isProtocol2026(version) {
		return nil
	}
	methodHeader = strings.TrimSpace(methodHeader)
	if methodHeader == "" {
		return &Error{Code: codeHeaderMismatch, Message: "Mcp-Method header is required for protocol 2026-07-28"}
	}
	if methodHeader != req.Method {
		return &Error{Code: codeHeaderMismatch, Message: "Mcp-Method header disagrees with the request method"}
	}
	want, required := mcpNameSource(req)
	if !required {
		return nil
	}
	got, err := decodeHeaderValue(nameHeader)
	if err != nil {
		return &Error{Code: codeHeaderMismatch, Message: "Mcp-Name header is not valid base64"}
	}
	if strings.TrimSpace(got) == "" {
		return &Error{Code: codeHeaderMismatch, Message: "Mcp-Name header is required for " + req.Method}
	}
	if got != want {
		return &Error{Code: codeHeaderMismatch, Message: "Mcp-Name header disagrees with the request body"}
	}
	return nil
}

func mcpNameSource(req *Request) (string, bool) {
	if req == nil {
		return "", false
	}
	switch req.Method {
	case "tools/call", "prompts/get":
		var params struct {
			Name string `json:"name"`
		}
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &params)
		}
		return params.Name, true
	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &params)
		}
		return params.URI, true
	default:
		return "", false
	}
}

func decodeHeaderValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	const prefix = "=?base64?"
	const suffix = "?="
	if strings.HasPrefix(value, prefix) && strings.HasSuffix(value, suffix) {
		encoded := strings.TrimSuffix(strings.TrimPrefix(value, prefix), suffix)
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", err
		}
		return string(decoded), nil
	}
	return value, nil
}

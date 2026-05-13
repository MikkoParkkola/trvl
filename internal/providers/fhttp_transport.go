// fhttp_transport.go provides an HTTP client with Chrome-like HTTP/2 framing
// for bot-detection bypass (Akamai, Cloudflare, PerimeterX).
//
// Problem: Go's x/net/http2 sends SETTINGS, WINDOW_UPDATE, and PRIORITY frames
// that differ from Chrome. Even with a perfect TLS fingerprint (utls), Akamai
// detects the non-Chrome HTTP/2 framing and classifies the request as "b_bot".
//
// Solution: bogdanfinn/fhttp is a fork of Go's net/http that sends Chrome-like
// HTTP/2 frames. We use fhttp's http2.Transport configured with Chrome's exact
// SETTINGS values, combined with our existing utls Chrome146 TLS dialer.
//
// Since fhttp defines its own Request/Response types (incompatible with
// net/http), this file provides a bridge RoundTripper that converts between
// the two type systems, allowing the provider runtime to continue using
// standard *http.Client.
package providers

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	fhttp2 "github.com/bogdanfinn/fhttp/http2"
	butls "github.com/bogdanfinn/utls"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	utls "github.com/refraction-networking/utls"
)

// Chrome HTTP/2 SETTINGS values (Chrome 131+).
// These must match exactly what Chrome sends or Akamai will detect the
// difference. The order is critical — Chrome sends them in this specific
// sequence.
var chromeH2SettingsOrder = []fhttp2.SettingID{
	fhttp2.SettingHeaderTableSize,
	fhttp2.SettingEnablePush,
	fhttp2.SettingMaxConcurrentStreams,
	fhttp2.SettingInitialWindowSize,
	fhttp2.SettingMaxFrameSize,
	fhttp2.SettingMaxHeaderListSize,
}

var chromeH2Settings = map[fhttp2.SettingID]uint32{
	fhttp2.SettingHeaderTableSize:      65536,
	fhttp2.SettingEnablePush:           0,
	fhttp2.SettingMaxConcurrentStreams: 1000,
	fhttp2.SettingInitialWindowSize:    6291456,
	fhttp2.SettingMaxFrameSize:         16384,
	fhttp2.SettingMaxHeaderListSize:    262144,
}

// chromeConnectionFlow is the initial WINDOW_UPDATE value Chrome sends.
const chromeConnectionFlow = 15663105

// chromePseudoHeaderOrder is the order Chrome sends HTTP/2 pseudo-headers.
var chromePseudoHeaderOrder = []string{
	":method",
	":authority",
	":scheme",
	":path",
}

// newChromeH2Client creates an *http.Client that impersonates Chrome at both
// the TLS layer (utls Chrome146) and the HTTP/2 framing layer (fhttp).
// This makes requests indistinguishable from Chrome to bot-detection systems
// like Akamai that fingerprint both layers.
func newChromeH2Client() *http.Client {
	jar, _ := cookiejar.New(nil)

	transport := &fhttpBridgeTransport{
		h2: &fhttp2.Transport{
			DialTLS: func(network, addr string, _ *butls.Config) (net.Conn, error) {
				// Ignore the bogdanfinn/utls config — use our own Chrome146
				// TLS dialer from batchexec, which uses refraction-networking/utls
				// with Chrome 146's exact ClientHello.
				return dialTLSChrome146(context.Background(), network, addr)
			},
			Settings:          chromeH2Settings,
			SettingsOrder:     chromeH2SettingsOrder,
			ConnectionFlow:    chromeConnectionFlow,
			PseudoHeaderOrder: chromePseudoHeaderOrder,
			HeaderPriority: &fhttp2.PriorityParam{
				StreamDep: 0,
				Exclusive: true,
				Weight:    255,
			},
		},
	}

	return &http.Client{
		Transport: transport,
		Jar:       jar,
		Timeout:   30 * time.Second,
	}
}

// dialTLSChrome146 performs a Chrome 146 TLS handshake advertising both "h2"
// and "http/1.1" in ALPN — matching what real Chrome 146 sends. It reuses
// the Chrome146Spec from batchexec which includes X25519MLKEM768 post-quantum
// key exchange and GREASE ECH.
func dialTLSChrome146(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("split host: %w", err)
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	rawConn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("dial tcp: %w", err)
	}

	spec := batchexec.Chrome146Spec()
	uConn := utls.UClient(rawConn, &utls.Config{ServerName: host}, utls.HelloCustom)
	if err := uConn.ApplyPreset(&spec); err != nil {
		_ = uConn.Close()
		return nil, fmt.Errorf("apply chrome146 preset: %w", err)
	}

	if err := uConn.HandshakeContext(ctx); err != nil {
		_ = uConn.Close()
		return nil, fmt.Errorf("utls handshake: %w", err)
	}

	slog.Debug("fhttp chrome tls handshake", "proto", uConn.ConnectionState().NegotiatedProtocol)
	return uConn, nil
}

// fhttpBridgeTransport implements net/http.RoundTripper by delegating to
// fhttp's http2.Transport. It converts between standard library and fhttp
// types since fhttp is a fork with its own type system.
type fhttpBridgeTransport struct {
	h2 *fhttp2.Transport
}

func (t *fhttpBridgeTransport) RoundTrip(stdReq *http.Request) (*http.Response, error) {
	// Convert net/http.Request to fhttp.Request.
	fReq, err := toFHTTPRequest(stdReq)
	if err != nil {
		return nil, fmt.Errorf("fhttp bridge: convert request: %w", err)
	}

	// Execute via fhttp's HTTP/2 transport (Chrome-like framing).
	fResp, err := t.h2.RoundTrip(fReq)
	if err != nil {
		return nil, err
	}

	// Convert fhttp.Response back to net/http.Response.
	return toStdResponse(fResp, stdReq), nil
}

// toFHTTPRequest converts a standard net/http.Request to an fhttp.Request.
// The body is shared (not copied) since only one consumer reads it.
func toFHTTPRequest(stdReq *http.Request) (*fhttp.Request, error) {
	fReq := &fhttp.Request{
		Method:           stdReq.Method,
		URL:              stdReq.URL,
		Proto:            stdReq.Proto,
		ProtoMajor:       stdReq.ProtoMajor,
		ProtoMinor:       stdReq.ProtoMinor,
		Header:           fhttp.Header(stdReq.Header),
		Body:             stdReq.Body,
		GetBody:          stdReq.GetBody,
		ContentLength:    stdReq.ContentLength,
		TransferEncoding: stdReq.TransferEncoding,
		Close:            stdReq.Close,
		Host:             stdReq.Host,
	}

	// Propagate the context so cancellation and deadlines work.
	fReq = fReq.WithContext(stdReq.Context())

	return fReq, nil
}

// toStdResponse converts an fhttp.Response to a standard net/http.Response.
// The body is shared (not copied) since only one consumer reads it.
func toStdResponse(fResp *fhttp.Response, stdReq *http.Request) *http.Response {
	return &http.Response{
		Status:           fResp.Status,
		StatusCode:       fResp.StatusCode,
		Proto:            fResp.Proto,
		ProtoMajor:       fResp.ProtoMajor,
		ProtoMinor:       fResp.ProtoMinor,
		Header:           http.Header(fResp.Header),
		Body:             fResp.Body,
		ContentLength:    fResp.ContentLength,
		TransferEncoding: fResp.TransferEncoding,
		Close:            fResp.Close,
		Uncompressed:     fResp.Uncompressed,
		Trailer:          http.Header(fResp.Trailer),
		Request:          stdReq,
	}
}

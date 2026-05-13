package waf

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/grafana/sobek"
)

//go:embed stubs.js
var stubsJS string

// cookieEntry mirrors a document.cookie=... assignment so the Go side can
// fish the WAF token out after the challenge resolves.
type cookieEntry struct {
	Name  string
	Value string
	Raw   string
	At    time.Time
}

// vmHost wires a sobek Runtime to the event loop, HTTP client, and cookie
// sink. All host bridges live as methods on vmHost so tests can construct a
// naked host independently of the full SolveAWSWAF flow.
type vmHost struct {
	vm        *sobek.Runtime
	loop      *eventLoop
	client    *http.Client
	origin    string
	userAgent string
	fetchCap  int // max outstanding fetches; keeps challenge scripts from DoSing us
	maxBody   int64
	cookies   []cookieEntry
	logger    func(msg string, args ...any)
}

const (
	defaultFetchCap = 32
	defaultMaxBody  = 4 << 20 // 4 MiB is plenty for a challenge response
)

func newVMHost(vm *sobek.Runtime, loop *eventLoop, client *http.Client, origin, ua string) *vmHost {
	return &vmHost{vm: vm, loop: loop, client: client, origin: origin, userAgent: ua, fetchCap: defaultFetchCap, maxBody: defaultMaxBody}
}

// install registers every host bridge on the global object and then evaluates
// stubs.js to populate the rest of the browser-like surface.
func (h *vmHost) install() error {
	vm := h.vm
	if err := vm.Set("__goOrigin", h.origin); err != nil {
		return fmt.Errorf("set origin: %w", err)
	}
	if err := vm.Set("__goUserAgent", h.userAgent); err != nil {
		return fmt.Errorf("set ua: %w", err)
	}
	if err := vm.Set("__goNow", func() int64 { return time.Now().UnixMilli() }); err != nil {
		return fmt.Errorf("set now: %w", err)
	}
	if err := vm.Set("__goLog", func(args ...sobek.Value) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = a.String()
		}
		msg := strings.Join(parts, " ")
		if h.logger != nil {
			h.logger("waf-js: " + msg)
		} else {
			slog.Debug("waf-js", "msg", msg)
		}
	}); err != nil {
		return fmt.Errorf("set log: %w", err)
	}
	if err := vm.Set("__goCryptoRandom", h.cryptoRandom); err != nil {
		return fmt.Errorf("set crypto random: %w", err)
	}
	if err := vm.Set("__goDigest", h.digest); err != nil {
		return fmt.Errorf("set digest: %w", err)
	}
	if err := vm.Set("__goSetTimeout", h.jsSetTimeout); err != nil {
		return fmt.Errorf("set setTimeout: %w", err)
	}
	if err := vm.Set("__goClearTimeout", h.jsClearTimeout); err != nil {
		return fmt.Errorf("set clearTimeout: %w", err)
	}
	if err := vm.Set("__goSetCookie", h.jsSetCookie); err != nil {
		return fmt.Errorf("set cookie sink: %w", err)
	}
	if err := vm.Set("__goFetch", h.jsFetch); err != nil {
		return fmt.Errorf("set fetch: %w", err)
	}
	if _, err := vm.RunScript("stubs.js", stubsJS); err != nil {
		return fmt.Errorf("eval stubs.js: %w", err)
	}
	return nil
}

// cryptoRandom fills a typed array (viewed through sobek as an Object with
// numeric-indexed byte slots) with bytes from crypto/rand. Accepts either a
// Uint8Array directly or an ArrayBuffer-like with a .buffer property.
func (h *vmHost) cryptoRandom(call sobek.FunctionCall) sobek.Value {
	if len(call.Arguments) == 0 {
		panic(h.vm.NewTypeError("crypto.getRandomValues requires a typed array"))
	}
	obj := call.Argument(0).ToObject(h.vm)
	lenVal := obj.Get("length")
	if lenVal == nil {
		panic(h.vm.NewTypeError("crypto.getRandomValues: not an array"))
	}
	n := int(lenVal.ToInteger())
	if n <= 0 {
		return call.Argument(0)
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(h.vm.NewGoError(err))
	}
	for i := 0; i < n; i++ {
		if err := obj.Set(fmt.Sprintf("%d", i), buf[i]); err != nil {
			panic(h.vm.NewGoError(err))
		}
	}
	return call.Argument(0)
}

// digest runs a SHA-2 hash in Go and hands back a Uint8Array to JS.
// algorithm is the SubtleCrypto name: SHA-256 / SHA-384 / SHA-512.
// data may be a Uint8Array, ArrayBuffer, or plain array of numbers.
func (h *vmHost) digest(call sobek.FunctionCall) sobek.Value {
	if len(call.Arguments) < 2 {
		panic(h.vm.NewTypeError("__goDigest requires (algorithm, data)"))
	}
	alg := strings.ToUpper(strings.TrimSpace(call.Argument(0).String()))
	data := readByteView(h.vm, call.Argument(1))
	var sum []byte
	switch alg {
	case "SHA-256", "SHA256":
		s := sha256.Sum256(data)
		sum = s[:]
	case "SHA-384", "SHA384":
		s := sha512.Sum384(data)
		sum = s[:]
	case "SHA-512", "SHA512":
		s := sha512.Sum512(data)
		sum = s[:]
	default:
		panic(h.vm.NewTypeError("unsupported digest algorithm: " + alg))
	}
	return newUint8Array(h.vm, sum)
}

// jsSetTimeout is the JS-facing setTimeout/setInterval bridge.
// (cb, delayMS, repeat) => id.
func (h *vmHost) jsSetTimeout(call sobek.FunctionCall) sobek.Value {
	if len(call.Arguments) == 0 {
		return h.vm.ToValue(0)
	}
	cb, ok := sobek.AssertFunction(call.Argument(0))
	if !ok {
		return h.vm.ToValue(0)
	}
	ms := int64(0)
	if len(call.Arguments) > 1 {
		ms = call.Argument(1).ToInteger()
	}
	repeat := false
	if len(call.Arguments) > 2 {
		repeat = call.Argument(2).ToBoolean()
	}
	if ms < 0 {
		ms = 0
	}
	id := h.loop.scheduleTimer(cb, time.Duration(ms)*time.Millisecond, repeat)
	return h.vm.ToValue(id)
}

func (h *vmHost) jsClearTimeout(call sobek.FunctionCall) sobek.Value {
	if len(call.Arguments) == 0 {
		return sobek.Undefined()
	}
	id := call.Argument(0).ToInteger()
	h.loop.clearTimer(id)
	return sobek.Undefined()
}

func (h *vmHost) jsSetCookie(call sobek.FunctionCall) sobek.Value {
	if len(call.Arguments) < 3 {
		return sobek.Undefined()
	}
	name := call.Argument(0).String()
	val := call.Argument(1).String()
	raw := call.Argument(2).String()
	h.cookies = append(h.cookies, cookieEntry{Name: name, Value: val, Raw: raw, At: time.Now()})
	return sobek.Undefined()
}

// jsFetch is the bridge behind window.fetch. Signature: (url, init, cb).
// cb is a Node-style (err, response) callback; calling it must be done on the
// loop goroutine, so we route back through enqueueFromGo.
func (h *vmHost) jsFetch(call sobek.FunctionCall) sobek.Value {
	if len(call.Arguments) < 3 {
		panic(h.vm.NewTypeError("__goFetch requires (url, init, cb)"))
	}
	target := call.Argument(0).String()
	init := call.Argument(1).ToObject(h.vm)
	cb, ok := sobek.AssertFunction(call.Argument(2))
	if !ok {
		panic(h.vm.NewTypeError("__goFetch callback must be a function"))
	}

	method := "GET"
	if init != nil {
		if m := init.Get("method"); m != nil && !sobek.IsUndefined(m) && !sobek.IsNull(m) {
			method = strings.ToUpper(m.String())
		}
	}
	headers := map[string]string{}
	if init != nil {
		if hv := init.Get("headers"); hv != nil && !sobek.IsUndefined(hv) && !sobek.IsNull(hv) {
			if hObj, ok := hv.(*sobek.Object); ok {
				for _, k := range hObj.Keys() {
					v := hObj.Get(k)
					if v != nil {
						headers[k] = v.String()
					}
				}
			}
		}
	}
	var body io.Reader
	if init != nil {
		if bv := init.Get("body"); bv != nil && !sobek.IsUndefined(bv) && !sobek.IsNull(bv) {
			switch v := bv.Export().(type) {
			case string:
				body = strings.NewReader(v)
			case []byte:
				body = strings.NewReader(string(v))
			default:
				body = strings.NewReader(bv.String())
			}
		}
	}

	if h.loop.pending >= h.fetchCap {
		err := errors.New("waf: fetch concurrency cap exceeded")
		_, _ = cb(sobek.Undefined(), h.vm.ToValue(err.Error()), sobek.Undefined())
		return sobek.Undefined()
	}

	resolved := resolveURL(h.origin, target)
	req, err := http.NewRequest(method, resolved, body)
	if err != nil {
		_, _ = cb(sobek.Undefined(), h.vm.ToValue(err.Error()), sobek.Undefined())
		return sobek.Undefined()
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" && h.userAgent != "" {
		req.Header.Set("User-Agent", h.userAgent)
	}

	h.loop.trackPending(+1)
	go func() {
		resp, doErr := h.client.Do(req)
		var (
			status     int
			statusText string
			bodyBytes  []byte
			respHdrs   = map[string]string{}
			respURL    = req.URL.String()
		)
		if doErr == nil {
			defer func() { _ = resp.Body.Close() }()
			status = resp.StatusCode
			statusText = resp.Status
			bodyBytes, doErr = io.ReadAll(io.LimitReader(resp.Body, h.maxBody))
			respURL = resp.Request.URL.String()
			for k, v := range resp.Header {
				if len(v) > 0 {
					respHdrs[k] = v[0]
				}
			}
		}
		h.loop.enqueueFromGo(func() error {
			defer h.loop.trackPending(-1)
			if doErr != nil {
				_, cbErr := cb(sobek.Undefined(), h.vm.ToValue(doErr.Error()), sobek.Undefined())
				return cbErr
			}
			raw := h.vm.NewObject()
			_ = raw.Set("status", status)
			_ = raw.Set("statusText", statusText)
			_ = raw.Set("url", respURL)
			_ = raw.Set("redirected", false)
			_ = raw.Set("body", newUint8Array(h.vm, bodyBytes))
			hdrs := h.vm.NewObject()
			for k, v := range respHdrs {
				_ = hdrs.Set(k, v)
			}
			_ = raw.Set("headers", hdrs)
			_, cbErr := cb(sobek.Undefined(), sobek.Undefined(), raw)
			return cbErr
		})
	}()
	return sobek.Undefined()
}

// resolveURL turns a possibly-relative challenge URL into an absolute URL
// using the page origin as the base. If either parse fails we just return
// target — http.NewRequest will reject it cleanly.
func resolveURL(origin, target string) string {
	if target == "" {
		return origin
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	base, err := url.Parse(origin)
	if err != nil {
		return target
	}
	ref, err := url.Parse(target)
	if err != nil {
		return target
	}
	return base.ResolveReference(ref).String()
}

// readByteView coerces a JS value (Uint8Array / ArrayBuffer / number-array /
// string) into a Go byte slice. This is deliberately permissive — challenge
// scripts are known to pass in whatever they like.
func readByteView(vm *sobek.Runtime, v sobek.Value) []byte {
	if v == nil || sobek.IsUndefined(v) || sobek.IsNull(v) {
		return nil
	}
	if s, ok := v.Export().(string); ok {
		return []byte(s)
	}
	obj := v.ToObject(vm)
	// ArrayBuffer: unwrap .byteLength / index via DataView-like path by
	// checking for a `buffer` property first (TypedArray views expose it).
	if buf := obj.Get("buffer"); buf != nil && !sobek.IsUndefined(buf) && !sobek.IsNull(buf) {
		if bo, ok := buf.(*sobek.Object); ok {
			obj = bo
		}
	}
	// Try direct ArrayBuffer.
	if ab, ok := obj.Export().(sobek.ArrayBuffer); ok {
		return ab.Bytes()
	}
	// Fallback: iterate 0..length like a regular array.
	lenVal := obj.Get("length")
	if lenVal == nil {
		return nil
	}
	n := int(lenVal.ToInteger())
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		iv := obj.Get(fmt.Sprintf("%d", i))
		if iv == nil {
			continue
		}
		out[i] = byte(iv.ToInteger() & 0xff)
	}
	return out
}

// newUint8Array allocates a real Uint8Array in the VM and copies src into it.
// We go via ArrayBuffer → Uint8Array constructor so challenge code can use
// .buffer / .byteLength naturally.
func newUint8Array(vm *sobek.Runtime, src []byte) sobek.Value {
	ab := vm.NewArrayBuffer(append([]byte(nil), src...))
	ctor := vm.Get("Uint8Array")
	if ctor == nil {
		// Extremely defensive — if the host tampered with the global Uint8Array
		// we fall back to a plain hex string so digest tests can still assert.
		return vm.ToValue(hex.EncodeToString(src))
	}
	obj, err := vm.New(ctor, vm.ToValue(ab))
	if err != nil {
		return vm.ToValue(hex.EncodeToString(src))
	}
	return obj
}

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// TestServeStdio_PingNotBlockedBySlowToolCall proves the stdio server handles
// requests concurrently: a long-running tool call must not block a `ping`.
// Regression for the gateway "Not connected" wedge — a health-probing transport
// pings while a 15-30s travel search is in flight; if the read loop were serial
// the ping would queue behind the search and the probe would time out.
func TestServeStdio_PingNotBlockedBySlowToolCall(t *testing.T) {
	s := NewServer()

	started := make(chan struct{})
	s.handlers["slowtool"] = func(_ context.Context, _ map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
		close(started)
		time.Sleep(600 * time.Millisecond)
		return []ContentBlock{{Type: "text", Text: "done"}}, nil, nil
	}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	serveDone := make(chan error, 1)
	go func() { serveDone <- s.ServeStdio(inR, outW) }()

	send := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}

	// Reader: record the order in which the ping (id 3) and slowtool (id 2)
	// responses arrive.
	order := make(chan int, 4)
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			var resp Response
			if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
				continue
			}
			if resp.ID == nil {
				continue // notification/log
			}
			if f, ok := resp.ID.(float64); ok {
				id := int(f)
				if id == 2 || id == 3 {
					order <- id
				}
			}
		}
	}()

	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"}}})
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "slowtool", "arguments": map[string]any{}}})

	// Only send the ping once the slow tool is actually running.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("slow tool never started")
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "ping"})

	first := waitID(t, order)
	second := waitID(t, order)
	if first != 3 {
		t.Fatalf("ping (id 3) should respond before the slow tool (id 2); got order %d then %d", first, second)
	}

	_ = inW.Close()
	select {
	case <-serveDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ServeStdio did not return after input closed")
	}
}

func waitID(t *testing.T, ch <-chan int) int {
	t.Helper()
	select {
	case id := <-ch:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a response")
		return 0
	}
}

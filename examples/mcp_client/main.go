// Minimal MCP JSON-RPC client that connects to the trvl MCP server and
// performs a flight search. Shows how to use trvl programmatically via MCP.
//
// Usage:
//
//	go run ./examples/mcp_client
//
// Requires the trvl binary in PATH (or adjust the command below).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
)

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func main() {
	cmd := exec.Command("trvl", "mcp")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "stdin pipe: %v\n", err)
		os.Exit(1)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "stdout pipe: %v\n", err)
		os.Exit(1)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "start trvl mcp: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = cmd.Process.Kill() }()

	reader := bufio.NewReader(stdout)

	// Step 1: Initialize
	send(stdin, request{
		JSONRPC: "2.0", ID: 1, Method: "initialize",
		Params: map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "example-client", "version": "0.1.0"},
		},
	})
	resp := recv(reader)
	fmt.Printf("Server: %s\n", resp.Result)

	// Step 2: Send initialized notification
	send(stdin, request{JSONRPC: "2.0", Method: "notifications/initialized"})

	// Step 3: Call search_flights
	send(stdin, request{
		JSONRPC: "2.0", ID: 2, Method: "tools/call",
		Params: map[string]any{
			"name": "search_flights",
			"arguments": map[string]any{
				"origin":         "HEL",
				"destination":    "BCN",
				"departure_date": "2026-07-01",
			},
		},
	})
	resp = recv(reader)
	if resp.Error != nil {
		fmt.Printf("Error: %s\n", resp.Error.Message)
	} else {
		fmt.Printf("Result: %s\n", resp.Result)
	}

	_ = stdin.Close()
	_ = cmd.Wait()
}

func send(w io.Writer, req request) {
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	_, _ = w.Write(data)
}

func recv(r *bufio.Reader) response {
	line, _ := r.ReadBytes('\n')
	var resp response
	_ = json.Unmarshal(line, &resp)
	return resp
}

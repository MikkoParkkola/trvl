// Command ferryhopper-ports regenerates internal/ground/ferryhopper_ports.txt,
// the port list behind Ferryhopper's launch gate, from one call to
// Ferryhopper's public MCP get_ports tool: one port name per line, trimmed,
// de-duplicated and byte-sorted. Run it from the repository root:
//
//	go run ./scripts/ferryhopper-ports
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const mcpURL = "https://mcp.ferryhopper.com/mcp"

func main() {
	out := flag.String("out", "internal/ground/ferryhopper_ports.txt", "file to write")
	flag.Parse()

	names, err := fetchPortNames()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ferryhopper-ports:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, []byte(strings.Join(names, "\n")+"\n"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "ferryhopper-ports:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %d ports to %s\n", len(names), *out)
}

// fetchPortNames calls get_ports once and returns the sorted, unique names.
func fetchPortNames() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_ports","arguments":{}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "trvl/1.0 (travel agent; github.com/MikkoParkkola/trvl)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get_ports: HTTP %d", resp.StatusCode)
	}

	// The answer is a Server-Sent Events stream; the JSON-RPC result is on a
	// "data: " line.
	var answer struct {
		Result struct {
			StructuredContent struct {
				Ports []struct {
					Name string `json:"name"`
				} `json:"ports"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			if err := json.Unmarshal([]byte(data), &answer); err != nil {
				return nil, fmt.Errorf("decode get_ports: %w", err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var names []string
	for _, p := range answer.Result.StructuredContent.Ports {
		if name := strings.TrimSpace(p.Name); name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, errors.New("get_ports returned no ports")
	}
	sort.Strings(names)
	return names, nil
}

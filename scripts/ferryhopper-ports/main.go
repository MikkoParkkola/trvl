// Command ferryhopper-ports regenerates internal/ground/ferryhopper_ports.txt,
// the port list behind Ferryhopper's launch gate, from one call to
// Ferryhopper's public MCP get_ports tool: one port name per line, trimmed,
// de-duplicated and byte-sorted. Mainland country groups ("GERMANY (ALL
// PORTS)") are left out so a country never counts as a port; island countries
// stay (see keepCountryGroups). Run it from the repository root:
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
	"io"
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

	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "ferryhopper-ports:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_ports","arguments":{}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "trvl/1.0 (travel agent; github.com/MikkoParkkola/trvl)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("get_ports: HTTP %d", resp.StatusCode)
	}

	names, err := portNames(resp.Body)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, render(names), 0o600); err != nil {
		return err
	}
	fmt.Printf("wrote %d ports to %s\n", len(names), out)
	return nil
}

// portNames reads a get_ports Server-Sent Events reply and returns the port
// names trimmed, de-duplicated and byte-sorted, without country groups.
func portNames(r io.Reader) ([]string, error) {
	var answer struct {
		Result struct {
			StructuredContent struct {
				Ports []struct {
					Name    string `json:"name"`
					Country string `json:"countryName"`
				} `json:"ports"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	sc := bufio.NewScanner(r)
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
		name := strings.TrimSpace(p.Name)
		if name == "" || seen[name] || isCountryGroup(name, p.Country) {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, errors.New("get_ports returned no ports")
	}
	sort.Strings(names)
	return names, nil
}

// keepCountryGroups lists the country groups that stay: island countries,
// where the country is the place people sail to ("Pozzallo to Malta").
// Ferryhopper's 22 country groups on 2026-11-16 were Albania, Algeria,
// Bermuda, Croatia, Denmark, Estonia, Finland, Germany, Guadeloupe, Ireland,
// Latvia, Malta, Montenegro, Morocco, Netherlands, Norway, Poland, Slovenia,
// Spain, Tunisia, Turkey and Uruguay; the 18 mainland ones are dropped.
var keepCountryGroups = map[string]bool{
	"BERMUDA (ALL PORTS)":    true,
	"GUADELOUPE (ALL PORTS)": true,
	"IRELAND (ALL PORTS)":    true,
	"MALTA (ALL PORTS)":      true,
}

// isCountryGroup reports Ferryhopper's per-country "X (ALL PORTS)" entry for
// a mainland country, which must not become a place name.
func isCountryGroup(name, country string) bool {
	base, ok := strings.CutSuffix(name, " (ALL PORTS)")
	return ok && country != "" && strings.EqualFold(base, strings.TrimSpace(country)) && !keepCountryGroups[name]
}

// render is the file format: one name per line, newline-terminated.
func render(names []string) []byte {
	return []byte(strings.Join(names, "\n") + "\n")
}

package ground

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// TestLiveProbe_FerryhopperPortsCurrent compares the embedded port list with
// Ferryhopper's live get_ports answer. A live port missing from the file would
// be wrongly skipped by the launch gate; regenerate it with
// go run ./scripts/ferryhopper-ports.
func TestLiveProbe_FerryhopperPortsCurrent(t *testing.T) {
	testutil.RequireLiveProbe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_ports","arguments":{}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ferryhopperMCPURL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := ferryhopperClient.Do(req)
	testutil.SkipIfTransient(t, err) // throttling or a timeout is not drift
	if err != nil {
		t.Fatalf("get_ports: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		statusErr := fmt.Errorf("get_ports: HTTP %d", resp.StatusCode)
		testutil.SkipIfTransient(t, statusErr)
		t.Fatal(statusErr)
	}

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
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			if err := json.Unmarshal([]byte(data), &answer); err != nil {
				t.Fatalf("decode get_ports: %v", err)
			}
		}
	}
	seen := map[string]bool{}
	var live []string
	// Country groups ("GERMANY (ALL PORTS)") stay out of the file, except the
	// island countries scripts/ferryhopper-ports keeps (its keepCountryGroups).
	keptIslandGroups := map[string]bool{
		"BERMUDA (ALL PORTS)": true, "GUADELOUPE (ALL PORTS)": true,
		"IRELAND (ALL PORTS)": true, "MALTA (ALL PORTS)": true,
	}
	countryGroups := map[string]bool{}
	for _, p := range answer.Result.StructuredContent.Ports {
		name := strings.TrimSpace(p.Name)
		if base, group := strings.CutSuffix(name, " (ALL PORTS)"); group && strings.EqualFold(base, strings.TrimSpace(p.Country)) {
			countryGroups[name] = true
			if !keptIslandGroups[name] {
				continue
			}
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		live = append(live, name)
	}
	if len(live) == 0 {
		t.Fatal("get_ports returned no ports")
	}
	sort.Strings(live)

	embedded := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(ferryhopperPortList, "\n"), "\n") {
		embedded[line] = true
	}
	for name := range keptIslandGroups {
		if !embedded[name] {
			t.Errorf("%s is a kept island country and must be in ferryhopper_ports.txt", name)
		}
	}
	var missing, dropped []string
	for _, name := range live {
		if !embedded[name] {
			missing = append(missing, name)
		}
	}
	for name := range embedded {
		switch {
		case countryGroups[name] && !keptIslandGroups[name]:
			t.Errorf("%s is a mainland country group and must not be in ferryhopper_ports.txt", name)
		case !seen[name]:
			dropped = append(dropped, name)
		}
	}
	sort.Strings(dropped)
	if len(missing) > 0 {
		t.Errorf("%d live ports missing from ferryhopper_ports.txt (first: %q); regenerate it with go run ./scripts/ferryhopper-ports", len(missing), missing[:min(5, len(missing))])
	}
	if len(dropped) > 0 {
		t.Errorf("%d ports in ferryhopper_ports.txt are gone from the live list (first: %q); regenerate it with go run ./scripts/ferryhopper-ports", len(dropped), dropped[:min(5, len(dropped))])
	}
	t.Logf("live ports %d (incl. %d kept island groups; %d country groups in all); embedded %d", len(live), len(keptIslandGroups), len(countryGroups), len(embedded))
}

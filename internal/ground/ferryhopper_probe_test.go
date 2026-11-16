package ground

import (
	"bufio"
	"context"
	"encoding/json"
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
	if err != nil {
		t.Fatalf("get_ports: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

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
				t.Fatalf("decode get_ports: %v", err)
			}
		}
	}
	seen := map[string]bool{}
	var live []string
	for _, p := range answer.Result.StructuredContent.Ports {
		if name := strings.TrimSpace(p.Name); name != "" && !seen[name] {
			seen[name] = true
			live = append(live, name)
		}
	}
	if len(live) == 0 {
		t.Fatal("get_ports returned no ports")
	}
	sort.Strings(live)

	embedded := map[string]bool{}
	for _, line := range strings.Split(ferryhopperPortList, "\n") {
		embedded[line] = true
	}
	var missing []string
	for _, name := range live {
		if !embedded[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d live ports missing from ferryhopper_ports.txt (first: %q); regenerate it with go run ./scripts/ferryhopper-ports", len(missing), missing[:min(5, len(missing))])
	}
	t.Logf("live ports %d, embedded %d", len(live), len(embedded)-1)
}

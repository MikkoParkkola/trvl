package mcp

import (
	"strings"
	"testing"
)

func TestSearchResultToolsAdvertiseMCPAppResource(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := NewServer()

	for _, name := range []string{"search_accommodations", "search_hotels_with_details", "search_hotels", "search_flights"} {
		tool, ok := s.toolDefs[name]
		if !ok {
			t.Fatalf("tool %q not registered", name)
		}
		ui, ok := tool.Meta["ui"].(map[string]any)
		if !ok {
			t.Fatalf("%s _meta.ui = %#v, want object", name, tool.Meta["ui"])
		}
		if got := ui["resourceUri"]; got != trvlSearchResultsAppURI {
			t.Fatalf("%s resourceUri = %v, want %s", name, got, trvlSearchResultsAppURI)
		}
		if got := tool.Meta["ui/resourceUri"]; got != trvlSearchResultsAppURI {
			t.Fatalf("%s ui/resourceUri = %v, want %s", name, got, trvlSearchResultsAppURI)
		}
	}
}

func TestSearchResultsAppResourceIsReadable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := NewServer()

	result, err := s.readResource(trvlSearchResultsAppURI)
	if err != nil {
		t.Fatalf("readResource(%s): %v", trvlSearchResultsAppURI, err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("contents len = %d, want 1", len(result.Contents))
	}
	content := result.Contents[0]
	if content.MimeType != mcpAppResourceMimeType {
		t.Fatalf("mime type = %q, want %q", content.MimeType, mcpAppResourceMimeType)
	}
	if !strings.Contains(content.Text, "trvl search results") {
		t.Fatalf("app HTML missing title")
	}
	ui, ok := content.Meta["ui"].(map[string]any)
	if !ok {
		t.Fatalf("resource _meta.ui = %#v, want object", content.Meta["ui"])
	}
	if _, ok := ui["csp"].(map[string]any); !ok {
		t.Fatalf("resource _meta.ui.csp = %#v, want object", ui["csp"])
	}
}

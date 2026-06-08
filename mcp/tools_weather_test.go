package mcp

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

func TestGetWeatherTool_Definition(t *testing.T) {
	t.Parallel()
	tool := getWeatherTool()
	if tool.Name != "get_weather" {
		t.Errorf("Name = %q, want get_weather", tool.Name)
	}
	if len(tool.InputSchema.Required) == 0 {
		t.Error("expected required params")
	}
}

func TestHandleGetWeather_Success(t *testing.T) {
	t.Parallel()
	testutil.RequireLiveProbe(t)
	content, structured, err := handleGetWeather(context.Background(), map[string]any{
		"city": "Rome",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content")
	}
	if structured == nil {
		t.Fatal("expected structured result")
	}
}

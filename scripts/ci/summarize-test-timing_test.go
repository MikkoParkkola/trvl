package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSummarizeEventsSortsSlowestFirst(t *testing.T) {
	input := strings.Join([]string{
		`{"Action":"pass","Package":"example/watch","Test":"TestFast","Elapsed":0.01}`,
		`{"Action":"pass","Package":"example/watch","Test":"TestSlow","Elapsed":1.25}`,
		`{"Action":"pass","Package":"example/watch","Elapsed":1.26}`,
	}, "\n")

	timings, failed, err := summarizeEvents(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if failed {
		t.Fatal("successful stream reported failure")
	}
	if len(timings) != 2 || timings[0].Name != "TestSlow" || timings[1].Name != "TestFast" {
		t.Fatalf("unexpected timings: %#v", timings)
	}
}

func TestRunPropagatesTestFailure(t *testing.T) {
	input := strings.Join([]string{
		`{"Action":"fail","Package":"example/watch","Test":"TestBroken","Elapsed":0.02}`,
		`{"Action":"fail","Package":"example/watch","Elapsed":0.03}`,
	}, "\n")

	var output bytes.Buffer
	if err := run(strings.NewReader(input), &output, ""); err == nil {
		t.Fatal("run succeeded for a failing test stream")
	}
	if !strings.Contains(output.String(), "TestBroken") {
		t.Fatalf("summary omitted failed test: %q", output.String())
	}
}

func TestRunRejectsRelativeSummaryPath(t *testing.T) {
	input := strings.Join([]string{
		`{"Action":"pass","Package":"example/watch","Test":"TestSafe","Elapsed":0.01}`,
		`{"Action":"pass","Package":"example/watch","Elapsed":0.02}`,
	}, "\n")
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	summaryPath, err := filepath.Rel(workingDir, filepath.Join(t.TempDir(), "summary.md"))
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err = run(strings.NewReader(input), &output, summaryPath)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("run accepted relative summary path %q: %v", summaryPath, err)
	}
}

func TestSummarizeEventsRejectsTruncatedStream(t *testing.T) {
	input := `{"Action":"run","Package":"example/watch","Test":"TestNeverFinished"}`
	if _, _, err := summarizeEvents(strings.NewReader(input)); err == nil {
		t.Fatal("truncated stream was accepted")
	}
}

func TestSummarizeEventsRejectsMalformedJSON(t *testing.T) {
	if _, _, err := summarizeEvents(strings.NewReader("not-json")); err == nil {
		t.Fatal("malformed JSON was accepted")
	}
}

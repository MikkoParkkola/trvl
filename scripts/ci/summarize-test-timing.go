package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

type testEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
}

type testTiming struct {
	Name    string
	Elapsed float64
	Failed  bool
}

type streamOutcome int

const (
	outcomeOK streamOutcome = iota
	outcomeTestFailed
	outcomePackageFailed
)

func summarizeEvents(r io.Reader) ([]testTiming, streamOutcome, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var timings []testTiming
	testFailed := false
	packageFailed := false
	sawPackageResult := false
	for scanner.Scan() {
		var event testEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, outcomeOK, fmt.Errorf("decode go test event: %w", err)
		}
		switch event.Action {
		case "pass", "fail", "skip":
			if event.Test == "" {
				sawPackageResult = true
				packageFailed = packageFailed || event.Action == "fail"
				continue
			}
			failed := event.Action == "fail"
			timings = append(timings, testTiming{
				Name:    event.Test,
				Elapsed: event.Elapsed,
				Failed:  failed,
			})
			testFailed = testFailed || failed
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, outcomeOK, fmt.Errorf("read go test events: %w", err)
	}
	if !sawPackageResult {
		return nil, outcomeOK, errors.New("go test stream ended without a package result")
	}
	outcome := outcomeOK
	if testFailed {
		outcome = outcomeTestFailed
	} else if packageFailed {
		// go test -timeout kills the package and emits a package fail after the
		// tests that finished have already been reported as pass. That is a
		// timeout, not a failing test.
		outcome = outcomePackageFailed
	}

	sort.Slice(timings, func(i, j int) bool {
		if timings[i].Elapsed == timings[j].Elapsed {
			return timings[i].Name < timings[j].Name
		}
		return timings[i].Elapsed > timings[j].Elapsed
	})
	return timings, outcome, nil
}

func writeSummary(w io.Writer, timings []testTiming) error {
	if _, err := fmt.Fprintln(w, "## Slowest internal/watch tests"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Test | Seconds | Result |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | ---: | --- |"); err != nil {
		return err
	}
	limit := min(10, len(timings))
	for _, timing := range timings[:limit] {
		result := "pass"
		if timing.Failed {
			result = "fail"
		}
		if _, err := fmt.Fprintf(w, "| `%s` | %.3f | %s |\n", timing.Name, timing.Elapsed, result); err != nil {
			return err
		}
	}
	return nil
}

func appendSummaryFile(summaryPath string, timings []testTiming) (err error) {
	if !filepath.IsAbs(summaryPath) {
		return fmt.Errorf("GitHub step summary path must be absolute")
	}
	cleanPath := filepath.Clean(summaryPath)
	root, err := os.OpenRoot(filepath.Dir(cleanPath))
	if err != nil {
		return fmt.Errorf("open GitHub step summary directory: %w", err)
	}
	defer func() {
		if closeErr := root.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close GitHub step summary directory: %w", closeErr)
		}
	}()

	file, err := root.OpenFile(filepath.Base(cleanPath), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open GitHub step summary: %w", err)
	}
	if err := writeSummary(file, timings); err != nil {
		_ = file.Close()
		return fmt.Errorf("write GitHub step summary: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close GitHub step summary: %w", err)
	}
	return nil
}

func run(input io.Reader, output io.Writer, summaryPath string) error {
	timings, outcome, err := summarizeEvents(input)
	if err != nil {
		return err
	}
	if err := writeSummary(output, timings); err != nil {
		return fmt.Errorf("write console summary: %w", err)
	}
	if summaryPath != "" {
		if err := appendSummaryFile(summaryPath, timings); err != nil {
			return err
		}
	}
	switch outcome {
	case outcomeTestFailed:
		return errors.New("internal/watch tests failed")
	case outcomePackageFailed:
		return errors.New("go test reported a package failure without a failing test (timeout or crash)")
	default:
		return nil
	}
}

func main() {
	if err := run(os.Stdin, os.Stdout, os.Getenv("GITHUB_STEP_SUMMARY")); err != nil {
		fmt.Fprintf(os.Stderr, "watch test timing: %v\n", err)
		os.Exit(1)
	}
}

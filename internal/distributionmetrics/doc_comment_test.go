package distributionmetrics

import (
	"os"
	"strings"
	"testing"
)

func TestPackageCommentNamesThePackage(t *testing.T) {
	body, err := os.ReadFile("metrics.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "// Package distributionmetrics ") {
		t.Fatal("metrics.go is missing its package comment")
	}
}

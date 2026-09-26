package testutil

import (
	"os"
	"strings"
	"testing"
)

func TestPackageCommentNamesThePackage(t *testing.T) {
	body, err := os.ReadFile("transient.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "// Package testutil ") {
		t.Fatal("transient.go is missing its package comment")
	}
}

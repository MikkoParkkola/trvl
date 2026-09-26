package searchctx

import (
	"os"
	"strings"
	"testing"
)

func TestPackageCommentNamesThePackage(t *testing.T) {
	body, err := os.ReadFile("searchctx.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "// Package searchctx ") {
		t.Fatal("searchctx.go is missing its package comment")
	}
}

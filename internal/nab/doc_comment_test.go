package nab

import (
	"os"
	"strings"
	"testing"
)

func TestPackageCommentNamesThePackage(t *testing.T) {
	body, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "// Package nab ") {
		t.Fatal("client.go is missing its package comment")
	}
}

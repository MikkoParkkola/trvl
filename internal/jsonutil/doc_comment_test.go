package jsonutil

import (
	"os"
	"strings"
	"testing"
)

func TestPackageCommentNamesThePackage(t *testing.T) {
	body, err := os.ReadFile("jsonutil.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "// Package jsonutil ") {
		t.Fatal("jsonutil.go is missing its package comment")
	}
}

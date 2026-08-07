package main

import "testing"

func TestScanSourceFindsMultilineURLValue(t *testing.T) {
	source := []byte(`package sample
import "log/slog"
func f(target string) {
	slog.Warn("request",
		"target_url",
		target,
	)
}`)
	findings, checked, err := scanSource("sample.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if checked != 1 || len(findings) != 1 {
		t.Fatalf("checked=%d findings=%#v, want one finding", checked, findings)
	}
}

func TestScanSourceAcceptsRedactedURLValues(t *testing.T) {
	source := []byte(`package sample
import (
	"log/slog"
	"github.com/MikkoParkkola/trvl/internal/logredact"
)
func f(target string) {
	slog.Debug("request", "url", logredact.URL(target))
	slog.Info("request", slog.String("captcha_url", logredact.URL(target)))
}`)
	findings, checked, err := scanSource("sample.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if checked != 2 || len(findings) != 0 {
		t.Fatalf("checked=%d findings=%#v, want two clean sites", checked, findings)
	}
}

func TestScanSourceFindsStructuredAttributeURL(t *testing.T) {
	source := []byte(`package sample
import "log/slog"
func f(target string) { slog.Error("request", slog.Any("webhook_url", target)) }`)
	findings, checked, err := scanSource("sample.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if checked != 1 || len(findings) != 1 {
		t.Fatalf("checked=%d findings=%#v, want one finding", checked, findings)
	}
}

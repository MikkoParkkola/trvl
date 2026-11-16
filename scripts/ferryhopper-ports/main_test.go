package main

import (
	"strings"
	"testing"
)

// TestPortNames feeds a recorded-shape get_ports reply offline and checks the
// file it would write: trimmed, de-duplicated, byte-sorted names without
// country groups, one per line and newline-terminated, the same form as
// internal/ground/ferryhopper_ports.txt.
func TestPortNames(t *testing.T) {
	reply := "event: message\n" +
		`data: {"jsonrpc":"2.0","id":1,"result":{"structuredContent":{"ports":[` +
		`{"name":" PIRAEUS ","countryName":"Greece","code":"PIR"},` +
		`{"name":"THIRA (SANTORINI)","countryName":"Greece","code":"JTR"},` +
		`{"name":"PIRAEUS","countryName":"Greece","code":"PIR"},` +
		`{"name":"GERMANY (ALL PORTS)","countryName":"Germany","code":"DE"},` +
		`{"name":"MALTA (ALL PORTS)","countryName":"Malta","code":"MT"},` +
		`{"name":"ATHENS (ALL PORTS)","countryName":"Greece","code":"ATH"},` +
		`{"name":"","countryName":"Greece","code":"X"},` +
		`{"name":"ABBADIA L.","countryName":"Italy","code":"ABB"}]}}}` + "\n\n"

	names, err := portNames(strings.NewReader(reply))
	if err != nil {
		t.Fatalf("portNames: %v", err)
	}
	want := "ABBADIA L.\nATHENS (ALL PORTS)\nMALTA (ALL PORTS)\nPIRAEUS\nTHIRA (SANTORINI)\n"
	if got := string(render(names)); got != want {
		t.Errorf("rendered file =\n%q\nwant\n%q", got, want)
	}
}

func TestPortNames_EmptyReplyIsAnError(t *testing.T) {
	reply := "event: message\n" + `data: {"result":{"structuredContent":{"ports":[]}}}` + "\n\n"
	if _, err := portNames(strings.NewReader(reply)); err == nil {
		t.Error("want an error for a reply without ports, so the file is never emptied")
	}
}

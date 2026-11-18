package models

import "testing"

// TestIsGoogleHotelID accepts exactly the three Google shapes the rooms
// lookup can resolve and nothing that merely looks like "provider:id".
func TestIsGoogleHotelID(t *testing.T) {
	cases := map[string]bool{
		"/g/11b6d4_v_4":                         true,
		"ChIJI85wbpwHbUcRiJL_xm_zLE4":           true,
		"0x476d079e5136ca9f:0xfdc2e58a51a25b46": true,
		" ChIJI85wbpwHbUcRiJL_xm_zLE4 ":         true,
		"flatio:98765":                          false,
		"agoda:123":                             false,
		"4711":                                  false,
		"/g/":                                   false,
		"ChIJ":                                  false,
		"0x12:0xzz":                             false,
		"":                                      false,
	}
	for id, want := range cases {
		if got := IsGoogleHotelID(id); got != want {
			t.Errorf("IsGoogleHotelID(%q) = %v, want %v", id, got, want)
		}
	}
}

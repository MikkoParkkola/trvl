package ground

import "testing"

func TestProviderEnabled(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		include  []string
		exclude  []string
		want     bool
	}{
		{"no filters allows all", "flixbus", nil, nil, true},
		{"exclude wins", "rome2rio", nil, []string{"rome2rio"}, false},
		{"exclude is case-insensitive", "Rome2Rio", nil, []string{"rome2rio"}, false},
		{"exclude beats include", "rome2rio", []string{"rome2rio"}, []string{"rome2rio"}, false},
		{"include allow-list permits listed", "flixbus", []string{"flixbus"}, nil, true},
		{"include allow-list blocks unlisted", "regiojet", []string{"flixbus"}, nil, false},
		{"not excluded passes through", "viking line", nil, []string{"rome2rio"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := providerEnabled(tc.provider, tc.include, tc.exclude); got != tc.want {
				t.Errorf("providerEnabled(%q, %v, %v) = %v, want %v",
					tc.provider, tc.include, tc.exclude, got, tc.want)
			}
		})
	}
}

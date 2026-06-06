package models

import "testing"

func TestIsAdultsOnly(t *testing.T) {
	tests := []struct {
		name string
		hn   string
		desc string
		want bool
	}{
		{"name adults only", "Madeira Gardens Adults Only", "", true},
		{"name hyphen", "TUI BLUE Adults-Only Resort", "", true},
		{"name adult only singular", "Serenity Adult Only Spa", "", true},
		{"desc adults recommended", "Quiet Retreat", "A calm hotel; adults recommended.", true},
		{"desc case insensitive", "Resort", "ADULTS ONLY property", true},
		{"family hotel name", "Family Fun Resort", "Great for kids", false},
		{"empty", "", "", false},
		{"contains adult substring only", "Adultsville Hotel", "near downtown", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAdultsOnly(tt.hn, tt.desc); got != tt.want {
				t.Errorf("IsAdultsOnly(%q, %q) = %v, want %v", tt.hn, tt.desc, got, tt.want)
			}
		})
	}
}

package ground

import (
	"strings"
	"testing"
)

func TestLookupTallinkPort(t *testing.T) {
	tests := []struct {
		city     string
		wantCode string
		wantCity string
		wantOK   bool
	}{
		// Helsinki aliases
		{"Helsinki", "HEL", "Helsinki", true},
		{"helsinki", "HEL", "Helsinki", true},
		{"hel", "HEL", "Helsinki", true},
		{"  Helsinki  ", "HEL", "Helsinki", true},

		// Tallinn aliases — new API uses TAL
		{"Tallinn", "TAL", "Tallinn", true},
		{"tallinn", "TAL", "Tallinn", true},
		{"tal", "TAL", "Tallinn", true},
		{"tll", "TAL", "Tallinn", true}, // legacy alias still resolves
		{"tln", "TAL", "Tallinn", true}, // legacy alias still resolves

		// Stockholm aliases
		{"Stockholm", "STO", "Stockholm", true},
		{"stockholm", "STO", "Stockholm", true},
		{"sto", "STO", "Stockholm", true},

		// Riga aliases
		{"Riga", "RIG", "Riga", true},
		{"riga", "RIG", "Riga", true},
		{"rig", "RIG", "Riga", true},

		// Turku aliases
		{"Turku", "TUR", "Turku", true},
		{"turku", "TUR", "Turku", true},
		{"tur", "TUR", "Turku", true},
		{"åbo", "TUR", "Turku", true},

		// Åland / Mariehamn — new code ALA
		{"Mariehamn", "ALA", "Mariehamn", true},
		{"mar", "ALA", "Mariehamn", true},
		{"ala", "ALA", "Mariehamn", true},

		// Långnäs maps to ALA now
		{"lng", "ALA", "Mariehamn", true},

		// Paldiski
		{"Paldiski", "PAL", "Paldiski", true},
		{"pal", "PAL", "Paldiski", true},

		// Kapellskär
		{"Kapellskär", "KAP", "Kapellskär", true},
		{"kap", "KAP", "Kapellskär", true},

		// Visby
		{"Visby", "VIS", "Visby", true},
		{"vis", "VIS", "Visby", true},

		// Non-existent
		{"", "", "", false},
		{"London", "", "", false},
		{"Paris", "", "", false},
		{"Atlantis", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.city, func(t *testing.T) {
			port, ok := LookupTallinkPort(tt.city)
			if ok != tt.wantOK {
				t.Fatalf("LookupTallinkPort(%q) ok = %v, want %v", tt.city, ok, tt.wantOK)
			}
			if ok {
				if port.Code != tt.wantCode {
					t.Errorf("Code = %q, want %q", port.Code, tt.wantCode)
				}
				if port.City != tt.wantCity {
					t.Errorf("City = %q, want %q", port.City, tt.wantCity)
				}
				if port.Name == "" {
					t.Errorf("Name should not be empty for %q", tt.city)
				}
			}
		})
	}
}

func TestHasTallinkPort(t *testing.T) {
	if !HasTallinkPort("Helsinki") {
		t.Error("Helsinki should have a Tallink port")
	}
	if !HasTallinkPort("Tallinn") {
		t.Error("Tallinn should have a Tallink port")
	}
	if HasTallinkPort("London") {
		t.Error("London should not have a Tallink port")
	}
	if HasTallinkPort("") {
		t.Error("empty city should not have a Tallink port")
	}
}

func TestHasTallinkRoute(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		{"Helsinki", "Tallinn", true},
		{"Tallinn", "Helsinki", true},
		{"Stockholm", "Tallinn", true},
		{"Stockholm", "Riga", true},
		{"Stockholm", "Helsinki", true},
		{"Helsinki", "Stockholm", true},
		{"Helsinki", "Paldiski", true},
		{"Kapellskär", "Paldiski", true},
		{"Helsinki", "London", false}, // London not a Tallink port
		{"London", "Tallinn", false},
		{"Atlantis", "Helsinki", false},
		{"Helsinki", "Atlantis", false},
		{"", "Helsinki", false},
		{"Helsinki", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.from+"->"+tt.to, func(t *testing.T) {
			got := HasTallinkRoute(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("HasTallinkRoute(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestTallinkRouteDuration(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want int
	}{
		{"HEL", "TAL", 120},
		{"TAL", "HEL", 120},
		{"STO", "TAL", 960},
		{"TAL", "STO", 960},
		{"STO", "HEL", 960},
		{"HEL", "STO", 960},
		{"STO", "RIG", 1020},
		{"RIG", "STO", 1020},
		{"TUR", "STO", 660},
		{"STO", "TUR", 660},
		{"PAL", "KAP", 540},
		{"KAP", "PAL", 540},
		// Unknown route falls back to 120
		{"HEL", "RIG", 120},
	}

	for _, tt := range tests {
		t.Run(tt.from+"-"+tt.to, func(t *testing.T) {
			got := tallinkRouteDuration(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("tallinkRouteDuration(%q, %q) = %d, want %d", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestTallinkNormalizeDateTime(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2026-05-01T07:30", "2026-05-01T07:30:00"},    // short format → normalized
		{"2026-05-01T07:30:00", "2026-05-01T07:30:00"}, // already full → unchanged
		{"2026-04-05T00:00:00", "2026-04-05T00:00:00"}, // full format
		{"", ""}, // empty
		{"2026-05-01T13:30", "2026-05-01T13:30:00"}, // afternoon
	}

	for _, tt := range tests {
		got := tallinkNormalizeDateTime(tt.input)
		if got != tt.want {
			t.Errorf("tallinkNormalizeDateTime(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildTallinkBookingURL(t *testing.T) {
	u := buildTallinkBookingURL("HEL", "TAL", "2026-04-10")
	if u == "" {
		t.Fatal("booking URL should not be empty")
	}
	if !strings.Contains(u, "tallink.com") {
		t.Errorf("URL should contain tallink.com, got %q", u)
	}
	if !strings.Contains(u, "2026-04-10") {
		t.Errorf("URL should contain date, got %q", u)
	}
	if !strings.Contains(u, "voyageType=TRANSPORT") {
		t.Errorf("URL should contain voyageType=TRANSPORT, got %q", u)
	}
	// URL uses booking.tallink.com with lowercase port codes
	if !strings.Contains(u, "booking.tallink.com") {
		t.Errorf("URL should use booking.tallink.com, got %q", u)
	}
	if !strings.Contains(u, "from=hel") {
		t.Errorf("URL should contain from=hel (lowercase), got %q", u)
	}
	if !strings.Contains(u, "to=tal") {
		t.Errorf("URL should contain to=tal (lowercase), got %q", u)
	}
}

func TestTallinkShipSuffix(t *testing.T) {
	if got := tallinkShipSuffix(""); got != "" {
		t.Errorf("empty ship name should return empty, got %q", got)
	}
	if got := tallinkShipSuffix("MEGASTAR"); got != " (MEGASTAR)" {
		t.Errorf("ship suffix = %q, want %q", got, " (MEGASTAR)")
	}
}

func TestNewUUID(t *testing.T) {
	id := newUUID()
	if id == "" {
		t.Fatal("UUID should not be empty")
	}
	// UUID v4 format: 8-4-4-4-12 hex chars
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Fatalf("UUID should have 5 parts, got %d: %q", len(parts), id)
	}

	// Generate two UUIDs and verify they are different.
	id2 := newUUID()
	if id == id2 {
		t.Error("two consecutive UUIDs should not be identical")
	}
}

func TestTallinkAllPortsHaveRequiredFields(t *testing.T) {
	for alias, port := range tallinkPorts {
		if port.Code == "" {
			t.Errorf("port alias %q has empty Code", alias)
		}
		if port.Name == "" {
			t.Errorf("port alias %q has empty Name", alias)
		}
		if port.City == "" {
			t.Errorf("port alias %q has empty City", alias)
		}
	}
}

func TestTallinkIsOvernightRoute(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		// Overnight routes (>= 600 min)
		{"HEL", "STO", true}, // 960 min
		{"STO", "HEL", true}, // 960 min
		{"TUR", "STO", true}, // 660 min
		{"STO", "TUR", true}, // 660 min
		{"STO", "RIG", true}, // 1020 min
		{"RIG", "STO", true}, // 1020 min
		{"HEL", "VIS", true}, // 780 min
		{"VIS", "HEL", true}, // 780 min
		{"STO", "TAL", true}, // 960 min
		{"TAL", "STO", true}, // 960 min

		// Shuttle routes (< 600 min)
		{"HEL", "TAL", false}, // 120 min
		{"TAL", "HEL", false}, // 120 min
		{"HEL", "ALA", false}, // 360 min
		{"ALA", "HEL", false}, // 360 min
		{"PAL", "KAP", false}, // 540 min
		{"KAP", "PAL", false}, // 540 min

		// Unknown route defaults to 120 min
		{"HEL", "RIG", false},
	}

	for _, tt := range tests {
		t.Run(tt.from+"-"+tt.to, func(t *testing.T) {
			got := tallinkIsOvernightRoute(tt.from, tt.to)
			if got != tt.want {
				d := tallinkRouteDuration(tt.from, tt.to)
				t.Errorf("tallinkIsOvernightRoute(%q, %q) = %v, want %v (duration: %d min, threshold: %d)",
					tt.from, tt.to, got, tt.want, d, tallinkOvernightThreshold)
			}
		})
	}
}

func TestTallinkExtractSessionGUID(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{
			name: "valid GUID",
			html: `<script>window.Env = { sessionGuid: '904EA629-7889-4E23-9CF9-98139E0184E1', locale: 'en' };</script>`,
			want: "904EA629-7889-4E23-9CF9-98139E0184E1",
		},
		{
			name: "no sessionGuid",
			html: `<script>window.Env = { locale: 'en' };</script>`,
			want: "",
		},
		{
			name: "empty HTML",
			html: "",
			want: "",
		},
		{
			name: "malformed — no closing quote",
			html: `sessionGuid: 'ABC123`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tallinkExtractSessionGUID(tt.html)
			if got != tt.want {
				t.Errorf("tallinkExtractSessionGUID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTallinkFormatCabinClasses(t *testing.T) {
	tests := []struct {
		name    string
		classes []tallinkCabinClass
		want    string
	}{
		{
			name:    "empty",
			classes: nil,
			want:    "",
		},
		{
			name: "single cabin",
			classes: []tallinkCabinClass{
				{Code: "A2", Price: 89.0},
			},
			want: "Cabins: A2 €89",
		},
		{
			name: "multiple cabins",
			classes: []tallinkCabinClass{
				{Code: "A2", Price: 89.0},
				{Code: "B4", Price: 65.0},
				{Code: "Deck", Price: 39.0},
			},
			want: "Cabins: A2 €89, B4 €65, Deck €39",
		},
		{
			name: "skip zero price",
			classes: []tallinkCabinClass{
				{Code: "A2", Price: 89.0},
				{Code: "FREE", Price: 0.0},
			},
			want: "Cabins: A2 €89",
		},
		{
			name: "all zero price",
			classes: []tallinkCabinClass{
				{Code: "FREE", Price: 0.0},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tallinkFormatCabinClasses(tt.classes)
			if got != tt.want {
				t.Errorf("tallinkFormatCabinClasses() = %q, want %q", got, tt.want)
			}
		})
	}
}

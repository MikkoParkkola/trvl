package lounges

// staticDataAsiaPacAmericas is a regional shard of the curated lounge data.
// See static_data.go for the merge into staticData.
var staticDataAsiaPacAmericas = map[string][]staticLounge{
	"PEK": {
		{
			Name:      "Air China First Class Lounge",
			Terminal:  "Terminal 3, Concourse E",
			Cards:     ppLK,
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers"},
			OpenHours: "06:00–22:00",
		},
		{
			Name:      "CNAC Lounge",
			Terminal:  "Terminal 2",
			Cards:     ppDragon,
			Amenities: []string{"Wi-Fi", "Buffet", "Bar"},
			OpenHours: "07:00–22:00",
		},
		{
			Name:      "VIP Lounge (Capital Airlines)",
			Terminal:  "Terminal 3, Concourse D",
			Cards:     ppDragon,
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers"},
			OpenHours: "06:00–22:00",
		},
	},
	"PVG": {
		{
			Name:      "Longemont Lounge",
			Terminal:  "Terminal 1",
			Cards:     ppDragon,
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers"},
			OpenHours: "24 hours",
		},
		{
			Name:      "Dragonair Lounge",
			Terminal:  "Terminal 2",
			Cards:     ppLK,
			Amenities: []string{"Wi-Fi", "Buffet", "Bar", "Showers"},
			OpenHours: "06:00–23:00",
		},
		{
			Name:      "VIP Lounge (China Eastern)",
			Terminal:  "Terminal 2",
			Cards:     ppDragon,
			Amenities: []string{"Wi-Fi", "Hot food", "Bar"},
			OpenHours: "06:00–23:00",
		},
	},
	"SYD": {
		{
			Name:      "Qantas International Business Lounge",
			Terminal:  "Terminal 1 (International)",
			Cards:     ppLK,
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers"},
			OpenHours: "05:30–22:00",
		},
		{
			Name:      "Plaza Premium Lounge",
			Terminal:  "Terminal 1 (International)",
			Cards:     ppDragon,
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers", "Spa"},
			OpenHours: "05:00–22:30",
		},
		{
			Name:      "No1 Lounge Sydney",
			Terminal:  "Terminal 1 (International)",
			Cards:     ppDragon,
			Amenities: []string{"Wi-Fi", "Buffet", "Bar", "Showers"},
			OpenHours: "05:30–21:30",
		},
	},
	"MEL": {
		{
			Name:      "Plaza Premium Lounge",
			Terminal:  "Terminal 2 (International)",
			Cards:     ppDragon,
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers", "Spa"},
			OpenHours: "05:00–22:00",
		},
		{
			Name:      "Qantas International Business Lounge",
			Terminal:  "Terminal 2 (International)",
			Cards:     ppLK,
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers"},
			OpenHours: "05:30–21:30",
		},
	},
	// ── Americas ────────────────────────────────────────────────────────────
	"JFK": {
		{
			Name:      "The Centurion Lounge",
			Terminal:  "Terminal 4",
			Cards:     []string{"Amex Centurion", "Amex Platinum"},
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers"},
			OpenHours: "06:00–22:00",
		},
		{
			Name:      "Plaza Premium Lounge",
			Terminal:  "Terminal 4",
			Cards:     ppDragon,
			Amenities: []string{"Wi-Fi", "Hot food", "Bar"},
			OpenHours: "05:30–22:30",
		},
		{
			Name:      "Wingtips Lounge",
			Terminal:  "Terminal 1",
			Cards:     ppDragon,
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers"},
			OpenHours: "04:30–23:00",
		},
		{
			Name:      "Club at JFK",
			Terminal:  "Terminal 5",
			Cards:     ppDragon,
			Amenities: []string{"Wi-Fi", "Buffet", "Bar"},
			OpenHours: "05:00–21:00",
		},
	},
	"LAX": {
		{
			Name:      "The Centurion Lounge",
			Terminal:  "Terminal 4",
			Cards:     []string{"Amex Centurion", "Amex Platinum"},
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers", "Spa"},
			OpenHours: "06:00–22:00",
		},
		{
			Name:      "The Club at LAX",
			Terminal:  "Tom Bradley International (TBIT)",
			Cards:     ppDragon,
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers"},
			OpenHours: "24 hours",
		},
		{
			Name:      "Plaza Premium Lounge",
			Terminal:  "Tom Bradley International (TBIT)",
			Cards:     ppDragon,
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers", "Spa"},
			OpenHours: "24 hours",
		},
		{
			Name:      "Star Alliance Lounge",
			Terminal:  "Tom Bradley International (TBIT)",
			Cards:     ppLK,
			Amenities: []string{"Wi-Fi", "Buffet", "Bar", "Showers"},
			OpenHours: "06:00–22:00",
		},
		// Airline-specific lounges (NOT Priority Pass)
		{
			Name:      "Delta Sky Club",
			Terminal:  "Terminal 2",
			Cards:     []string{"Delta One", "Delta Business Class", "SkyTeam Elite Plus", "Delta SkyMiles Diamond", "Delta SkyMiles Platinum", "Delta Sky Club Membership"},
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers", "Sky Deck"},
			OpenHours: "04:30–23:00",
		},
		{
			Name:      "Delta Sky Club",
			Terminal:  "Terminal 3",
			Cards:     []string{"Delta One", "Delta Business Class", "SkyTeam Elite Plus", "Delta SkyMiles Diamond", "Delta SkyMiles Platinum", "Delta Sky Club Membership"},
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers"},
			OpenHours: "04:30–23:00",
		},
		{
			Name:      "United Polaris Lounge",
			Terminal:  "Terminal 7",
			Cards:     []string{"United Business Class", "United First Class", "Star Alliance Gold", "United MileagePlus 1K", "United MileagePlus Global Services"},
			Amenities: []string{"Wi-Fi", "À la carte dining", "Bar", "Showers", "Daybeds", "Workstations"},
			OpenHours: "06:00–23:00",
		},
	},
	"SFO": {
		{
			Name:      "The Centurion Lounge",
			Terminal:  "Terminal 3",
			Cards:     []string{"Amex Centurion", "Amex Platinum"},
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers"},
			OpenHours: "05:30–22:00",
		},
		{
			Name:      "United Club",
			Terminal:  "Terminal 3, Gate F",
			Cards:     ppLK,
			Amenities: []string{"Wi-Fi", "Snacks", "Bar"},
			OpenHours: "05:00–22:00",
		},
		{
			Name:      "The Club at SFO",
			Terminal:  "International Terminal G",
			Cards:     ppDragon,
			Amenities: []string{"Wi-Fi", "Hot food", "Bar", "Showers"},
			OpenHours: "05:00–23:00",
		},
		// Airline-specific lounges (NOT Priority Pass)
		{
			Name:      "United Polaris Lounge",
			Terminal:  "International Terminal G",
			Cards:     []string{"United Business Class", "United First Class", "Star Alliance Gold", "United MileagePlus 1K", "United MileagePlus Global Services"},
			Amenities: []string{"Wi-Fi", "À la carte dining", "Bar", "Showers", "Daybeds", "Workstations"},
			OpenHours: "06:00–23:30",
		},
	},
}

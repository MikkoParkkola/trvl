package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/spf13/cobra"
)

func prefsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prefs",
		Short: "Manage personal travel preferences",
		Long: `View and manage your personal travel preferences.
Preferences are stored in ~/.trvl/preferences.json and applied automatically
to hotel searches, flight origin defaults, and currency display.

Examples:
  trvl prefs                              # show all preferences
  trvl prefs set home_airports HEL,AMS   # set home airports
  trvl prefs set carry_on_only true       # boolean preference
  trvl prefs set min_hotel_rating 8.0    # numeric preference (0-10 scale)
  trvl prefs set display_currency EUR    # set display currency
  trvl prefs set preferred_districts Prague=Prague 1,Prague 2,Prague 5
  trvl prefs edit                        # open in $EDITOR
  trvl prefs init                        # interactive first-time setup`,
		RunE: runPrefsShow,
	}

	cmd.AddCommand(prefsSetCmd())
	cmd.AddCommand(prefsEditCmd())
	cmd.AddCommand(prefsInitCmd())
	cmd.AddCommand(prefsAddFamilyMemberCmd())

	return cmd
}

// runPrefsShow prints the current preferences as formatted JSON.
func runPrefsShow(_ *cobra.Command, _ []string) error {
	p, err := preferences.Load()
	if err != nil {
		return fmt.Errorf("load preferences: %w", err)
	}

	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func prefsSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a single preference value",
		Long: `Set a preference by key name. Value is interpreted based on the field type.

Supported keys:
  home_airports       comma-separated IATA codes (e.g. HEL,AMS)
  home_cities         comma-separated city names (e.g. Helsinki,Amsterdam)
  carry_on_only       true/false
  prefer_direct       true/false
  no_dormitories      true/false
  ensuite_only        true/false
  fast_wifi_needed    true/false
  min_hotel_stars     integer 0-5
  min_hotel_rating    decimal 0-10 (e.g. 8.0)
  display_currency    3-letter currency code (e.g. EUR)
  locale              locale string (e.g. en-FI)
  loyalty_airlines    comma-separated IATA codes (e.g. KL,AY)
  loyalty_hotels      comma-separated programme names (e.g. "Marriott Bonvoy,IHG")
  preferred_districts city=district1,district2 (e.g. Prague=Prague 1,Prague 2)`,
		Args: cobra.ExactArgs(2),
		RunE: runPrefsSet,
	}
}

func runPrefsSet(_ *cobra.Command, args []string) error {
	key := strings.ToLower(strings.TrimSpace(args[0]))
	value := strings.TrimSpace(args[1])

	p, err := preferences.Load()
	if err != nil {
		return fmt.Errorf("load preferences: %w", err)
	}

	if err := applyPreference(p, key, value); err != nil {
		return err
	}

	if err := preferences.Save(p); err != nil {
		return fmt.Errorf("save preferences: %w", err)
	}

	fmt.Printf("Set %s = %s\n", key, value)
	return nil
}

// applyPreference applies a single key=value update to p.
func applyPreference(p *preferences.Preferences, key, value string) error {
	switch key {
	case "home_airports":
		p.HomeAirports = splitAndTrim(value)
	case "home_cities":
		p.HomeCities = splitAndTrim(value)
	case "carry_on_only":
		b, err := parseBool(value)
		if err != nil {
			return err
		}
		p.CarryOnOnly = b
	case "prefer_direct":
		b, err := parseBool(value)
		if err != nil {
			return err
		}
		p.PreferDirect = b
	case "no_dormitories":
		b, err := parseBool(value)
		if err != nil {
			return err
		}
		p.NoDormitories = b
	case "ensuite_only":
		b, err := parseBool(value)
		if err != nil {
			return err
		}
		p.EnSuiteOnly = b
	case "fast_wifi_needed":
		b, err := parseBool(value)
		if err != nil {
			return err
		}
		p.FastWifiNeeded = b
	case "min_hotel_stars":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 5 {
			return fmt.Errorf("min_hotel_stars must be an integer 0-5")
		}
		p.MinHotelStars = n
	case "min_hotel_rating":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f < 0 || f > 10 {
			return fmt.Errorf("min_hotel_rating must be a number 0-10")
		}
		p.MinHotelRating = f
	case "display_currency":
		if len(value) != 3 {
			return fmt.Errorf("display_currency must be a 3-letter ISO code (e.g. EUR)")
		}
		p.DisplayCurrency = strings.ToUpper(value)
	case "locale":
		p.Locale = value
	case "loyalty_airlines":
		p.LoyaltyAirlines = splitAndTrim(value)
	case "loyalty_hotels":
		p.LoyaltyHotels = splitAndTrim(value)
	case "preferred_districts":
		// Format: city=district1,district2
		idx := strings.Index(value, "=")
		if idx < 0 {
			return fmt.Errorf("preferred_districts format: city=district1,district2")
		}
		city := strings.TrimSpace(value[:idx])
		districts := splitAndTrim(value[idx+1:])
		if city == "" {
			return fmt.Errorf("preferred_districts: city name required before =")
		}
		if p.PreferredDistricts == nil {
			p.PreferredDistricts = make(map[string][]string)
		}
		if len(districts) == 0 {
			delete(p.PreferredDistricts, city)
		} else {
			p.PreferredDistricts[city] = districts
		}
	default:
		return fmt.Errorf("unknown preference key %q. Run `trvl prefs set --help` for supported keys", key)
	}
	return nil
}

func prefsEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open preferences in $EDITOR",
		RunE:  runPrefsEdit,
	}
}

func runPrefsEdit(_ *cobra.Command, _ []string) error {
	// Determine the file path.
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	path := home + "/.trvl/preferences.json"

	// Ensure the file exists (with defaults) before opening.
	p, err := preferences.Load()
	if err != nil {
		return err
	}
	if err := preferences.Save(p); err != nil {
		return err
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}

	// #nosec G702,G204 -- EDITOR/VISUAL intentionally selects a local executable for
	// this interactive command; exec.Command does not invoke a shell and path is
	// the fixed owner-only preferences file.
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func prefsInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Interactive first-time preferences setup",
		RunE:  runPrefsInit,
	}
}

func runPrefsInit(_ *cobra.Command, _ []string) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("trvl preferences setup")
	fmt.Println("Press Enter to keep the current/default value.")
	fmt.Println()

	p, err := preferences.Load()
	if err != nil {
		return err
	}

	p.HomeAirports = promptStringSlice(scanner, "Home airports (IATA codes, comma-separated)", p.HomeAirports)
	p.HomeCities = promptStringSlice(scanner, "Home cities (comma-separated)", p.HomeCities)
	p.DisplayCurrency = promptString(scanner, "Display currency (e.g. EUR, USD)", p.DisplayCurrency)
	p.Locale = promptString(scanner, "Locale (e.g. en-FI)", p.Locale)
	p.CarryOnOnly = promptBool(scanner, "Carry-on only?", p.CarryOnOnly)
	p.PreferDirect = promptBool(scanner, "Prefer direct flights?", p.PreferDirect)
	p.NoDormitories = promptBool(scanner, "Exclude dormitory/hostel shared rooms?", p.NoDormitories)
	p.EnSuiteOnly = promptBool(scanner, "Require en-suite bathroom?", p.EnSuiteOnly)
	p.FastWifiNeeded = promptBool(scanner, "Need fast wifi (co-working)?", p.FastWifiNeeded)

	minStarsStr := promptString(scanner, "Minimum hotel stars (0=any)", strconv.Itoa(p.MinHotelStars))
	if n, err2 := strconv.Atoi(minStarsStr); err2 == nil && n >= 0 && n <= 5 {
		p.MinHotelStars = n
	}

	minRatingStr := promptString(scanner, "Minimum hotel rating 0-10 (0=any, e.g. 8.0)", formatRating(p.MinHotelRating))
	if f, err2 := strconv.ParseFloat(minRatingStr, 64); err2 == nil && f >= 0 && f <= 10 {
		p.MinHotelRating = f
	}

	if err := preferences.Save(p); err != nil {
		return fmt.Errorf("save preferences: %w", err)
	}

	fmt.Println()
	fmt.Println("Preferences saved. Use `trvl prefs` to review.")
	return nil
}

func prefsAddFamilyMemberCmd() *cobra.Command {
	var notes string
	cmd := &cobra.Command{
		Use:   "add family_member <name> --relationship <rel> [--notes <text>]",
		Short: "Add a family member for booking on their behalf",
		Long: `Add a family member to your preferences for booking travel on their behalf.

Examples:
  trvl prefs add family_member father --notes "prefers sea view, 2 bedrooms"
  trvl prefs add family_member spouse --relationship spouse --notes "no aisle seats"`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "family_member" {
				return fmt.Errorf("expected 'family_member', got %q", args[0])
			}
			name := strings.Join(args[1:], " ")
			relationship, _ := cmd.Flags().GetString("relationship")
			if relationship == "" {
				relationship = name // default: use name as relationship label
			}

			p, err := preferences.Load()
			if err != nil {
				return err
			}

			p.FamilyMembers = append(p.FamilyMembers, preferences.FamilyMember{
				Name:         name,
				Relationship: relationship,
				Notes:        notes,
			})

			if err := preferences.Save(p); err != nil {
				return err
			}

			fmt.Printf("Added family member: %s (%s)\n", name, relationship)
			return nil
		},
	}
	cmd.Flags().StringP("relationship", "r", "", "Relationship (e.g. father, spouse, child)")
	cmd.Flags().StringVarP(&notes, "notes", "n", "", "Free-form notes about their preferences")
	return cmd
}

// --- helpers ---

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1", "on":
		return true, nil
	case "false", "no", "0", "off":
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean %q: use true/false", s)
}

func promptString(scanner *bufio.Scanner, label, current string) string {
	if current != "" {
		fmt.Printf("  %s [%s]: ", label, current)
	} else {
		fmt.Printf("  %s: ", label)
	}
	if scanner.Scan() {
		if v := strings.TrimSpace(scanner.Text()); v != "" {
			return v
		}
	}
	return current
}

func promptBool(scanner *bufio.Scanner, label string, current bool) bool {
	def := "no"
	if current {
		def = "yes"
	}
	raw := promptString(scanner, label+" (yes/no)", def)
	b, err := parseBool(raw)
	if err != nil {
		return current
	}
	return b
}

func promptStringSlice(scanner *bufio.Scanner, label string, current []string) []string {
	def := strings.Join(current, ",")
	raw := promptString(scanner, label, def)
	return splitAndTrim(raw)
}

func formatRating(r float64) string {
	if r == 0 {
		return "0"
	}
	return strconv.FormatFloat(r, 'f', 1, 64)
}

// Package hotelarb contains pure hotel arbitrage helpers for re-booking,
// last-minute deal detection, and hotel points-vs-cash comparisons.
package hotelarb

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/points"
)

const activeHoldsFilename = "active_holds.json"

// Hold is an existing hotel reservation the user may be able to cancel and
// re-book if the same stay reprices lower.
type Hold struct {
	ID                    string    `json:"id"`
	HotelID               string    `json:"hotel_id,omitempty"`
	HotelName             string    `json:"hotel_name"`
	Location              string    `json:"location,omitempty"`
	CheckIn               string    `json:"check_in"`
	CheckOut              string    `json:"check_out"`
	Guests                int       `json:"guests,omitempty"`
	Provider              string    `json:"provider,omitempty"`
	OriginalPrice         float64   `json:"original_price"`
	Currency              string    `json:"currency"`
	Refundable            bool      `json:"refundable"`
	FreeCancellationUntil time.Time `json:"free_cancellation_until,omitempty"`
	BookingURL            string    `json:"booking_url,omitempty"`
	LastSeenPrice         float64   `json:"last_seen_price,omitempty"`
	LastSeenAt            time.Time `json:"last_seen_at,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	Notes                 string    `json:"notes,omitempty"`
}

// PriceQuote is the current price found for an existing hold.
type PriceQuote struct {
	Price      float64   `json:"price"`
	Currency   string    `json:"currency"`
	Provider   string    `json:"provider,omitempty"`
	BookingURL string    `json:"booking_url,omitempty"`
	CheckedAt  time.Time `json:"checked_at,omitempty"`
}

// RebookAction labels the action TRVL recommends for an active hold.
type RebookAction string

const (
	ActionHoldCurrent       RebookAction = "hold_current"
	ActionRebookLowerPrice  RebookAction = "rebook_lower_price"
	defaultLastMinuteDropPC              = 25.0
	defaultLastMinuteHours               = 48.0
)

// RebookOptions controls when a lower quote is worth surfacing.
type RebookOptions struct {
	MinSavings float64
}

// RebookDecision is the user-facing hold-current vs. re-book recommendation.
type RebookDecision struct {
	HoldID                string       `json:"hold_id"`
	HotelName             string       `json:"hotel_name"`
	Action                RebookAction `json:"action"`
	OriginalPrice         float64      `json:"original_price"`
	CurrentPrice          float64      `json:"current_price"`
	Savings               float64      `json:"savings"`
	SavingsPercent        float64      `json:"savings_percent"`
	Currency              string       `json:"currency"`
	Provider              string       `json:"provider,omitempty"`
	BookingURL            string       `json:"booking_url,omitempty"`
	ManualConfirmRequired bool         `json:"manual_confirm_required"`
	Reason                string       `json:"reason"`
}

// EvaluateRebook compares an active hold with a fresh quote and returns the
// lower-price re-book decision. It never authorizes automatic cancellation:
// lower-price decisions always require manual confirmation.
func EvaluateRebook(hold Hold, quote PriceQuote, opts RebookOptions) RebookDecision {
	decision := RebookDecision{
		HoldID:        hold.ID,
		HotelName:     hold.HotelName,
		Action:        ActionHoldCurrent,
		OriginalPrice: hold.OriginalPrice,
		CurrentPrice:  quote.Price,
		Currency:      firstNonEmpty(quote.Currency, hold.Currency),
		Provider:      quote.Provider,
		BookingURL:    quote.BookingURL,
	}

	switch {
	case hold.OriginalPrice <= 0:
		decision.Reason = "current hold has no original price to compare"
		return decision
	case quote.Price <= 0:
		decision.Reason = "no current lower-price quote was available"
		return decision
	case hold.Currency != "" && quote.Currency != "" && !strings.EqualFold(hold.Currency, quote.Currency):
		decision.Reason = fmt.Sprintf("currency mismatch: hold is %s, quote is %s", hold.Currency, quote.Currency)
		return decision
	case !hold.Refundable:
		decision.Savings = hold.OriginalPrice - quote.Price
		decision.SavingsPercent = percentOf(decision.Savings, hold.OriginalPrice)
		decision.Reason = "current hold is not marked refundable, so TRVL will not suggest a re-book"
		return decision
	}

	decision.Savings = hold.OriginalPrice - quote.Price
	decision.SavingsPercent = percentOf(decision.Savings, hold.OriginalPrice)
	if decision.Savings <= 0 {
		decision.Reason = "current price is not lower than the held reservation"
		return decision
	}
	if opts.MinSavings > 0 && decision.Savings < opts.MinSavings {
		decision.Reason = fmt.Sprintf("savings %.2f are below the %.2f minimum", decision.Savings, opts.MinSavings)
		return decision
	}

	decision.Action = ActionRebookLowerPrice
	decision.ManualConfirmRequired = true
	decision.Reason = fmt.Sprintf("re-book manually to save %.2f %s before cancelling the existing hold", decision.Savings, decision.Currency)
	return decision
}

// LastMinuteOptions configures sub-48h hotel deal detection.
type LastMinuteOptions struct {
	DropPercentThreshold float64
	MaxWindowHours       float64
}

// LastMinuteSignal reports whether current availability is materially cheaper
// than the last seen price inside the last-minute booking window.
type LastMinuteSignal struct {
	Triggered       bool    `json:"triggered"`
	LastSeenPrice   float64 `json:"last_seen_price"`
	CurrentPrice    float64 `json:"current_price"`
	DiscountPercent float64 `json:"discount_percent"`
	WindowHours     float64 `json:"window_hours"`
	Reason          string  `json:"reason"`
}

// DetectLastMinuteDeal flags sub-48h hotel availability when the current price
// is at least 25% below the last seen price by default.
func DetectLastMinuteDeal(now, checkIn time.Time, lastSeenPrice, currentPrice float64, opts LastMinuteOptions) LastMinuteSignal {
	threshold := opts.DropPercentThreshold
	if threshold <= 0 {
		threshold = defaultLastMinuteDropPC
	}
	maxWindow := opts.MaxWindowHours
	if maxWindow <= 0 {
		maxWindow = defaultLastMinuteHours
	}

	signal := LastMinuteSignal{
		LastSeenPrice: lastSeenPrice,
		CurrentPrice:  currentPrice,
	}
	if now.IsZero() {
		now = time.Now()
	}
	if checkIn.IsZero() {
		signal.Reason = "check-in time is unknown"
		return signal
	}
	signal.WindowHours = checkIn.Sub(now).Hours()
	switch {
	case signal.WindowHours < 0:
		signal.Reason = "check-in is already in the past"
		return signal
	case signal.WindowHours > maxWindow:
		signal.Reason = fmt.Sprintf("check-in is %.1f hours away, outside the %.1f hour last-minute window", signal.WindowHours, maxWindow)
		return signal
	case lastSeenPrice <= 0:
		signal.Reason = "last seen price is unavailable"
		return signal
	case currentPrice <= 0:
		signal.Reason = "current availability price is unavailable"
		return signal
	}

	signal.DiscountPercent = percentOf(lastSeenPrice-currentPrice, lastSeenPrice)
	if signal.DiscountPercent < threshold {
		signal.Reason = fmt.Sprintf("discount %.1f%% is below the %.1f%% last-minute threshold", signal.DiscountPercent, threshold)
		return signal
	}

	signal.Triggered = true
	signal.Reason = fmt.Sprintf("sub-%.0fh hotel availability is %.1f%% below the last seen price", maxWindow, signal.DiscountPercent)
	return signal
}

// PointsOffer is one points redemption option for the same hotel stay.
type PointsOffer struct {
	Program        string  `json:"program"`
	PointsRequired int     `json:"points_required"`
	CashFees       float64 `json:"cash_fees,omitempty"`
}

// PointsArbitrageInput compares the cash booking price to one or more hotel
// loyalty redemption options.
type PointsArbitrageInput struct {
	CashPrice float64       `json:"cash_price"`
	Currency  string        `json:"currency"`
	Offers    []PointsOffer `json:"offers"`
}

// PointsRecommendation is the high-level recommendation across all offers.
type PointsRecommendation string

const (
	RecommendUsePoints PointsRecommendation = "use_points"
	RecommendPayCash   PointsRecommendation = "pay_cash"
)

// PointsOfferValue is the evaluated value of one hotel points offer.
type PointsOfferValue struct {
	ProgramSlug     string  `json:"program_slug"`
	ProgramName     string  `json:"program_name"`
	PointsRequired  int     `json:"points_required"`
	CashFees        float64 `json:"cash_fees,omitempty"`
	CentsPerPoint   float64 `json:"cents_per_point"`
	FloorCPP        float64 `json:"floor_cpp"`
	CeilingCPP      float64 `json:"ceiling_cpp"`
	OpportunityCost float64 `json:"opportunity_cost"`
	SavingsVsCash   float64 `json:"savings_vs_cash"`
	Verdict         string  `json:"verdict"`
	Reason          string  `json:"reason"`
}

// PointsArbitrageResult is the cash-vs-points recommendation.
type PointsArbitrageResult struct {
	CashPrice      float64              `json:"cash_price"`
	Currency       string               `json:"currency"`
	Recommendation PointsRecommendation `json:"recommendation"`
	BestOffer      PointsOfferValue     `json:"best_offer"`
	Offers         []PointsOfferValue   `json:"offers"`
	Reason         string               `json:"reason"`
}

// ComparePointsArbitrage recommends cash or points by comparing each offer's
// conservative opportunity cost against the cash booking price.
func ComparePointsArbitrage(input PointsArbitrageInput) (*PointsArbitrageResult, error) {
	if input.CashPrice <= 0 {
		return nil, fmt.Errorf("cash price must be greater than 0")
	}
	if len(input.Offers) == 0 {
		return nil, fmt.Errorf("at least one points offer is required")
	}

	values := make([]PointsOfferValue, 0, len(input.Offers))
	for _, offer := range input.Offers {
		v, err := evaluatePointsOffer(input.CashPrice, offer)
		if err != nil {
			return nil, err
		}
		values = append(values, v)
	}

	best := values[0]
	for _, candidate := range values[1:] {
		if candidate.SavingsVsCash > best.SavingsVsCash {
			best = candidate
			continue
		}
		if candidate.SavingsVsCash == best.SavingsVsCash && candidate.CentsPerPoint > best.CentsPerPoint {
			best = candidate
		}
	}

	result := &PointsArbitrageResult{
		CashPrice: input.CashPrice,
		Currency:  firstNonEmpty(strings.ToUpper(input.Currency), "USD"),
		BestOffer: best,
		Offers:    values,
	}
	if best.SavingsVsCash > 0 {
		result.Recommendation = RecommendUsePoints
		result.Reason = fmt.Sprintf("%s beats cash by %.2f %s at conservative floor value", best.ProgramName, best.SavingsVsCash, result.Currency)
	} else {
		result.Recommendation = RecommendPayCash
		result.Reason = fmt.Sprintf("pay cash; best points option costs %.2f %s more than cash at floor value", -best.SavingsVsCash, result.Currency)
	}
	return result, nil
}

func evaluatePointsOffer(cashPrice float64, offer PointsOffer) (PointsOfferValue, error) {
	if offer.PointsRequired <= 0 {
		return PointsOfferValue{}, fmt.Errorf("points required must be greater than 0")
	}
	slug := strings.ToLower(strings.TrimSpace(offer.Program))
	program := points.LookupProgram(slug)
	if program == nil {
		return PointsOfferValue{}, fmt.Errorf("unknown program %q", offer.Program)
	}

	netCashAvoided := cashPrice - offer.CashFees
	if netCashAvoided < 0 {
		netCashAvoided = 0
	}
	cpp := (netCashAvoided * 100) / float64(offer.PointsRequired)
	opportunityCost := offer.CashFees + (float64(offer.PointsRequired) * program.FloorCPP / 100)
	savingsVsCash := cashPrice - opportunityCost
	verdict := "pay cash"
	if savingsVsCash > 0 {
		verdict = "use points"
	}

	return PointsOfferValue{
		ProgramSlug:     program.Slug,
		ProgramName:     program.Name,
		PointsRequired:  offer.PointsRequired,
		CashFees:        offer.CashFees,
		CentsPerPoint:   cpp,
		FloorCPP:        program.FloorCPP,
		CeilingCPP:      program.CeilingCPP,
		OpportunityCost: opportunityCost,
		SavingsVsCash:   savingsVsCash,
		Verdict:         verdict,
		Reason:          fmt.Sprintf("%.2f cents/point vs %.2f floor for %s", cpp, program.FloorCPP, program.Name),
	}, nil
}

// HoldStore persists active hotel holds in active_holds.json.
type HoldStore struct {
	dir   string
	path  string
	mu    sync.Mutex
	holds []Hold
}

// NewHoldStore creates a hold store rooted at dir.
func NewHoldStore(dir string) *HoldStore {
	return &HoldStore{
		dir:  dir,
		path: filepath.Join(dir, activeHoldsFilename),
	}
}

// DefaultHoldStore stores holds under ~/.trvl/active_holds.json.
func DefaultHoldStore() (*HoldStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return NewHoldStore(filepath.Join(home, ".trvl")), nil
}

// Path returns the JSON file path used by the store.
func (s *HoldStore) Path() string {
	return s.path
}

// Load reads active holds from disk and returns a copy of the loaded slice.
func (s *HoldStore) Load() ([]Hold, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var holds []Hold
	if err := loadHoldJSON(s.path, &holds); err != nil {
		return nil, err
	}
	s.holds = holds
	return cloneHolds(s.holds), nil
}

// List returns active holds in insertion order.
func (s *HoldStore) List() []Hold {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneHolds(s.holds)
}

// Get returns a hold by ID.
func (s *HoldStore) Get(id string) (Hold, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.holds {
		if h.ID == id {
			return h, true
		}
	}
	return Hold{}, false
}

// Add validates and persists a new active hold.
func (s *HoldStore) Add(h Hold) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateHold(h); err != nil {
		return "", err
	}
	if h.ID == "" {
		h.ID = newHoldID()
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = time.Now().UTC()
	}
	if h.LastSeenPrice == 0 {
		h.LastSeenPrice = h.OriginalPrice
	}
	s.holds = append(s.holds, h)
	if err := s.saveLocked(); err != nil {
		return "", err
	}
	return h.ID, nil
}

// Update replaces an existing hold.
func (s *HoldStore) Update(h Hold) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h.ID == "" {
		return fmt.Errorf("hold id is required")
	}
	if err := validateHold(h); err != nil {
		return err
	}
	for i := range s.holds {
		if s.holds[i].ID == h.ID {
			s.holds[i] = h
			return s.saveLocked()
		}
	}
	return fmt.Errorf("hold %s not found", h.ID)
}

// Remove deletes an active hold by ID.
func (s *HoldStore) Remove(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.holds {
		if s.holds[i].ID == id {
			s.holds = append(s.holds[:i], s.holds[i+1:]...)
			return true, s.saveLocked()
		}
	}
	return false, nil
}

func (s *HoldStore) saveLocked() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return saveHoldJSON(s.path, s.holds)
}

func validateHold(h Hold) error {
	if strings.TrimSpace(h.HotelName) == "" {
		return fmt.Errorf("hotel name is required")
	}
	if h.OriginalPrice <= 0 {
		return fmt.Errorf("original price must be greater than 0")
	}
	if strings.TrimSpace(h.Currency) == "" {
		return fmt.Errorf("currency is required")
	}
	if err := validateHoldDate("check-in", h.CheckIn); err != nil {
		return err
	}
	if err := validateHoldDate("check-out", h.CheckOut); err != nil {
		return err
	}
	return nil
}

func validateHoldDate(label, value string) error {
	if value == "" {
		return fmt.Errorf("%s date is required", label)
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return fmt.Errorf("%s date must use YYYY-MM-DD", label)
	}
	return nil
}

func saveHoldJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func loadHoldJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	return json.Unmarshal(data, dst)
}

func cloneHolds(in []Hold) []Hold {
	if in == nil {
		return nil
	}
	out := make([]Hold, len(in))
	copy(out, in)
	return out
}

func newHoldID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "hold_" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("hold_%d", time.Now().UnixNano())
}

func percentOf(part, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return part / total * 100
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

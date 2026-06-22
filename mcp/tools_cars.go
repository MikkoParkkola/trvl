package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/cars"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

func searchCarsTool() ToolDef {
	return ToolDef{
		Name:        "search_cars",
		Title:       "Rental Car Search",
		Description: "Search rental car offers for a pickup/dropoff location and date range. Uses optional partner-gated car providers and returns typed setup statuses when credentials are missing.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"pickup_location":  {Type: "string", Description: "Pickup airport, city, station, or address (e.g. HEL, Helsinki Airport, Prague)"},
				"dropoff_location": {Type: "string", Description: "Dropoff airport, city, station, or address. Defaults to pickup_location"},
				"pickup_date":      {Type: "string", Description: "Pickup date (YYYY-MM-DD)"},
				"dropoff_date":     {Type: "string", Description: "Dropoff date (YYYY-MM-DD)"},
				"pickup_time":      {Type: "string", Description: "Pickup local time HH:MM (default 10:00)"},
				"dropoff_time":     {Type: "string", Description: "Dropoff local time HH:MM (default 10:00)"},
				"currency":         {Type: "string", Description: "Price currency (defaults to display_currency preference, then EUR)"},
				"passengers":       {Type: "integer", Description: "Traveller count for capacity recommendations (defaults to profile companions + user)"},
				"driver_age":       {Type: "integer", Description: "Driver age for age-sensitive offers (default 30)"},
				"max_price":        {Type: "number", Description: "Maximum total rental price filter"},
				"vehicle_class":    {Type: "string", Description: "Vehicle class filter, e.g. economy, compact, SUV, van"},
				"provider":         {Type: "string", Description: "Restrict to provider, currently skyscanner"},
			},
			Required: []string{"pickup_location", "pickup_date", "dropoff_date"},
		},
		OutputSchema: carSearchOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Rental Car Search",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func carSearchOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success": schemaBool(),
			"count":   schemaInt(),
			"offers": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"provider":          schemaString(),
					"supplier":          schemaString(),
					"vehicle_class":     schemaString(),
					"vehicle_name":      schemaString(),
					"transmission":      schemaString(),
					"fuel_policy":       schemaString(),
					"seats":             schemaInt(),
					"bags":              schemaInt(),
					"doors":             schemaInt(),
					"passengers":        schemaInt(),
					"pickup":            carEndpointSchema(),
					"dropoff":           carEndpointSchema(),
					"price":             schemaNum(),
					"currency":          schemaString(),
					"taxes_and_fees":    schemaNum(),
					"free_cancellation": schemaBool(),
					"unlimited_mileage": schemaBool(),
					"booking_url":       schemaString(),
					"freshness":         schemaString(),
				},
			}),
			"provider_statuses": schemaArrayDesc("Per-provider outcome. Status: 'ok'|'error'|'skipped'. Missing partner credentials are surfaced as skipped with MISSING_CREDENTIAL.", map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":            schemaString(),
					"name":          schemaString(),
					"status":        schemaString(),
					"results":       schemaInt(),
					"error":         schemaString(),
					"fix_hint":      schemaString(),
					"fix_hint_code": schemaString(),
				},
			}),
			"error": schemaString(),
		},
		"required": []string{"success", "count"},
	}
}

func carEndpointSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"location": schemaString(),
			"code":     schemaString(),
			"time":     schemaString(),
			"address":  schemaString(),
			"lat":      schemaNum(),
			"lon":      schemaNum(),
		},
	}
}

func handleSearchCars(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	prefs, prefsErr := preferences.Load()
	if prefsErr != nil {
		slog.Warn("search_cars: preferences load failed, continuing without prefs", "err", prefsErr)
	}
	currency := strings.ToUpper(argString(args, "currency"))
	if currency == "" && prefs != nil {
		currency = strings.ToUpper(prefs.DisplayCurrency)
	}

	passengers := argInt(args, "passengers", 0)
	if passengers <= 0 {
		passengers = 1
		if prefs != nil && prefs.DefaultCompanions > 0 {
			passengers = prefs.DefaultCompanions + 1
		}
	}

	opts := cars.SearchOptions{
		PickupLocation:  argString(args, "pickup_location"),
		DropoffLocation: argString(args, "dropoff_location"),
		PickupDate:      argString(args, "pickup_date"),
		DropoffDate:     argString(args, "dropoff_date"),
		PickupTime:      argString(args, "pickup_time"),
		DropoffTime:     argString(args, "dropoff_time"),
		Currency:        currency,
		Passengers:      passengers,
		DriverAge:       argInt(args, "driver_age", 0),
		MaxPrice:        argFloat(args, "max_price", 0),
		VehicleClass:    argString(args, "vehicle_class"),
	}
	if provider := argString(args, "provider"); provider != "" {
		opts.Providers = strings.Split(provider, ",")
	}

	result, err := cars.Search(ctx, opts)
	if err != nil {
		return nil, nil, err
	}

	summary := buildCarSearchSummary(result, opts)
	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return content, result, nil
}

func buildCarSearchSummary(result *models.CarSearchResult, opts cars.SearchOptions) string {
	if result == nil {
		return "No rental car search result was returned."
	}
	if !result.Success {
		if result.Error != "" {
			return fmt.Sprintf("No rental car offers found for %s from %s to %s: %s", opts.PickupLocation, opts.PickupDate, opts.DropoffDate, result.Error)
		}
		return fmt.Sprintf("No rental car offers found for %s from %s to %s.", opts.PickupLocation, opts.PickupDate, opts.DropoffDate)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d rental car offers for %s from %s to %s", result.Count, opts.PickupLocation, opts.PickupDate, opts.DropoffDate))
	if opts.DropoffLocation != "" && opts.DropoffLocation != opts.PickupLocation {
		sb.WriteString(fmt.Sprintf(", returning at %s", opts.DropoffLocation))
	}
	sb.WriteString(":\n\n")
	limit := min(len(result.Offers), 5)
	for i := 0; i < limit; i++ {
		offer := result.Offers[i]
		name := strings.TrimSpace(strings.Join([]string{offer.Supplier, offer.VehicleName}, " "))
		if name == "" {
			name = offer.VehicleClass
		}
		sb.WriteString(fmt.Sprintf("%d. %s — %s %.2f", i+1, name, offer.Currency, offer.Price))
		if offer.VehicleClass != "" {
			sb.WriteString(" · " + offer.VehicleClass)
		}
		if offer.Seats > 0 {
			sb.WriteString(fmt.Sprintf(" · %d seats", offer.Seats))
		}
		if offer.BookingURL != "" {
			sb.WriteString(" · " + offer.BookingURL)
		}
		sb.WriteString("\n")
	}
	if len(result.ProviderStatuses) > 0 {
		sb.WriteString("\nProvider status: ")
		statusParts := make([]string, 0, len(result.ProviderStatuses))
		for _, status := range result.ProviderStatuses {
			statusParts = append(statusParts, fmt.Sprintf("%s=%s", status.ID, status.Status))
		}
		sb.WriteString(strings.Join(statusParts, ", "))
	}
	return strings.TrimSpace(sb.String())
}

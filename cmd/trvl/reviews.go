package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

var (
	reviewLimit  int
	reviewSort   string
	reviewFormat string
)

var reviewsCmd = &cobra.Command{
	Use:   "reviews <hotel_id>",
	Short: "Get hotel guest reviews",
	Long:  "Fetch guest reviews for a specific hotel from Google Hotels.\nUse a hotel ID from search results (e.g., /g/11b6d4_v_4).",
	Args:  cobra.ExactArgs(1),
	RunE:  runReviews,
}

func init() {
	reviewsCmd.Flags().IntVar(&reviewLimit, "limit", 10, "Maximum number of reviews to return")
	reviewsCmd.Flags().StringVar(&reviewSort, "sort", "newest", "Sort order: newest, highest, lowest")
	reviewsCmd.Flags().StringVar(&reviewFormat, "format", "table", "Output format: table, json")
}

func runReviews(cmd *cobra.Command, args []string) error {
	hotelID := args[0]

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	result, err := hotels.GetHotelReviews(ctx, hotelID, hotels.ReviewOptions{
		Limit: reviewLimit,
		Sort:  reviewSort,
	})
	if err != nil {
		return fmt.Errorf("fetch reviews: %w", err)
	}

	if reviewFormat == "json" {
		return models.FormatJSON(os.Stdout, result)
	}

	return printReviewsTable(result)
}

func printReviewsTable(result *models.HotelReviewResult) error {
	if result.Name != "" {
		fmt.Printf("Reviews for %s\n", result.Name)
	}
	fmt.Printf("Average: %.1f/5 (%d total reviews)\n\n",
		result.Summary.AverageRating, result.Summary.TotalReviews)

	if len(result.Reviews) == 0 {
		fmt.Println("No reviews found.")
		return nil
	}

	headers := []string{"Rating", "Author", "Date", "Review"}
	var rows [][]string

	for _, r := range result.Reviews {
		stars := starRating(r.Rating)
		text := r.Text
		if len(text) > 80 {
			text = text[:77] + "..."
		}
		rows = append(rows, []string{stars, r.Author, r.Date, text})
	}

	models.FormatTable(os.Stdout, headers, rows)
	return nil
}

// starRating returns a visual star representation of a rating.
func starRating(rating float64) string {
	full := int(rating)
	half := rating-float64(full) >= 0.5
	var b strings.Builder
	for range full {
		b.WriteRune('\u2605') // filled star
	}
	if half {
		b.WriteRune('\u2606') // empty star (half indicator)
	}
	for b.Len()/3 < 5 { // unicode stars are 3 bytes each
		b.WriteRune('\u2606') // empty star
	}
	return b.String()
}

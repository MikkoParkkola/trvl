package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

func guideCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guide <location>",
		Short: "Get a Wikivoyage travel guide for a destination",
		Long: `Get a structured travel guide from Wikivoyage with sections like
Get in, See, Do, Eat, Drink, Sleep, and Stay safe.

Free, no API key required.

Examples:
  trvl guide "Barcelona"
  trvl guide "Tokyo" --format json`,
		Args: cobra.ExactArgs(1),
		RunE: runGuide,
	}

	return cmd
}

func runGuide(cmd *cobra.Command, args []string) error {
	location := args[0]
	format, _ := cmd.Flags().GetString("format")

	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	defer cancel()

	guide, err := destinations.GetWikivoyageGuide(ctx, location)
	if err != nil {
		return fmt.Errorf("travel guide: %w", err)
	}

	if format == "json" {
		return models.FormatJSON(os.Stdout, guide)
	}

	return formatGuideCard(guide)
}

func formatGuideCard(guide *models.WikivoyageGuide) error {
	fmt.Printf("\n  %s\n", guide.Location)
	fmt.Printf("  %s\n\n", guide.URL)

	if guide.Summary != "" {
		fmt.Println("  OVERVIEW")
		printWrapped(guide.Summary, 72, "    ")
		fmt.Println()
	}

	// Print sections in a consistent order.
	sectionOrder := []string{"Understand", "Get in", "Get around", "See", "Do", "Buy", "Eat", "Drink", "Sleep", "Stay safe", "Connect", "Go next"}
	printed := make(map[string]bool)

	for _, name := range sectionOrder {
		content, ok := guide.Sections[name]
		if !ok || content == "" {
			continue
		}
		fmt.Printf("  %s\n", name)
		printWrapped(content, 72, "    ")
		fmt.Println()
		printed[name] = true
	}

	// Print any remaining sections not in the standard order.
	remaining := make([]string, 0)
	for name := range guide.Sections {
		if !printed[name] && guide.Sections[name] != "" {
			remaining = append(remaining, name)
		}
	}
	sort.Strings(remaining)

	for _, name := range remaining {
		fmt.Printf("  %s\n", name)
		printWrapped(guide.Sections[name], 72, "    ")
		fmt.Println()
	}

	return nil
}

// printWrapped prints text wrapped to maxWidth with a prefix on each line.
func printWrapped(text string, maxWidth int, prefix string) {
	// Simple line-based output (Wikivoyage text is already well-formatted).
	lines := splitLines(text)
	for _, line := range lines {
		if len(line) == 0 {
			fmt.Println()
			continue
		}
		fmt.Printf("%s%s\n", prefix, line)
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

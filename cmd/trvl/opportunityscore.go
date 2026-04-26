package main

// MIK-3065: trvl opportunity-score — opportunity signal scorer.

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/opportunity"
	"github.com/spf13/cobra"
)

func opportunityScoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "opportunity-score <dest:depart:return:profile:request:deal>...",
		Short: "Rank travel opportunity candidates by weighted signal score",
		Long: `opportunity-score scores candidates using 0.4*ProfileMatch + 0.2*RequestMatch + 0.4*DealQuality.
Each candidate is a colon-separated sextuple:
  destination:depart-date:return-date:profile-score:request-score:deal-score

Signal scores are 0-100. Candidates below --min-score are dropped.
Use --file with a JSON array of Candidate objects for richer input.

Examples:
  trvl opportunity-score PRG:2026-06-15:2026-06-22:90:70:95
  trvl opportunity-score PRG:2026-06-15:2026-06-22:90:70:95 ROM:2026-07-01:2026-07-08:60:80:75 --min-score 70
  trvl opportunity-score --file candidates.json --format json`,
		Args: cobra.ArbitraryArgs,
		RunE: runOpportunityScore,
	}
	cmd.Flags().Float64("min-score", 0, "Minimum overall score to include (0 = keep all)")
	cmd.Flags().String("file", "", "Path to JSON file with candidates array")
	return cmd
}

func runOpportunityScore(cmd *cobra.Command, args []string) error {
	minScore, _ := cmd.Flags().GetFloat64("min-score")
	filePath, _ := cmd.Flags().GetString("file")
	format, _ := cmd.Flags().GetString("format")
	var candidates []opportunity.Candidate
	var err error
	if filePath != "" {
		candidates, err = loadCandidatesFromFile(filePath)
	} else {
		if len(args) == 0 {
			return fmt.Errorf("opportunity-score: provide candidate args or --file path")
		}
		candidates, err = parseCandidateArgs(args)
	}
	if err != nil {
		return fmt.Errorf("opportunity-score: %w", err)
	}
	ranked := opportunity.FilterAndRank(candidates, opportunity.Weights{}, minScore)
	if format == "json" {
		return json.NewEncoder(os.Stdout).Encode(ranked)
	}
	renderOpportunityTable(os.Stdout, ranked)
	return nil
}

func loadCandidatesFromFile(path string) ([]opportunity.Candidate, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open file: %w", err)
	}
	defer f.Close()
	var c []opportunity.Candidate
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return c, nil
}

func parseCandidateArgs(args []string) ([]opportunity.Candidate, error) {
	out := make([]opportunity.Candidate, 0, len(args))
	for _, arg := range args {
		parts := strings.SplitN(arg, ":", 6)
		if len(parts) != 6 {
			return nil, fmt.Errorf("invalid candidate %q: expected dest:depart:return:profile:request:deal (6 fields)", arg)
		}
		profile, err := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid candidate %q: profile score must be a number", arg)
		}
		request, err := strconv.ParseFloat(strings.TrimSpace(parts[4]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid candidate %q: request score must be a number", arg)
		}
		deal, err := strconv.ParseFloat(strings.TrimSpace(parts[5]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid candidate %q: deal score must be a number", arg)
		}
		out = append(out, opportunity.Candidate{Destination: strings.TrimSpace(parts[0]), DepartDate: strings.TrimSpace(parts[1]), ReturnDate: strings.TrimSpace(parts[2]), Signals: opportunity.Signals{ProfileMatch: profile, RequestMatch: request, DealQuality: deal}})
	}
	return out, nil
}

func renderOpportunityTable(out *os.File, candidates []opportunity.Candidate) {
	if len(candidates) == 0 {
		fmt.Fprintln(out, "No candidates above minimum score threshold.")
		return
	}
	fmt.Fprintf(out, "%-4s  %-8s  %-12s  %-12s  %7s  %7s  %7s  %7s  %s\n", "Rank", "Dest", "Depart", "Return", "Profile", "Request", "Deal", "Overall", "Verdict")
	fmt.Fprintf(out, "%s\n", strings.Repeat("-", 88))
	for i, c := range candidates {
		fmt.Fprintf(out, "#%-3d  %-8s  %-12s  %-12s  %7.1f  %7.1f  %7.1f  %7.1f  %s\n", i+1, c.Destination, c.DepartDate, c.ReturnDate, c.Signals.ProfileMatch, c.Signals.RequestMatch, c.Signals.DealQuality, c.Overall, c.Reason)
	}
}

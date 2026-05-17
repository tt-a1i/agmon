package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

type compareToolDiff struct {
	Name   string `json:"name"`
	ACount int    `json:"a_count"`
	BCount int    `json:"b_count"`
}

type compareCostDiff struct {
	A     float64 `json:"a"`
	B     float64 `json:"b"`
	Delta float64 `json:"delta"`
}

type compareTokenDiff struct {
	AInput      int `json:"a_input"`
	BInput      int `json:"b_input"`
	DeltaInput  int `json:"delta_input"`
	AOutput     int `json:"a_output"`
	BOutput     int `json:"b_output"`
	DeltaOutput int `json:"delta_output"`
}

type compareFileDiff struct {
	AOnly  []string `json:"a_only"`
	BOnly  []string `json:"b_only"`
	Common []string `json:"common"`
}

type compareResult struct {
	ToolDiff  []compareToolDiff `json:"tool_diff"`
	CostDiff  compareCostDiff   `json:"cost_diff"`
	TokenDiff compareTokenDiff  `json:"token_diff"`
	FileDiff  compareFileDiff   `json:"file_diff"`
}

func runCompare() error {
	args := os.Args[2:]
	if maybePrintCmdHelp("compare", args) {
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("usage: tokenmeter compare <sessionA> <sessionB> [--format text|json]")
	}
	aPrefix := args[0]
	bPrefix := args[1]
	format := "text"
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--format":
			if i+1 >= len(args) {
				return fmt.Errorf("--format requires a value")
			}
			format = args[i+1]
			i++
		default:
			return fmt.Errorf("unknown compare argument: %s", args[i])
		}
	}
	if format != "text" && format != "json" {
		return fmt.Errorf("unsupported compare format %q (use text or json)", format)
	}

	db := mustOpenDB()
	defer db.Close()

	a, err := lookupCompareSession(db, "A", aPrefix)
	if err != nil {
		return err
	}
	b, err := lookupCompareSession(db, "B", bPrefix)
	if err != nil {
		return err
	}
	result, err := buildCompareResult(db, a, b)
	if err != nil {
		return err
	}

	if format == "json" {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	writeCompareText(os.Stdout, a, b, result)
	return nil
}

func lookupCompareSession(db *storage.DB, label, prefix string) (storage.SessionRow, error) {
	s, found, err := db.GetSessionByIDPrefix(prefix)
	if err != nil {
		if errors.Is(err, storage.ErrAmbiguousSessionPrefix) {
			return storage.SessionRow{}, err
		}
		return storage.SessionRow{}, fmt.Errorf("lookup session %s: %w", label, err)
	}
	if !found {
		return storage.SessionRow{}, fmt.Errorf("session %s not found: %s", label, prefix)
	}
	return s, nil
}

func buildCompareResult(db *storage.DB, a, b storage.SessionRow) (compareResult, error) {
	toolDiff, err := compareTools(db, a.SessionID, b.SessionID)
	if err != nil {
		return compareResult{}, err
	}
	fileDiff, err := compareFiles(db, a.SessionID, b.SessionID)
	if err != nil {
		return compareResult{}, err
	}
	return compareResult{
		ToolDiff: toolDiff,
		CostDiff: compareCostDiff{
			A:     a.TotalCostUSD,
			B:     b.TotalCostUSD,
			Delta: b.TotalCostUSD - a.TotalCostUSD,
		},
		TokenDiff: compareTokenDiff{
			AInput:      a.TotalInputTokens,
			BInput:      b.TotalInputTokens,
			DeltaInput:  b.TotalInputTokens - a.TotalInputTokens,
			AOutput:     a.TotalOutputTokens,
			BOutput:     b.TotalOutputTokens,
			DeltaOutput: b.TotalOutputTokens - a.TotalOutputTokens,
		},
		FileDiff: fileDiff,
	}, nil
}

func compareTools(db *storage.DB, aID, bID string) ([]compareToolDiff, error) {
	aStats, err := db.ListToolStats(aID)
	if err != nil {
		return nil, err
	}
	bStats, err := db.ListToolStats(bID)
	if err != nil {
		return nil, err
	}

	counts := make(map[string]*compareToolDiff)
	for _, stat := range aStats {
		diff := counts[stat.ToolName]
		if diff == nil {
			diff = &compareToolDiff{Name: stat.ToolName}
			counts[stat.ToolName] = diff
		}
		diff.ACount = stat.Count
	}
	for _, stat := range bStats {
		diff := counts[stat.ToolName]
		if diff == nil {
			diff = &compareToolDiff{Name: stat.ToolName}
			counts[stat.ToolName] = diff
		}
		diff.BCount = stat.Count
	}

	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]compareToolDiff, 0, len(names))
	for _, name := range names {
		result = append(result, *counts[name])
	}
	return result, nil
}

func compareFiles(db *storage.DB, aID, bID string) (compareFileDiff, error) {
	aFiles, err := db.ListFileChanges(aID)
	if err != nil {
		return compareFileDiff{}, err
	}
	bFiles, err := db.ListFileChanges(bID)
	if err != nil {
		return compareFileDiff{}, err
	}

	aSet := make(map[string]struct{})
	bSet := make(map[string]struct{})
	for _, f := range aFiles {
		aSet[f.FilePath] = struct{}{}
	}
	for _, f := range bFiles {
		bSet[f.FilePath] = struct{}{}
	}

	var diff compareFileDiff
	for path := range aSet {
		if _, ok := bSet[path]; ok {
			diff.Common = append(diff.Common, path)
		} else {
			diff.AOnly = append(diff.AOnly, path)
		}
	}
	for path := range bSet {
		if _, ok := aSet[path]; !ok {
			diff.BOnly = append(diff.BOnly, path)
		}
	}
	sort.Strings(diff.AOnly)
	sort.Strings(diff.BOnly)
	sort.Strings(diff.Common)
	return diff, nil
}

func writeCompareText(w interface{ Write([]byte) (int, error) }, a, b storage.SessionRow, result compareResult) {
	aToolCount, bToolCount := toolTotals(result.ToolDiff)
	aFileCount := len(result.FileDiff.AOnly) + len(result.FileDiff.Common)
	bFileCount := len(result.FileDiff.BOnly) + len(result.FileDiff.Common)

	fmt.Fprintf(w, "Session A: %s/%s · $%.2f (%d tools, %d files)\n",
		a.Platform, compareSessionName(a), a.TotalCostUSD, aToolCount, aFileCount)
	fmt.Fprintf(w, "Session B: %s/%s · $%.2f (%d tools, %d files)\n\n",
		b.Platform, compareSessionName(b), b.TotalCostUSD, bToolCount, bFileCount)

	costPercent := "n/a"
	if result.CostDiff.A != 0 {
		costPercent = fmt.Sprintf("%+.1f%%", result.CostDiff.Delta/result.CostDiff.A*100)
	}
	fmt.Fprintf(w, "Cost delta:    %s (%s)\n", signedUSD(result.CostDiff.Delta), costPercent)
	fmt.Fprintf(w, "Tool delta:    %+d calls\n", bToolCount-aToolCount)
	fmt.Fprintf(w, "File delta:    %+d files\n\n", bFileCount-aFileCount)

	fmt.Fprintln(w, "Top tools diff:")
	if len(result.ToolDiff) == 0 {
		fmt.Fprintln(w, "- none")
		return
	}
	topTools := append([]compareToolDiff(nil), result.ToolDiff...)
	sort.SliceStable(topTools, func(i, j int) bool {
		left := absInt(topTools[i].BCount - topTools[i].ACount)
		right := absInt(topTools[j].BCount - topTools[j].ACount)
		if left == right {
			return topTools[i].Name < topTools[j].Name
		}
		return left > right
	})
	for i, diff := range topTools {
		if i >= 10 {
			break
		}
		fmt.Fprintf(w, "- %s:  A=%d B=%d (%+d)\n", diff.Name, diff.ACount, diff.BCount, diff.BCount-diff.ACount)
	}
}

func compareSessionName(s storage.SessionRow) string {
	if s.GitBranch != "" {
		return s.GitBranch
	}
	if s.CWD != "" {
		base := filepath.Base(s.CWD)
		if base != "." && base != string(filepath.Separator) {
			return base
		}
		return s.CWD
	}
	return shortSessionID(s.SessionID)
}

func signedUSD(v float64) string {
	if v >= 0 {
		return fmt.Sprintf("+$%.2f", v)
	}
	return fmt.Sprintf("-$%.2f", -v)
}

func toolTotals(diff []compareToolDiff) (int, int) {
	var a, b int
	for _, d := range diff {
		a += d.ACount
		b += d.BCount
	}
	return a, b
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

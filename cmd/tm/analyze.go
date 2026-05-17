package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func runAnalyze() error {
	if maybePrintCmdHelp("analyze", os.Args[2:]) {
		return nil
	}
	opts, err := parseAnalyzeArgs(os.Args[2:])
	if err != nil {
		return err
	}

	db := mustOpenDB()
	defer db.Close()

	from, to, label, err := analyzeRange(db, opts.rangeName)
	if err != nil {
		return err
	}
	result, err := db.Analyze(from, to)
	if err != nil {
		return err
	}
	result.Range = label

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	renderAnalyzeText(os.Stdout, result)
	return nil
}

type analyzeOptions struct {
	rangeName  string
	jsonOutput bool
}

func parseAnalyzeArgs(args []string) (analyzeOptions, error) {
	opts := analyzeOptions{rangeName: "month"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--range":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--range requires a value")
			}
			opts.rangeName = args[i+1]
			i++
		case "--json":
			opts.jsonOutput = true
		default:
			return opts, fmt.Errorf("unknown analyze argument: %s", args[i])
		}
	}
	switch opts.rangeName {
	case "week", "month", "all":
		return opts, nil
	default:
		return opts, fmt.Errorf("unknown analyze range %q (use week, month, all)", opts.rangeName)
	}
}

func analyzeRange(db *storage.DB, name string) (time.Time, time.Time, string, error) {
	now := time.Now()
	switch name {
	case "week":
		return now.AddDate(0, 0, -7), now, "Last 7 days", nil
	case "month":
		return now.AddDate(0, 0, -30), now, "Last 30 days", nil
	case "all":
		first, err := db.GetFirstTokenDate()
		if err != nil {
			return time.Time{}, time.Time{}, "", err
		}
		if first.IsZero() {
			first = now.AddDate(0, 0, -30)
		}
		return first, now, "All time", nil
	default:
		return time.Time{}, time.Time{}, "", fmt.Errorf("unknown analyze range %q", name)
	}
}

func renderAnalyzeText(out *os.File, result *storage.AnalysisResult) {
	fmt.Fprintf(out, "TokenMeter Analysis — %s\n\n", result.Range)
	fmt.Fprintln(out, "Cost")
	fmt.Fprintf(out, "  Total:           $%.2f\n", result.Cost.Total)
	fmt.Fprintf(out, "  Average / day:   $%.2f\n", result.Cost.AveragePerDay)
	if result.Cost.HighestDay != "" {
		fmt.Fprintf(out, "  Highest day:     $%.2f (%s)\n", result.Cost.HighestDayCost, result.Cost.HighestDay)
	} else {
		fmt.Fprintln(out, "  Highest day:     $0.00")
	}
	fmt.Fprintf(out, "  Active days:     %d of %d\n\n", result.Cost.ActiveDays, result.Cost.Days)

	fmt.Fprintln(out, "Sessions")
	fmt.Fprintf(out, "  Total:           %d (avg %.1f/day)\n", result.Sessions.Total, result.Sessions.AveragePerDay)
	fmt.Fprintf(out, "  Active:          %d\n", result.Sessions.Active)
	fmt.Fprintf(out, "  By platform:     %s\n", renderPlatformCounts(result.Sessions.ByPlatform, result.Sessions.Total))
	fmt.Fprintf(out, "  Avg cost/sess:   $%.2f\n", result.Sessions.AverageCost)
	if result.Sessions.MostExpensive != nil {
		top := result.Sessions.MostExpensive
		fmt.Fprintf(out, "  Most expensive:  $%.2f  %s (%s)\n\n", top.CostUSD, top.Name, top.Platform)
	} else {
		fmt.Fprintln(out, "  Most expensive:  none")
		fmt.Fprintln(out)
	}

	fmt.Fprintln(out, "Models")
	if len(result.Models) == 0 {
		fmt.Fprintln(out, "  none")
	} else {
		for _, model := range result.Models {
			fmt.Fprintf(out, "  %-20s %5.1f%% of cost\n", model.Model+":", model.Percent)
		}
	}
	fmt.Fprintln(out)

	fmt.Fprintln(out, "Tools (top 5)")
	if len(result.Tools) == 0 {
		fmt.Fprintln(out, "  none")
	} else {
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for i, tool := range result.Tools {
			if i >= 5 {
				break
			}
			fail := ""
			if tool.FailCount > 0 {
				fail = fmt.Sprintf(", %.1f%% fail", tool.FailPercent)
			}
			fmt.Fprintf(tw, "  %s\t%d calls\t(avg %s%s)\n", tool.Name, tool.Count, formatAnalyzeDuration(tool.AvgMs), fail)
		}
		_ = tw.Flush()
	}
	fmt.Fprintln(out)

	fmt.Fprintln(out, "Files touched")
	totalFiles := 0
	for _, count := range result.FilesByExt {
		totalFiles += count
	}
	fmt.Fprintf(out, "  Total unique:    %d\n", totalFiles)
	fmt.Fprintf(out, "  By extension:    %s\n", renderExtensionCounts(result.FilesByExt))
	if len(result.TopFiles) > 0 {
		fmt.Fprintf(out, "  Most edited:     %s (%d changes)\n", result.TopFiles[0].Path, result.TopFiles[0].Count)
	} else {
		fmt.Fprintln(out, "  Most edited:     none")
	}
	fmt.Fprintln(out)

	fmt.Fprintln(out, "Activity heatmap (UTC+8)")
	renderHeatmap(out, result.Heatmap)
}

func renderPlatformCounts(counts map[string]int, total int) string {
	if len(counts) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		percent := 0.0
		if total > 0 {
			percent = float64(counts[key]) / float64(total) * 100
		}
		parts = append(parts, fmt.Sprintf("%s %d (%.0f%%)", titlePlatform(key), counts[key], percent))
	}
	return strings.Join(parts, " · ")
}

func renderExtensionCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	sorted := storageSortedExtensionCounts(counts)
	parts := make([]string, 0, len(sorted))
	other := 0
	for i, item := range sorted {
		if i < 5 {
			parts = append(parts, fmt.Sprintf("%s %d", item.Path, item.Count))
			continue
		}
		other += item.Count
	}
	if other > 0 {
		parts = append(parts, fmt.Sprintf("other %d", other))
	}
	return strings.Join(parts, " · ")
}

func storageSortedExtensionCounts(counts map[string]int) []storage.FileEditCount {
	result := make([]storage.FileEditCount, 0, len(counts))
	for ext, count := range counts {
		result = append(result, storage.FileEditCount{Path: ext, Count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count == result[j].Count {
			return result[i].Path < result[j].Path
		}
		return result[i].Count > result[j].Count
	})
	return result
}

func renderHeatmap(out *os.File, heatmap [7][24]int) {
	labels := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	maxCount := 0
	for _, day := range heatmap {
		for _, count := range day {
			if count > maxCount {
				maxCount = count
			}
		}
	}
	for i, label := range labels {
		fmt.Fprintf(out, "  %s %s\n", label, heatmapLine(heatmap[i], maxCount))
	}
}

func heatmapLine(hours [24]int, maxCount int) string {
	if maxCount == 0 {
		return strings.Repeat("░", 24)
	}
	var b strings.Builder
	for _, count := range hours {
		switch {
		case count == 0:
			b.WriteRune('░')
		case count*4 <= maxCount:
			b.WriteRune('▒')
		case count*2 <= maxCount:
			b.WriteRune('▓')
		default:
			b.WriteRune('█')
		}
	}
	return b.String()
}

func titlePlatform(platform string) string {
	switch strings.ToLower(platform) {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex"
	case "":
		return "Unknown"
	default:
		return strings.ToUpper(platform[:1]) + platform[1:]
	}
}

func formatAnalyzeDuration(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	if ms >= 1000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	return fmt.Sprintf("%dms", ms)
}

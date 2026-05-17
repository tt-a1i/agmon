package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

const topClearScreen = "\033[2J\033[H"

type topOptions struct {
	once     bool
	interval time.Duration
	noClear  bool
}

func runTop() error {
	if maybePrintCmdHelp("top", os.Args[2:]) {
		return nil
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	defer close(done)
	go func() {
		select {
		case <-sigCh:
			close(stop)
		case <-done:
		}
	}()
	return runTopWithDeps(os.Args[2:], os.Stdout, stop)
}

func runTopWithDeps(args []string, out io.Writer, stop <-chan struct{}) error {
	opts, err := parseTopArgs(args)
	if err != nil {
		return err
	}
	db, err := storage.Open(storage.DefaultDBPath())
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	if opts.once {
		return renderTopFrame(out, db, opts, time.Now())
	}
	if stop == nil {
		stop = make(chan struct{})
	}

	ticker := time.NewTicker(opts.interval)
	defer ticker.Stop()
	for {
		if err := renderTopFrame(out, db, opts, time.Now()); err != nil {
			return err
		}
		select {
		case <-stop:
			return nil
		case <-ticker.C:
		}
	}
}

func parseTopArgs(args []string) (topOptions, error) {
	opts := topOptions{interval: 2 * time.Second}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--once":
			opts.once = true
		case "--no-clear":
			opts.noClear = true
		case "--interval":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--interval requires a value")
			}
			interval, err := parseTopInterval(args[i+1])
			if err != nil {
				return opts, err
			}
			opts.interval = interval
			i++
		default:
			return opts, fmt.Errorf("unknown top argument: %s", args[i])
		}
	}
	return opts, nil
}

func parseTopInterval(raw string) (time.Duration, error) {
	if d, err := time.ParseDuration(raw); err == nil {
		if d <= 0 {
			return 0, fmt.Errorf("--interval must be positive")
		}
		return d, nil
	}
	seconds, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid --interval %q", raw)
	}
	if seconds <= 0 {
		return 0, fmt.Errorf("--interval must be positive")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func renderTopFrame(out io.Writer, db *storage.DB, opts topOptions, now time.Time) error {
	if opts.noClear {
		fmt.Fprintf(out, "=== %s ===\n", now.Format("15:04:05"))
	} else if !opts.once {
		fmt.Fprint(out, topClearScreen)
	}
	return renderTopSnapshot(out, db, opts, now)
}

func renderTopSnapshot(out io.Writer, db *storage.DB, opts topOptions, now time.Time) error {
	todayCost, err := db.GetTodayCost()
	if err != nil {
		return err
	}
	inTokens, outTokens, err := db.GetTodayTokens()
	if err != nil {
		return err
	}
	activeSessions, err := db.GetActiveSessionCount()
	if err != nil {
		return err
	}
	totalSessions, err := db.GetVisibleSessionCount()
	if err != nil {
		return err
	}
	projection, err := db.GetMonthCostProjection(now)
	if err != nil {
		return err
	}

	todayStart := time.Date(now.In(time.Local).Year(), now.In(time.Local).Month(), now.In(time.Local).Day(), 0, 0, 0, 0, time.Local)
	topSessions, err := db.GetTopSessionsByCost(now.Add(-24*time.Hour), now, 5)
	if err != nil {
		return err
	}
	toolStats, err := db.AllToolStats(todayStart, now)
	if err != nil {
		return err
	}
	models, err := db.GetModelCostBreakdown(todayStart, now)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "TokenMeter @ %s\n\n", now.Format("15:04:05 MST"))
	fmt.Fprintf(out, "Today      %s  (%s tokens, %d sessions, %d active)\n",
		colorTopCost(fmt.Sprintf("$%.2f", todayCost), todayCost),
		formatTopTokens(inTokens+outTokens), totalSessions, activeSessions)
	fmt.Fprintf(out, "This month $%.2f / Projected $%.2f (%s confidence)\n\n",
		projection.UsedSoFar, projection.ProjectedTotal, projection.Confidence)

	if todayCost == 0 && inTokens+outTokens == 0 && len(topSessions) == 0 && len(toolStats) == 0 && len(models) == 0 {
		fmt.Fprintln(out, "no data yet")
		fmt.Fprintln(out)
	}

	fmt.Fprintln(out, "Top sessions by cost (24h):")
	if len(topSessions) == 0 {
		fmt.Fprintln(out, "  no data")
	} else {
		if err := renderTopSessions(out, db, topSessions, now); err != nil {
			return err
		}
	}
	fmt.Fprintln(out)

	fmt.Fprintln(out, "Top tools today:")
	if len(toolStats) == 0 {
		fmt.Fprintln(out, "  no data")
	} else {
		renderTopTools(out, toolStats)
	}
	fmt.Fprintln(out)

	fmt.Fprintln(out, "Models:")
	if len(models) == 0 {
		fmt.Fprintln(out, "  no data")
	} else {
		renderTopModels(out, models)
	}
	fmt.Fprintln(out)

	if !opts.once {
		fmt.Fprintf(out, "Ctrl+C to quit · interval %s\n", formatTopInterval(opts.interval))
	}
	return nil
}

func renderTopSessions(out io.Writer, db *storage.DB, sessions []storage.TopSessionRow, now time.Time) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for i, session := range sessions {
		toolCount, err := topSessionToolCount(db, session.SessionID)
		if err != nil {
			return err
		}
		age := ""
		if row, found, err := db.GetSessionByIDPrefix(session.SessionID); err == nil && found {
			ref := row.StartTime
			if row.EndTime != nil {
				ref = *row.EndTime
			}
			age = formatTopAge(now.Sub(ref))
		}
		fmt.Fprintf(tw, "  %d.\t%s\t%s\t$%.2f\t%d tools\t%s\n",
			i+1, session.Platform, topSessionName(session), session.CostUSD, toolCount, age)
	}
	return tw.Flush()
}

func topSessionToolCount(db *storage.DB, sessionID string) (int, error) {
	stats, err := db.ListToolStats(sessionID)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, stat := range stats {
		total += stat.Count
	}
	return total, nil
}

func topSessionName(session storage.TopSessionRow) string {
	base := filepath.Base(session.CWD)
	if base == "." || base == string(filepath.Separator) {
		base = ""
	}
	switch {
	case base != "" && session.GitBranch != "":
		return base + "/" + session.GitBranch
	case session.GitBranch != "":
		return session.GitBranch
	case base != "":
		return base
	default:
		if len(session.SessionID) > 8 {
			return session.SessionID[:8]
		}
		return session.SessionID
	}
}

func renderTopTools(out io.Writer, tools []storage.ToolStatRow) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for i, tool := range tools {
		if i >= 5 {
			break
		}
		fail := ""
		if tool.FailCount > 0 && tool.Count > 0 {
			fail = fmt.Sprintf(", %.1f%% fail", float64(tool.FailCount)/float64(tool.Count)*100)
		}
		fmt.Fprintf(tw, "  %s\t%d calls\t(%s avg%s)\n",
			tool.ToolName, tool.Count, formatAnalyzeDuration(tool.AvgMs), fail)
	}
	_ = tw.Flush()
}

func renderTopModels(out io.Writer, models []storage.ModelCostRow) {
	totalCost := 0.0
	for _, model := range models {
		totalCost += model.CostUSD
	}
	if totalCost <= 0 {
		fmt.Fprintln(out, "  no data")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for i, model := range models {
		if i >= 5 {
			break
		}
		percent := model.CostUSD / totalCost * 100
		fmt.Fprintf(tw, "  %s\t%.0f%%\n", model.Model, percent)
	}
	_ = tw.Flush()
}

func colorTopCost(s string, cost float64) string {
	switch {
	case cost < 1:
		return "\033[32m" + s + "\033[0m"
	case cost <= 10:
		return "\033[33m" + s + "\033[0m"
	default:
		return "\033[31m" + s + "\033[0m"
	}
}

func formatTopTokens(n int) string {
	if n < 1000 {
		return strconv.Itoa(n)
	}
	parts := []string{}
	for n >= 1000 {
		parts = append([]string{fmt.Sprintf("%03d", n%1000)}, parts...)
		n /= 1000
	}
	parts = append([]string{strconv.Itoa(n)}, parts...)
	return strings.Join(parts, "")
}

func formatTopAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func formatTopInterval(d time.Duration) string {
	if d%time.Second == 0 {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	return d.String()
}

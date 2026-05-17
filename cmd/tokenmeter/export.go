package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func runExport() error {
	if maybePrintCmdHelp("export", os.Args[2:]) {
		return nil
	}
	opts, err := parseExportArgs(os.Args[2:])
	if err != nil {
		return err
	}

	db := mustOpenDB()
	defer db.Close()

	from, to, err := exportTimeRange(db, opts.rangeName)
	if err != nil {
		return err
	}

	var w io.Writer = os.Stdout
	var f *os.File
	if opts.outPath != "" {
		f, err = os.Create(opts.outPath)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer f.Close()
		w = f
	}

	switch opts.format {
	case "csv":
		return writeSessionExportCSV(w, db, from, to)
	case "json":
		return writeSessionExportJSON(w, db, from, to)
	default:
		return fmt.Errorf("unsupported export format %q (use csv or json)", opts.format)
	}
}

type exportOptions struct {
	rangeName string
	format    string
	outPath   string
}

func parseExportArgs(args []string) (exportOptions, error) {
	opts := exportOptions{rangeName: "week", format: "csv"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--range":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--range requires a value")
			}
			opts.rangeName = args[i+1]
			i++
		case "--format":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--format requires a value")
			}
			opts.format = args[i+1]
			i++
		case "--out":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--out requires a value")
			}
			opts.outPath = args[i+1]
			i++
		default:
			return opts, fmt.Errorf("unknown export argument: %s", args[i])
		}
	}
	return opts, nil
}

func exportTimeRange(db *storage.DB, rangeName string) (time.Time, time.Time, error) {
	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	switch rangeName {
	case "today":
		return startOfToday, now, nil
	case "week":
		wd := now.Weekday()
		if wd == 0 {
			wd = 7
		}
		return time.Date(now.Year(), now.Month(), now.Day()-int(wd-1), 0, 0, 0, 0, time.Local), now, nil
	case "month":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local), now, nil
	case "all":
		firstDate, err := db.GetFirstTokenDate()
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		if firstDate.IsZero() {
			firstDate = time.Unix(0, 0).In(time.Local)
		}
		return firstDate, now, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unknown export range %q (use today, week, month, all)", rangeName)
	}
}

func writeSessionExportCSV(w io.Writer, db *storage.DB, from, to time.Time) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"date", "session_id", "session_name", "platform", "model", "input_tokens", "output_tokens", "cache_tokens", "cost_usd"}); err != nil {
		return err
	}
	if err := db.ForEachSessionExportRow(from, to, func(row storage.SessionExportRow) error {
		return cw.Write([]string{
			row.Date,
			row.SessionID,
			row.SessionName,
			row.Platform,
			row.Model,
			strconv.Itoa(row.InputTokens),
			strconv.Itoa(row.OutputTokens),
			strconv.Itoa(row.CacheTokens),
			fmt.Sprintf("%.6f", row.CostUSD),
		})
	}); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

func writeSessionExportJSON(w io.Writer, db *storage.DB, from, to time.Time) error {
	rows := make([]storage.SessionExportRow, 0)
	if err := db.ForEachSessionExportRow(from, to, func(row storage.SessionExportRow) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(rows)
}

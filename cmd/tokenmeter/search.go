package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func runSearch() error {
	query, limit, err := parseSearchArgs(os.Args[2:])
	if err != nil {
		return err
	}

	db := mustOpenDB()
	defer db.Close()

	hits, err := db.SearchHits(query, limit)
	if err != nil {
		return err
	}

	fmt.Printf("Found %d matches:\n\n", len(hits))
	for _, hit := range hits {
		fmt.Printf("[%s] %s · %s\n", hit.Kind, hit.SessionName, hit.Timestamp.Format("2006-01-02 15:04"))
		detail := plainSearchExcerpt(hit.Excerpt)
		if hit.Kind == "tool_param" {
			if toolName := searchHitToolName(db, hit, query, true); toolName != "" {
				detail = toolName + " " + detail
			}
		} else if hit.Kind == "tool_result" && !strings.HasPrefix(detail, "output:") {
			detail = "output: " + detail
		}
		fmt.Printf("  %s\n\n", detail)
	}
	return nil
}

func plainSearchExcerpt(s string) string {
	s = strings.ReplaceAll(s, "<mark>", "")
	return strings.ReplaceAll(s, "</mark>", "")
}

func parseSearchArgs(args []string) (string, int, error) {
	limit := 20
	var queryParts []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit":
			if i+1 >= len(args) {
				return "", 0, fmt.Errorf("--limit requires a value")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return "", 0, fmt.Errorf("invalid limit: %s", args[i+1])
			}
			limit = n
			i++
		default:
			if strings.HasPrefix(args[i], "--") {
				return "", 0, fmt.Errorf("unknown search argument: %s", args[i])
			}
			queryParts = append(queryParts, args[i])
		}
	}
	query := strings.TrimSpace(strings.Join(queryParts, " "))
	if query == "" {
		return "", 0, fmt.Errorf("usage: tokenmeter search <query> [--limit N]")
	}
	return query, limit, nil
}

func searchHitToolName(db *storage.DB, hit storage.SearchHit, query string, params bool) string {
	calls, err := db.ListToolCalls(hit.SessionID, 200)
	if err != nil {
		return ""
	}
	query = strings.ToLower(query)
	for _, call := range calls {
		if params && strings.Contains(strings.ToLower(call.ParamsSummary), query) {
			return call.ToolName
		}
		if !params && strings.Contains(strings.ToLower(call.ResultSummary), query) {
			return call.ToolName
		}
	}
	return ""
}

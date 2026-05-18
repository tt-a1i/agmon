package web

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

// Insight is one auto-generated card describing usage in the requested range.
// Each insight is self-describing (Title/Body) plus a machine-readable Value
// and Metadata so future UIs can render or filter without re-deriving.
type Insight struct {
	ID       string                 `json:"id"`
	Kind     string                 `json:"kind"`
	Title    string                 `json:"title"`
	Body     string                 `json:"body"`
	Value    float64                `json:"value"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type insightsResponse struct {
	Range       string    `json:"range"`
	GeneratedAt string    `json:"generated_at"`
	Insights    []Insight `json:"insights"`
}

// toolSavedMinutes is the rough human-time saved per call by tool kind.
// Pure heuristic for the top_tool insight — see TASK.md, "拍脑袋系数".
var toolSavedMinutes = map[string]float64{
	"Edit":      3.0,
	"Write":     3.0,
	"MultiEdit": 4.0,
	"Read":      1.0,
	"Bash":      2.0,
	"Grep":      1.5,
	"Glob":      0.5,
	"Task":      5.0,
	"WebFetch":  2.0,
}

const defaultToolSavedMinutes = 1.5

func savedHoursForTool(tool string, count int) float64 {
	minutes, ok := toolSavedMinutes[tool]
	if !ok {
		minutes = defaultToolSavedMinutes
	}
	return minutes * float64(count) / 60.0
}

// insightsRange parses the ?range= param and returns the current window and the
// previous comparable window. For "all" the previous window is zero-valued and
// model_mix_shift is skipped.
func (s *Server) insightsRange(rangeParam string) (label string, from, to, prevFrom, prevTo time.Time, ok bool) {
	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	switch rangeParam {
	case "", "week":
		label = "week"
		wd := now.Weekday()
		if wd == 0 {
			wd = 7
		}
		from = time.Date(now.Year(), now.Month(), now.Day()-int(wd-1), 0, 0, 0, 0, time.Local)
	case "month":
		label = "month"
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	case "all":
		label = "all"
		firstDate, err := s.db.GetFirstTokenDate()
		if err == nil && !firstDate.IsZero() {
			from = firstDate
		} else {
			from = startOfToday.AddDate(0, 0, -29)
		}
	default:
		return "", time.Time{}, time.Time{}, time.Time{}, time.Time{}, false
	}

	to = now
	if label != "all" {
		dur := to.Sub(from)
		prevTo = from
		prevFrom = from.Add(-dur)
	}
	return label, from, to, prevFrom, prevTo, true
}

func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	rangeParam := strings.TrimSpace(r.URL.Query().Get("range"))
	label, from, to, prevFrom, prevTo, ok := s.insightsRange(rangeParam)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid range")
		return
	}

	// Analyze() gives us peak-day, top-tool, current model mix, and the
	// activity heatmap in one method call. Anything that needs comparison
	// against the previous window or per-session anomaly scoring goes through
	// dedicated follow-up queries below.
	analysis, err := s.db.Analyze(from, to)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	// Always return an empty array (not null) so the client side can iterate
	// without a nil check.
	insights := make([]Insight, 0, 5)

	if ins, emit := buildPeakDayInsight(analysis); emit {
		insights = append(insights, ins)
	}
	if ins, emit := buildTopToolInsight(analysis); emit {
		insights = append(insights, ins)
	}

	// model_mix_shift needs a previous period to compare against. Skip when
	// range=all (no meaningful "previous").
	if label != "all" {
		prevModels, err := s.db.GetModelCostBreakdown(prevFrom, prevTo)
		if err == nil {
			if ins, emit := buildModelMixShiftInsight(analysis, prevModels); emit {
				insights = append(insights, ins)
			}
		}
	}

	// cost_anomaly: pull up to 500 sessions in range and reuse the existing
	// z-score detector. Sessions list is unfiltered (no workspace/tag).
	if topSessions, err := s.db.GetTopSessionsByCost(from, to, 500); err == nil {
		if ins, emit := buildCostAnomalyInsight(topSessions); emit {
			insights = append(insights, ins)
		}
	}

	if ins, emit := buildRhythmInsight(analysis); emit {
		insights = append(insights, ins)
	}

	writeJSON(w, insightsResponse{
		Range:       label,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Insights:    insights,
	})
}

// buildPeakDayInsight emits when the highest-cost day in the range is at least
// 1.5× the period's average daily cost. Single-day ranges or empty data are
// skipped because the comparison has no meaning.
func buildPeakDayInsight(a *storage.AnalysisResult) (Insight, bool) {
	if a == nil || a.Cost.ActiveDays < 2 || a.Cost.HighestDayCost <= 0 {
		return Insight{}, false
	}
	avg := a.Cost.AveragePerDay
	if avg <= 0 {
		return Insight{}, false
	}
	ratio := a.Cost.HighestDayCost / avg
	if ratio < 1.5 {
		return Insight{}, false
	}

	day, dayName := a.Cost.HighestDay, weekdayNameFromISO(a.Cost.HighestDay)
	title := "Peak day was " + dayName
	body := fmt.Sprintf("Spent $%.2f on %s — %.1f× your %s average",
		a.Cost.HighestDayCost, day, ratio, a.Range)
	return Insight{
		ID:    "peak_day",
		Kind:  "peak_day",
		Title: title,
		Body:  body,
		Value: a.Cost.HighestDayCost,
		Metadata: map[string]interface{}{
			"date":  day,
			"ratio": roundTo(ratio, 2),
		},
	}, true
}

// buildTopToolInsight picks the most-called tool, computes its share of total
// calls, and estimates wall-clock time saved using the toolSavedMinutes table.
func buildTopToolInsight(a *storage.AnalysisResult) (Insight, bool) {
	if a == nil || len(a.Tools) == 0 {
		return Insight{}, false
	}
	top := a.Tools[0]
	if top.Count <= 0 {
		return Insight{}, false
	}
	var total int
	for _, t := range a.Tools {
		total += t.Count
	}
	if total <= 0 {
		return Insight{}, false
	}
	share := float64(top.Count) / float64(total)
	savedHours := savedHoursForTool(top.Name, top.Count)

	title := fmt.Sprintf("%s was your top tool", top.Name)
	body := fmt.Sprintf("%d calls (%.0f%% of all tool use) saved an estimated %.1f hours",
		top.Count, share*100, savedHours)
	return Insight{
		ID:    "top_tool",
		Kind:  "top_tool",
		Title: title,
		Body:  body,
		Value: float64(top.Count),
		Metadata: map[string]interface{}{
			"tool":            top.Name,
			"share":           roundTo(share, 4),
			"saved_hours_est": roundTo(savedHours, 1),
		},
	}, true
}

// buildModelMixShiftInsight compares per-model cost shares between the current
// and previous period and reports the model with the largest share gain. We
// also surface the largest declining model so the body matches the spec.
//
// Skips when either period has no recorded models or when the largest delta is
// below 10 percentage points — otherwise the noise is too high to be useful.
func buildModelMixShiftInsight(a *storage.AnalysisResult, prev []storage.ModelCostRow) (Insight, bool) {
	if a == nil || len(a.Models) == 0 || len(prev) == 0 {
		return Insight{}, false
	}

	curShare := modelCostShare(modelsToCostMap(a.Models))
	prevShare := modelCostShare(prevModelsToCostMap(prev))
	if len(curShare) == 0 || len(prevShare) == 0 {
		return Insight{}, false
	}

	type delta struct {
		model string
		delta float64
	}
	all := make(map[string]struct{})
	for k := range curShare {
		all[k] = struct{}{}
	}
	for k := range prevShare {
		all[k] = struct{}{}
	}
	deltas := make([]delta, 0, len(all))
	for k := range all {
		deltas = append(deltas, delta{model: k, delta: curShare[k] - prevShare[k]})
	}
	sort.Slice(deltas, func(i, j int) bool {
		return deltas[i].delta > deltas[j].delta
	})

	up := deltas[0]
	down := deltas[len(deltas)-1]
	// Require a meaningful shift: at least 10 percentage points of growth.
	if up.delta < 0.10 {
		return Insight{}, false
	}

	title := fmt.Sprintf("Shifting from %s to %s", down.model, up.model)
	body := fmt.Sprintf("%s usage up %.0f%% — try Opus for hard tasks",
		up.model, up.delta*100)
	return Insight{
		ID:    "model_mix_shift",
		Kind:  "model_mix_shift",
		Title: title,
		Body:  body,
		Value: roundTo(up.delta, 4),
		Metadata: map[string]interface{}{
			"model_up":   up.model,
			"model_down": down.model,
			"delta_up":   roundTo(up.delta, 4),
			"delta_down": roundTo(down.delta, 4),
		},
	}, true
}

// buildCostAnomalyInsight delegates to computeCostAnomalies (already used by
// /api/analytics) and surfaces the single highest-z outlier. Anything with
// fewer than 3 sessions returns no insight — variance is undefined.
func buildCostAnomalyInsight(sessions []storage.TopSessionRow) (Insight, bool) {
	anomalies := computeCostAnomalies(sessions)
	if len(anomalies) == 0 {
		return Insight{}, false
	}
	top := anomalies[0]
	for _, a := range anomalies[1:] {
		if a.ZScore > top.ZScore {
			top = a
		}
	}
	short := top.SessionID
	if len(short) > 8 {
		short = short[:8]
	}
	title := "Outlier session detected"
	body := fmt.Sprintf("%s cost $%.2f vs $%.2f median (z=%.1f)",
		short, top.CostUSD, top.Mean, top.ZScore)
	return Insight{
		ID:    "cost_anomaly",
		Kind:  "cost_anomaly",
		Title: title,
		Body:  body,
		Value: top.CostUSD,
		Metadata: map[string]interface{}{
			"session_id": top.SessionID,
			"z_score":    roundTo(top.ZScore, 2),
			"mean":       roundTo(top.Mean, 2),
		},
	}, true
}

// buildRhythmInsight buckets the activity heatmap into four windows
// (weekday morning/afternoon/evening + weekend) and emits if any window
// holds at least 30% of total activity.
//
// Heatmap layout matches storage.AnalysisResult: row 0..4 = Mon..Fri,
// row 5 = Sat, row 6 = Sun.
func buildRhythmInsight(a *storage.AnalysisResult) (Insight, bool) {
	if a == nil {
		return Insight{}, false
	}

	type bucket struct {
		key, label string
		count      int
	}
	buckets := []bucket{
		{"weekday_morning", "Mon–Fri 9–13", 0},
		{"weekday_afternoon", "Mon–Fri 13–18", 0},
		{"weekday_evening", "Mon–Fri 18–23", 0},
		{"weekend", "weekends", 0},
	}

	var total int
	for dow := 0; dow < 7; dow++ {
		for hr := 0; hr < 24; hr++ {
			c := a.Heatmap[dow][hr]
			if c <= 0 {
				continue
			}
			total += c
			weekday := dow < 5
			switch {
			case weekday && hr >= 9 && hr < 13:
				buckets[0].count += c
			case weekday && hr >= 13 && hr < 18:
				buckets[1].count += c
			case weekday && hr >= 18 && hr < 23:
				buckets[2].count += c
			case !weekday:
				buckets[3].count += c
			}
		}
	}
	if total == 0 {
		return Insight{}, false
	}

	best := buckets[0]
	for _, b := range buckets[1:] {
		if b.count > best.count {
			best = b
		}
	}
	share := float64(best.count) / float64(total)
	if share < 0.30 {
		return Insight{}, false
	}

	titleSuffix := map[string]string{
		"weekday_morning":   "on weekday mornings",
		"weekday_afternoon": "on weekday afternoons",
		"weekday_evening":   "on weekday evenings",
		"weekend":           "on weekends",
	}[best.key]

	title := "You code most " + titleSuffix
	body := fmt.Sprintf("%.0f%% of tool calls happen %s", share*100, best.label)
	return Insight{
		ID:    "rhythm",
		Kind:  "rhythm",
		Title: title,
		Body:  body,
		Value: roundTo(share, 4),
		Metadata: map[string]interface{}{
			"window": best.key,
		},
	}, true
}

// modelsToCostMap reduces an []ModelStat slice (current period) to model→cost.
func modelsToCostMap(models []storage.ModelStat) map[string]float64 {
	m := make(map[string]float64, len(models))
	for _, mm := range models {
		m[mm.Model] = mm.CostUSD
	}
	return m
}

// prevModelsToCostMap reduces an []ModelCostRow slice (prev period) to model→cost.
func prevModelsToCostMap(models []storage.ModelCostRow) map[string]float64 {
	m := make(map[string]float64, len(models))
	for _, mm := range models {
		m[mm.Model] = mm.CostUSD
	}
	return m
}

// modelCostShare normalizes a model→cost map to model→share-of-total. Returns
// nil when the total is zero so callers can short-circuit.
func modelCostShare(costs map[string]float64) map[string]float64 {
	var total float64
	for _, v := range costs {
		total += v
	}
	if total <= 0 {
		return nil
	}
	out := make(map[string]float64, len(costs))
	for k, v := range costs {
		out[k] = v / total
	}
	return out
}

// weekdayNameFromISO returns "Mon" / "Tue" / ... for a YYYY-MM-DD date string.
// Falls back to the input string if parsing fails so the title never blanks.
func weekdayNameFromISO(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.Weekday().String()
}

// roundTo trims a float to n decimal places. The JSON encoder otherwise emits
// long fractional tails for ratios like 0.6666666666 — these are noise for a
// human-readable card payload.
func roundTo(v float64, n int) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	p := math.Pow10(n)
	return math.Round(v*p) / p
}

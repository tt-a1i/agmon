package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

// Forecast is the response shape for GET /api/forecast.
// All times serialize as UTC RFC3339; cost figures are USD.
type Forecast struct {
	Period             string                `json:"period"`
	PeriodStart        time.Time             `json:"period_start"`
	PeriodEnd          time.Time             `json:"period_end"`
	Now                time.Time             `json:"now"`
	ElapsedDays        int                   `json:"elapsed_days"`
	RemainingDays      int                   `json:"remaining_days"`
	SpentToDate        float64               `json:"spent_to_date"`
	BurnRatePerDay     float64               `json:"burn_rate_per_day"`
	BurnRateWindowDays int                   `json:"burn_rate_window_days"`
	ProjectedTotal     float64               `json:"projected_total"`
	ProjectedRemaining float64               `json:"projected_remaining"`
	Confidence         string                `json:"confidence"`
	Trend              string                `json:"trend"`
	VsPreviousPeriod   *PrevPeriodComparison `json:"vs_previous_period,omitempty"`
}

// PrevPeriodComparison ties projected_total to the previous calendar period
// (last month, last week). Omitted from the response when no spend was
// recorded in that window so first-time users don't see misleading 0% deltas.
type PrevPeriodComparison struct {
	PreviousTotal      float64 `json:"previous_total"`
	ProjectedChangePct float64 `json:"projected_change_pct"`
	Direction          string  `json:"direction"`
}

const (
	burnRateWindow = 7    // days
	trendWindow    = 3    // days
	trendThreshold = 0.15 // 15% deviation from burn rate flips up/down
)

func (s *Server) handleForecast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	period := strings.TrimSpace(r.URL.Query().Get("period"))
	if period == "" {
		period = "month"
	}
	if period != "month" && period != "week" {
		writeAPIError(w, http.StatusBadRequest, "invalid period")
		return
	}

	now := time.Now().In(time.Local)
	periodStart, periodEnd := periodBounds(period, now)
	prevStart, prevEnd := previousPeriodBounds(period, periodStart)
	totalDays := daysBetweenInclusive(periodStart, periodEnd)

	// One daily-cost query spanning whichever is earlier: period start, or
	// 7 days ago (needed so the burn-rate window is fully populated even
	// when we're early in the period). One more scalar query for the
	// previous-period comparison. Two DB round-trips, per the API contract.
	queryFrom := localDayStart(now).AddDate(0, 0, -(burnRateWindow - 1))
	if periodStart.Before(queryFrom) {
		queryFrom = localDayStart(periodStart)
	}
	daily, err := s.db.GetDailyCostsBetween(queryFrom, now)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	periodStartDay := localDayStart(periodStart).Format("2006-01-02")
	spent, dataDays := sumInPeriod(daily, periodStartDay)

	elapsedDays := daysElapsed(periodStart, now)
	remainingDays := daysRemaining(now, periodEnd)

	burnRate := lastWindowAverage(daily, burnRateWindow)
	projected := spent + burnRate*float64(remainingDays)
	confidence := computeConfidence(elapsedDays, totalDays, dataDays)
	trend := computeTrend(daily, burnRate)

	forecast := Forecast{
		Period:             period,
		PeriodStart:        periodStart.UTC(),
		PeriodEnd:          periodEnd.UTC(),
		Now:                now.UTC(),
		ElapsedDays:        elapsedDays,
		RemainingDays:      remainingDays,
		SpentToDate:        spent,
		BurnRatePerDay:     burnRate,
		BurnRateWindowDays: burnRateWindow,
		ProjectedTotal:     projected,
		ProjectedRemaining: projected - spent,
		Confidence:         confidence,
		Trend:              trend,
	}

	if cmp := comparePreviousPeriod(s.db, projected, prevStart, prevEnd); cmp != nil {
		forecast.VsPreviousPeriod = cmp
	}

	writeJSON(w, forecast)
}

// periodBounds returns [start, endInclusive] in local time for the requested
// period containing now. "month" → [first-of-month 00:00, last-of-month
// 23:59:59]. "week" → [Monday 00:00, Sunday 23:59:59].
func periodBounds(period string, now time.Time) (time.Time, time.Time) {
	loc := now.Location()
	switch period {
	case "month":
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
		end := start.AddDate(0, 1, 0).Add(-time.Second)
		return start, end
	case "week":
		// Monday-anchored week; Sunday becomes day 7 to match handleCosts.
		wd := now.Weekday()
		if wd == 0 {
			wd = 7
		}
		start := time.Date(now.Year(), now.Month(), now.Day()-int(wd-1), 0, 0, 0, 0, loc)
		end := start.AddDate(0, 0, 7).Add(-time.Second)
		return start, end
	}
	return now, now
}

// previousPeriodBounds returns the calendar period immediately preceding
// the period whose start is `curStart`. For month: last month. For week:
// last Monday→Sunday.
func previousPeriodBounds(period string, curStart time.Time) (time.Time, time.Time) {
	switch period {
	case "month":
		prevStart := curStart.AddDate(0, -1, 0)
		prevEnd := curStart.Add(-time.Second)
		return prevStart, prevEnd
	case "week":
		prevStart := curStart.AddDate(0, 0, -7)
		prevEnd := curStart.Add(-time.Second)
		return prevStart, prevEnd
	}
	return curStart, curStart
}

// localDayStart truncates t to local midnight. Mirrors the unexported helper
// inside storage so we can compute day-level keys without crossing the
// package boundary.
func localDayStart(t time.Time) time.Time {
	loc := t.Location()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// daysElapsed counts the calendar days from periodStart to now, inclusive of
// "today". Returns 0 when now precedes periodStart so projection math doesn't
// divide by zero.
func daysElapsed(periodStart, now time.Time) int {
	if !now.After(periodStart) {
		return 0
	}
	startDay := localDayStart(periodStart)
	nowDay := localDayStart(now)
	delta := int(nowDay.Sub(startDay).Hours()/24) + 1
	if delta < 1 {
		return 1
	}
	return delta
}

// daysRemaining counts whole calendar days left after "today" until periodEnd.
// A negative-or-zero result clamps to 0 so a finished period yields 0
// remaining and the projection collapses to spent_to_date.
func daysRemaining(now, periodEnd time.Time) int {
	if !periodEnd.After(now) {
		return 0
	}
	nowDay := localDayStart(now)
	endDay := localDayStart(periodEnd)
	delta := int(endDay.Sub(nowDay).Hours() / 24)
	if delta < 0 {
		return 0
	}
	return delta
}

// daysBetweenInclusive counts calendar days from start to end inclusive,
// matching how daysElapsed counts: both ends count as a full day each.
func daysBetweenInclusive(start, end time.Time) int {
	if !end.After(start) {
		return 1
	}
	startDay := localDayStart(start)
	endDay := localDayStart(end)
	delta := int(endDay.Sub(startDay).Hours()/24) + 1
	if delta < 1 {
		return 1
	}
	return delta
}

// sumInPeriod sums the cost of daily entries whose date is >= periodStartDay
// (YYYY-MM-DD), counting how many distinct days have positive cost. The
// daily slice is the period-extended series fetched by the handler.
func sumInPeriod(daily []storage.DailyCost, periodStartDay string) (sum float64, dataDays int) {
	for _, d := range daily {
		if d.Date < periodStartDay {
			continue
		}
		sum += d.Cost
		if d.Cost > 0 {
			dataDays++
		}
	}
	return sum, dataDays
}

// lastWindowAverage averages the last `window` entries in daily (oldest-first).
// Empty / short slices average over whatever is available; an empty input
// returns 0 rather than NaN.
func lastWindowAverage(daily []storage.DailyCost, window int) float64 {
	if len(daily) == 0 || window <= 0 {
		return 0
	}
	start := len(daily) - window
	if start < 0 {
		start = 0
	}
	var sum float64
	count := 0
	for _, d := range daily[start:] {
		sum += d.Cost
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// computeConfidence blends "how far into the period we are" with "how many
// days of data we have". The low-bar check fires first so an empty DB returns
// low even when we're mid-period.
func computeConfidence(elapsedDays, totalDays, dataDays int) string {
	if totalDays <= 0 {
		return "low"
	}
	share := float64(elapsedDays) / float64(totalDays)
	if share < 0.25 || dataDays < 7 {
		return "low"
	}
	if share >= 0.5 && dataDays >= 14 {
		return "high"
	}
	return "medium"
}

// computeTrend compares the last 3 days against the 7-day burn rate. Burn
// rates below $0.01/day collapse to "stable" so an empty-DB sequence doesn't
// trigger a false flip from division by near-zero.
func computeTrend(daily []storage.DailyCost, burnRate float64) string {
	if burnRate < 0.01 {
		return "stable"
	}
	recent := lastWindowAverage(daily, trendWindow)
	if recent <= 0 {
		return "down"
	}
	delta := (recent - burnRate) / burnRate
	switch {
	case delta >= trendThreshold:
		return "up"
	case delta <= -trendThreshold:
		return "down"
	default:
		return "stable"
	}
}

// comparePreviousPeriod fetches the previous period's actual cost and
// computes the projected delta. Returns nil when the previous period has no
// recorded spend — the caller then omits the field rather than emit a 0%
// comparison which would be misleading for first-time users.
func comparePreviousPeriod(db *storage.DB, projectedTotal float64, prevStart, prevEnd time.Time) *PrevPeriodComparison {
	prev, err := db.GetCostBetween(prevStart, prevEnd)
	if err != nil || prev <= 0 {
		return nil
	}
	pct := (projectedTotal - prev) / prev * 100
	direction := "flat"
	switch {
	case pct >= 5:
		direction = "up"
	case pct <= -5:
		direction = "down"
	}
	return &PrevPeriodComparison{
		PreviousTotal:      prev,
		ProjectedChangePct: roundTo(pct, 1),
		Direction:          direction,
	}
}

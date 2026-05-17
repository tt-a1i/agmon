package storage

import (
	"fmt"
	"strings"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

type BudgetRow struct {
	ID         int64
	Name       string
	MonthlyUSD float64
	Platform   string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (s *DB) InsertBudget(name string, monthlyUSD float64, platform string) (int64, error) {
	name, platform, err := normalizeBudgetInput(name, monthlyUSD, platform)
	if err != nil {
		return 0, err
	}
	now := formatStorageTime(time.Now())
	res, err := s.db.Exec(`
		INSERT INTO budgets (name, monthly_usd, platform, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, name, monthlyUSD, platform, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *DB) ListBudgets() ([]BudgetRow, error) {
	rows, err := s.db.Query(`
		SELECT id, name, monthly_usd, platform, created_at, updated_at
		FROM budgets ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []BudgetRow
	for rows.Next() {
		var r BudgetRow
		var createdAt, updatedAt string
		if err := rows.Scan(&r.ID, &r.Name, &r.MonthlyUSD, &r.Platform, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		r.CreatedAt = parseTime(createdAt)
		r.UpdatedAt = parseTime(updatedAt)
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *DB) UpdateBudget(id int64, name string, monthlyUSD float64, platform string) error {
	name, platform, err := normalizeBudgetInput(name, monthlyUSD, platform)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		UPDATE budgets
		SET name = ?, monthly_usd = ?, platform = ?, updated_at = ?
		WHERE id = ?
	`, name, monthlyUSD, platform, formatStorageTime(time.Now()), id)
	return err
}

func (s *DB) DeleteBudget(id int64) error {
	_, err := s.db.Exec(`DELETE FROM budgets WHERE id = ?`, id)
	return err
}

func (s *DB) GetBudgetUsage(id int64) (used, limit float64, err error) {
	var platform string
	if err := s.db.QueryRow(`SELECT monthly_usd, platform FROM budgets WHERE id = ?`, id).Scan(&limit, &platform); err != nil {
		return 0, 0, err
	}
	from := startOfMonth(time.Now())
	used, err = s.GetCostBetweenForPlatform(from, time.Now(), platform)
	if err != nil {
		return 0, 0, err
	}
	return used, limit, nil
}

func normalizeBudgetInput(name string, monthlyUSD float64, platform string) (string, string, error) {
	name = strings.TrimSpace(name)
	platform = strings.TrimSpace(platform)
	if name == "" {
		return "", "", fmt.Errorf("budget name is required")
	}
	if monthlyUSD <= 0 {
		return "", "", fmt.Errorf("monthly budget must be positive")
	}
	if platform != "" && platform != string(event.PlatformClaude) && platform != string(event.PlatformCodex) {
		return "", "", fmt.Errorf("invalid budget platform")
	}
	return name, platform, nil
}

func startOfMonth(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
}

package main

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func runBudget() error {
	if maybePrintCmdHelp("budget", os.Args[2:]) {
		return nil
	}
	if len(os.Args) < 3 {
		return fmt.Errorf("usage: tokenmeter budget <list|set|delete|usage>")
	}

	db := mustOpenDB()
	defer db.Close()

	switch os.Args[2] {
	case "list":
		return runBudgetList(db)
	case "set":
		return runBudgetSet(db, os.Args[3:])
	case "delete":
		return runBudgetDelete(db, os.Args[3:])
	case "usage":
		return runBudgetUsage(db, os.Args[3:])
	default:
		return fmt.Errorf("unknown budget command: %s", os.Args[2])
	}
}

func runBudgetList(db *storage.DB) error {
	budgets, err := db.ListBudgets()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tPLATFORM\tLIMIT\tUSED\tPERCENT\tSTATUS")
	for _, budget := range budgets {
		used, limit, err := db.GetBudgetUsage(budget.ID)
		if err != nil {
			return err
		}
		percent, status := budgetPercentStatus(used, limit)
		platform := budget.Platform
		if platform == "" {
			platform = "all"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t$%.2f\t$%.2f\t%.1f%%\t%s\n",
			budget.ID, budget.Name, platform, limit, used, percent, status)
	}
	return tw.Flush()
}

func runBudgetSet(db *storage.DB, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: tokenmeter budget set <name> <monthly_usd> [--platform claude|codex]")
	}
	name := args[0]
	monthlyUSD, err := strconv.ParseFloat(args[1], 64)
	if err != nil {
		return fmt.Errorf("invalid monthly_usd: %s", args[1])
	}
	platform := ""
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--platform":
			if i+1 >= len(args) {
				return fmt.Errorf("--platform requires a value")
			}
			platform = args[i+1]
			i++
		default:
			return fmt.Errorf("unknown budget set argument: %s", args[i])
		}
	}

	budgets, err := db.ListBudgets()
	if err != nil {
		return err
	}
	for _, budget := range budgets {
		if budget.Name == name {
			if err := db.UpdateBudget(budget.ID, name, monthlyUSD, platform); err != nil {
				return err
			}
			fmt.Printf("Set budget %d: %s $%.2f\n", budget.ID, name, monthlyUSD)
			return nil
		}
	}

	id, err := db.InsertBudget(name, monthlyUSD, platform)
	if err != nil {
		return err
	}
	fmt.Printf("Set budget %d: %s $%.2f\n", id, name, monthlyUSD)
	return nil
}

func runBudgetDelete(db *storage.DB, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tokenmeter budget delete <id>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid budget id: %s", args[0])
	}
	if err := db.DeleteBudget(id); err != nil {
		return err
	}
	fmt.Printf("Deleted budget %d\n", id)
	return nil
}

func runBudgetUsage(db *storage.DB, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tokenmeter budget usage <id>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid budget id: %s", args[0])
	}

	budget, err := findBudget(db, id)
	if err != nil {
		return err
	}
	used, limit, err := db.GetBudgetUsage(id)
	if err != nil {
		return err
	}
	percent, status := budgetPercentStatus(used, limit)
	platform := budget.Platform
	if platform == "" {
		platform = "all"
	}
	fmt.Printf("%s (%s)\n", budget.Name, platform)
	fmt.Printf("Usage: $%.2f / $%.2f (%.1f%%, %s)\n", used, limit, percent, status)
	return nil
}

func findBudget(db *storage.DB, id int64) (storage.BudgetRow, error) {
	budgets, err := db.ListBudgets()
	if err != nil {
		return storage.BudgetRow{}, err
	}
	for _, budget := range budgets {
		if budget.ID == id {
			return budget, nil
		}
	}
	return storage.BudgetRow{}, fmt.Errorf("budget not found: %d", id)
}

func budgetPercentStatus(used, limit float64) (float64, string) {
	percent := 0.0
	if limit > 0 {
		percent = used / limit * 100
	}
	status := "ok"
	if percent >= 100 {
		status = "over"
	} else if percent >= 80 {
		status = "warn"
	}
	return percent, status
}

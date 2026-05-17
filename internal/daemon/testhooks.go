package daemon

import "context"

// RunBudgetSweepForTest runs one budget transition pass synchronously.
// It is used by integration tests to avoid waiting for the production ticker.
func (d *Daemon) RunBudgetSweepForTest(ctx context.Context) error {
	return d.checkBudgetTransitions(ctx)
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/daemon"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

const defaultHealthcheckURL = "http://127.0.0.1:8370/api/health"

type healthcheckResponse struct {
	Status        string            `json:"status"`
	UptimeSeconds int64             `json:"uptime_seconds,omitempty"`
	Checks        healthcheckChecks `json:"checks"`
	Version       string            `json:"version,omitempty"`
}

type healthcheckChecks struct {
	DB     healthcheckCheck `json:"db"`
	Daemon healthcheckCheck `json:"daemon"`
}

type healthcheckCheck struct {
	Status    string  `json:"status"`
	LatencyMS float64 `json:"latency_ms,omitempty"`
}

func runHealthcheck() error {
	if maybePrintCmdHelp("healthcheck", os.Args[2:]) {
		return nil
	}
	code, err := runHealthcheckWithDeps(os.Args[2:], os.Stdout, defaultHealthcheckURL)
	if err != nil {
		return err
	}
	os.Exit(code)
	return nil
}

func runHealthcheckWithDeps(args []string, out io.Writer, webURL string) (int, error) {
	jsonOutput := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		default:
			return 2, fmt.Errorf("unknown healthcheck argument: %s", arg)
		}
	}

	resp, code := healthcheckFromWeb(webURL)
	if code == 2 {
		resp, code = localHealthcheck()
	}

	if jsonOutput {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return code, enc.Encode(resp)
	}

	switch code {
	case 0:
		fmt.Fprintln(out, "OK")
	case 1:
		fmt.Fprintf(out, "UNHEALTHY: %s\n", healthcheckSummary(resp))
	default:
		fmt.Fprintf(out, "UNREACHABLE: %s\n", healthcheckSummary(resp))
	}
	return code, nil
}

func healthcheckFromWeb(webURL string) (healthcheckResponse, int) {
	if strings.TrimSpace(webURL) == "" {
		return healthcheckResponse{Status: "unreachable"}, 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, webURL, nil)
	if err != nil {
		return healthcheckResponse{Status: "unreachable", Checks: healthcheckChecks{
			Daemon: healthcheckCheck{Status: "error: " + err.Error()},
		}}, 2
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return healthcheckResponse{Status: "unreachable", Checks: healthcheckChecks{
			Daemon: healthcheckCheck{Status: "error: " + err.Error()},
		}}, 2
	}
	defer res.Body.Close()

	var resp healthcheckResponse
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return healthcheckResponse{Status: "unreachable", Checks: healthcheckChecks{
			Daemon: healthcheckCheck{Status: "error: " + err.Error()},
		}}, 2
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 && resp.Status == "healthy" {
		return resp, 0
	}
	if res.StatusCode == http.StatusServiceUnavailable || resp.Status == "unhealthy" {
		return resp, 1
	}
	return resp, 2
}

func localHealthcheck() (healthcheckResponse, int) {
	dbCheck := localDBHealthcheck()
	daemonCheck := localDaemonHealthcheck()

	status := "healthy"
	code := 0
	if dbCheck.Status != "ok" || daemonCheck.Status != "ok" {
		status = "unhealthy"
		code = 1
	}

	return healthcheckResponse{
		Status: status,
		Checks: healthcheckChecks{
			DB:     dbCheck,
			Daemon: daemonCheck,
		},
		Version: version,
	}, code
}

func localDBHealthcheck() healthcheckCheck {
	start := time.Now()
	db, err := storage.Open(storage.DefaultDBPath())
	if err != nil {
		return healthcheckCheck{Status: "error: " + err.Error(), LatencyMS: healthcheckLatencyMS(start)}
	}
	defer db.Close()

	if _, err := db.GetVisibleSessionCount(); err != nil {
		return healthcheckCheck{Status: "error: " + err.Error(), LatencyMS: healthcheckLatencyMS(start)}
	}
	return healthcheckCheck{Status: "ok", LatencyMS: healthcheckLatencyMS(start)}
}

func localDaemonHealthcheck() healthcheckCheck {
	if running, _ := daemon.IsRunning(); running {
		return healthcheckCheck{Status: "ok"}
	}
	return healthcheckCheck{Status: "unreachable"}
}

func healthcheckLatencyMS(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}

func healthcheckSummary(resp healthcheckResponse) string {
	if resp.Checks.DB.Status != "" && resp.Checks.DB.Status != "ok" {
		return "db " + resp.Checks.DB.Status
	}
	if resp.Checks.Daemon.Status != "" && resp.Checks.Daemon.Status != "ok" {
		return "daemon " + resp.Checks.Daemon.Status
	}
	if resp.Status != "" {
		return resp.Status
	}
	return "unknown"
}

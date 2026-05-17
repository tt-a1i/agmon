package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

type VersionInfo struct {
	Current          string `json:"current"`
	Latest           string `json:"latest,omitempty"`
	IsNewerAvailable bool   `json:"is_newer_available"`
	ReleaseURL       string `json:"release_url,omitempty"`
	ReleasedAt       string `json:"released_at,omitempty"`
	CheckError       string `json:"check_error,omitempty"`
}

func runVersion() error {
	if maybePrintCmdHelp("version", os.Args[2:]) {
		return nil
	}
	return runVersionWithDeps(os.Args[1:], os.Stdout)
}

func runVersionWithDeps(args []string, out io.Writer) error {
	check := false
	jsonOutput := false
	for _, arg := range args[1:] {
		switch arg {
		case "--check":
			check = true
		case "--json":
			jsonOutput = true
		default:
			return fmt.Errorf("unknown version argument: %s", arg)
		}
	}

	info := VersionInfo{Current: normalizeVersion(version)}
	if check {
		rel, err := fetchLatestReleaseFunc()
		if err != nil {
			info.CheckError = err.Error()
		} else {
			info.Latest = rel.TagName
			info.ReleaseURL = releaseURL(rel.TagName)
			if !rel.PublishedAt.IsZero() {
				info.ReleasedAt = rel.PublishedAt.UTC().Format(time.RFC3339)
			}
			info.IsNewerAvailable = isNewerVersion(info.Latest, info.Current)
		}
	}

	if jsonOutput {
		return json.NewEncoder(out).Encode(info)
	}
	printVersionText(out, info, check)
	return nil
}

func printVersionText(out io.Writer, info VersionInfo, checked bool) {
	if !checked {
		fmt.Fprintf(out, "tokenmeter %s\n", info.Current)
		return
	}
	if info.CheckError != "" {
		fmt.Fprintf(out, "TokenMeter %s\n", info.Current)
		fmt.Fprintf(out, "Update check failed: %s\n", info.CheckError)
		return
	}
	if !info.IsNewerAvailable {
		fmt.Fprintf(out, "TokenMeter %s (current, latest)\n", info.Current)
		fmt.Fprintln(out, "You're up to date.")
		return
	}

	fmt.Fprintf(out, "TokenMeter %s (current)\n", info.Current)
	if info.ReleasedAt != "" {
		if t, err := time.Parse(time.RFC3339, info.ReleasedAt); err == nil {
			fmt.Fprintf(out, "Latest: %s — released %s\n", info.Latest, t.Format("2006-01-02"))
		} else {
			fmt.Fprintf(out, "Latest: %s\n", info.Latest)
		}
	} else {
		fmt.Fprintf(out, "Latest: %s\n", info.Latest)
	}
	fmt.Fprintf(out, "Update: %s\n\n", info.ReleaseURL)
	fmt.Fprintln(out, "To update: tokenmeter update")
}

func isNewerVersion(remote, local string) bool {
	remoteParts, ok := parseVersionParts(remote)
	if !ok {
		return false
	}
	localParts, ok := parseVersionParts(local)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		if remoteParts[i] > localParts[i] {
			return true
		}
		if remoteParts[i] < localParts[i] {
			return false
		}
	}
	return false
}

func parseVersionParts(v string) ([3]int, bool) {
	var parts [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	raw := strings.Split(v, ".")
	if len(raw) != 3 {
		return parts, false
	}
	for i := range raw {
		n, err := strconv.Atoi(raw[i])
		if err != nil {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}

func normalizeVersion(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

func releaseURL(tag string) string {
	return "https://github.com/tt-a1i/tokenmeter/releases/tag/" + tag
}

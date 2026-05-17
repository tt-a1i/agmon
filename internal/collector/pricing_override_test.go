package collector

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func setPricingTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
	return home
}

func restorePricingTablesAfterTest(t *testing.T) {
	t.Helper()
	claude := append([]modelPricing(nil), claudePricingTable...)
	codex := append([]modelPricing(nil), codexPricingTable...)
	t.Cleanup(func() {
		claudePricingTable = claude
		codexPricingTable = codex
	})
}

func writePricingOverride(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".tokenmeter")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pricing.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	oldOutput := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldOutput)
		log.SetFlags(oldFlags)
	})
	return &buf
}

func TestLoadPricingOverridesAbsentFile(t *testing.T) {
	restorePricingTablesAfterTest(t)
	setPricingTestHome(t)

	LoadPricingOverrides()

	if !reflect.DeepEqual(claudePricingTable, defaultClaudePricingTable) {
		t.Fatal("claude pricing table changed when override file is absent")
	}
	if !reflect.DeepEqual(codexPricingTable, defaultCodexPricingTable) {
		t.Fatal("codex pricing table changed when override file is absent")
	}
}

func TestLoadPricingOverridesMalformedJSON(t *testing.T) {
	restorePricingTablesAfterTest(t)
	home := setPricingTestHome(t)
	writePricingOverride(t, home, `{"claude": [`)
	logs := captureLog(t)

	LoadPricingOverrides()

	if !strings.Contains(logs.String(), "pricing override") {
		t.Fatalf("expected malformed pricing override to log a warning, got %q", logs.String())
	}
	if !reflect.DeepEqual(claudePricingTable, defaultClaudePricingTable) {
		t.Fatal("claude pricing table changed for malformed override file")
	}
	if !reflect.DeepEqual(codexPricingTable, defaultCodexPricingTable) {
		t.Fatal("codex pricing table changed for malformed override file")
	}
}

func TestLoadPricingOverridesMergesAtFront(t *testing.T) {
	restorePricingTablesAfterTest(t)
	home := setPricingTestHome(t)
	writePricingOverride(t, home, `{
		"claude": [
			{"match": ["sonnet"], "inputPerMillion": 42.0, "outputPerMillion": 84.0, "cacheCreatePerMill": 4.2, "cacheReadPerMill": 0.42}
		],
		"codex": [
			{"match": ["gpt-5"], "inputPerMillion": 9.0, "outputPerMillion": 18.0, "cacheReadPerMill": 0.9}
		]
	}`)

	LoadPricingOverrides()

	if got := claudePricingTable[0]; !reflect.DeepEqual(got.match, []string{"sonnet"}) {
		t.Fatalf("claude override was not prepended: first match = %#v", got.match)
	}
	if got := codexPricingTable[0]; !reflect.DeepEqual(got.match, []string{"gpt-5"}) {
		t.Fatalf("codex override was not prepended: first match = %#v", got.match)
	}
	if got := claudePricing("claude-sonnet-4-6"); got.inputPerMillion != 42.0 || got.outputPerMillion != 84.0 {
		t.Fatalf("claudePricing did not prefer override, got input=%v output=%v", got.inputPerMillion, got.outputPerMillion)
	}
	input, output, cache := CodexPricing("gpt-5")
	if input != 9.0 || output != 18.0 || cache != 0.9 {
		t.Fatalf("CodexPricing did not prefer override, got input=%v output=%v cache=%v", input, output, cache)
	}
}

func TestLoadPricingOverridesValidatesFields(t *testing.T) {
	restorePricingTablesAfterTest(t)
	home := setPricingTestHome(t)
	writePricingOverride(t, home, `{
		"codex": [
			{"match": ["gpt-5"], "inputPerMillion": -1.0, "outputPerMillion": 1.0},
			{"match": ["local-model"], "inputPerMillion": 0.5, "outputPerMillion": 1.0}
		]
	}`)

	LoadPricingOverrides()

	input, output, _ := CodexPricing("gpt-5")
	if input != 1.25 || output != 10.0 {
		t.Fatalf("invalid override should be skipped; got gpt-5 input=%v output=%v", input, output)
	}
	input, output, _ = CodexPricing("local-model")
	if input != 0.5 || output != 1.0 {
		t.Fatalf("valid override after invalid entry should still load; got input=%v output=%v", input, output)
	}
}

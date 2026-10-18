package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultsAreNeutral(t *testing.T) {
	c := Default()
	if c.Review.Channel != "" || c.Jira.BaseURL != "" || len(c.MergeWindows.Days) != 0 || c.ProductionLabel != "" {
		t.Fatalf("defaults must not name any organisation: %+v", c)
	}
	if c.Review.RemindAfter.Duration != 5*time.Hour || c.MergeWindows.RemindBefore.Duration != 10*time.Minute {
		t.Fatalf("approved defaults: remind_after=%v remind_before=%v", c.Review.RemindAfter, c.MergeWindows.RemindBefore)
	}
	if c.MergeWindows.OutsideWindow != "schedule" || c.MergeWindows.MissedWindow != "next_window" || !c.ProductionLabelIsCritical {
		t.Fatalf("approved scheduling defaults changed: %+v", c.MergeWindows)
	}
}

func TestExampleConfigLoads(t *testing.T) {
	c, err := Load(filepath.Join("..", "..", "docs", "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Review.Channel == "" || len(c.MergeWindows.Slots) != 2 || c.Review.RemindAfter.Duration != 5*time.Hour {
		t.Fatalf("example config: %+v", c)
	}
	sel := strings.Join(c.CriticalSelectors(), ",")
	if !strings.Contains(sel, "label:"+c.ProductionLabel) {
		t.Fatalf("production label should count as critical: %s", sel)
	}
}

func TestValidation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte("[merge_windows]\noutside_window = \"later\"\n"), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("bad outside_window must fail")
	}
	os.WriteFile(p, []byte("[github]\nmerge_method = \"octopus\"\n"), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("bad merge_method must fail")
	}
}

func TestSecretPrefersEnvAndNeverConfig(t *testing.T) {
	t.Setenv("PRD_TEST_TOKEN", " abc ")
	if got := Secret([]string{"PRD_TEST_UNSET", "PRD_TEST_TOKEN"}, []string{"echo", "cmd"}); got != "abc" {
		t.Fatalf("env first: %q", got)
	}
	if got := Secret([]string{"PRD_TEST_UNSET"}, []string{"echo", "from-cmd"}); got != "from-cmd" {
		t.Fatalf("cmd fallback: %q", got)
	}
	if got := Secret(nil, nil); got != "" {
		t.Fatalf("nothing configured: %q", got)
	}
}

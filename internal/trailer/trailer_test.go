package trailer

import (
	"testing"
	"time"
)

func TestPurpose(t *testing.T) {
	body := "Bumps the limit.\n\nPurpose: Allow uploads up to 25 MB on eng first.\nMerge-after: #318\n"
	if got := Purpose(body); got != "Allow uploads up to 25 MB on eng first." {
		t.Fatalf("Purpose = %q", got)
	}
	if got := Purpose("no trailer here"); got != "" {
		t.Fatalf("Purpose without trailer = %q", got)
	}
}

func TestMergeAfter(t *testing.T) {
	cases := []struct {
		body string
		repo string
		n    int
		ok   bool
	}{
		{"Merge-after: #318", "", 318, true},
		{"x\nmerge-after: acme/infra-config#12\n", "acme/infra-config", 12, true},
		{"Merge-after: soon", "", 0, false},
		{"", "", 0, false},
	}
	for _, c := range cases {
		repo, n, ok := MergeAfter(c.body)
		if repo != c.repo || n != c.n || ok != c.ok {
			t.Errorf("MergeAfter(%q) = %q %d %v, want %q %d %v", c.body, repo, n, ok, c.repo, c.n, c.ok)
		}
	}
}

func TestTopic(t *testing.T) {
	for _, body := range []string{
		"Topic: prod PDB\n",
		"## TLDR\n- Topic: prod PDB\n- Purpose: x\n",
		"**Topic:** prod PDB",
		"* **Topic**: prod PDB",
	} {
		if got := Topic(body); got != "prod PDB" {
			t.Errorf("Topic(%q) = %q", body, got)
		}
	}
	if got := Topic("Topics are hard"); got != "" {
		t.Errorf("Topic without trailer = %q", got)
	}
}

func TestHold(t *testing.T) {
	paris, _ := time.LoadLocation("Europe/Paris")
	tallinn, _ := time.LoadLocation("Europe/Tallinn")
	cases := []struct {
		body   string
		until  time.Time
		reason string
		ok     bool
	}{
		{"Hold-until: 2026-10-29 19:00 Europe/Paris", time.Date(2026, 10, 29, 19, 0, 0, 0, paris), "2026-10-29 19:00 Europe/Paris", true},
		{"- **Hold-until:** 2026-10-29T19:00 Europe/Tallinn", time.Date(2026, 10, 29, 19, 0, 0, 0, tallinn), "2026-10-29T19:00 Europe/Tallinn", true},
		{"hold-until: 2026-10-29 UTC", time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC), "2026-10-29 UTC", true},
		{"Hold-until: after the cutover", time.Time{}, "after the cutover", true},
		{"- Hold: waiting for the vendor", time.Time{}, "waiting for the vendor", true},
		{"Holder: someone", time.Time{}, "", false},
		{"", time.Time{}, "", false},
	}
	for _, c := range cases {
		until, reason, ok := Hold(c.body)
		if !until.Equal(c.until) || reason != c.reason || ok != c.ok {
			t.Errorf("Hold(%q) = %v %q %v, want %v %q %v", c.body, until, reason, ok, c.until, c.reason, c.ok)
		}
	}
}

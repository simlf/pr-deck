package ticket

import "testing"

func TestExtractOrder(t *testing.T) {
	if got := Extract(nil, "ops-412-body-size-eng", "OPS-999 other", ""); got != "OPS-412" {
		t.Fatalf("branch should win, got %q", got)
	}
	if got := Extract(nil, "main", "OPS-415 redis: rotate certs", ""); got != "OPS-415" {
		t.Fatalf("title fallback, got %q", got)
	}
	if got := Extract(nil, "fix", "bump", "Refs OPS-398"); got != "OPS-398" {
		t.Fatalf("body fallback, got %q", got)
	}
	if got := Extract(nil, "utf-8-fix", "sha-256 support", ""); got != "" {
		t.Fatalf("false positives must be skipped, got %q", got)
	}
}

func TestExtractProjects(t *testing.T) {
	if got := Extract([]string{"ops"}, "feat-12-thing", "OPS-7 x", ""); got != "OPS-7" {
		t.Fatalf("allow-list should skip FEAT, got %q", got)
	}
}

func TestShort(t *testing.T) {
	if got := Short("Allow bigger uploads (ingress 25 MB)", "", 22); got != "Allow bigger uploads" {
		t.Fatalf("Short = %q", got)
	}
	if got := Short("Renew redis TLS certs", "Redis certs", 22); got != "Redis certs" {
		t.Fatalf("alias must win, got %q", got)
	}
	if got := Short("Short one", "", 22); got != "Short one" {
		t.Fatalf("Short = %q", got)
	}
}

func TestStripKey(t *testing.T) {
	for in, want := range map[string]string{
		"OPS-412 ingress: body size 25m (eng)": "ingress: body size 25m (eng)",
		"[OPS-412] ingress":                    "ingress",
		"ingress fix (ops-412)":                "ingress fix",
		"OPS-412":                              "",
	} {
		if got := StripKey(in, "OPS-412"); got != want {
			t.Errorf("StripKey(%q) = %q, want %q", in, got, want)
		}
	}
}

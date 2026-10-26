package ticket

import "testing"

func TestExtractOrder(t *testing.T) {
	if got := Extract(nil, "abc-412-body-size-eng", "ABC-999 other", ""); got != "ABC-412" {
		t.Fatalf("branch should win, got %q", got)
	}
	if got := Extract(nil, "main", "ABC-415 redis: rotate certs", ""); got != "ABC-415" {
		t.Fatalf("title fallback, got %q", got)
	}
	if got := Extract(nil, "fix", "bump", "Refs ABC-398"); got != "ABC-398" {
		t.Fatalf("body fallback, got %q", got)
	}
	if got := Extract(nil, "utf-8-fix", "sha-256 support", ""); got != "" {
		t.Fatalf("false positives must be skipped, got %q", got)
	}
}

func TestExtractProjects(t *testing.T) {
	if got := Extract([]string{"abc"}, "feat-12-thing", "ABC-7 x", ""); got != "ABC-7" {
		t.Fatalf("allow-list should skip FEAT, got %q", got)
	}
	if got := Extract([]string{"abc"}, "feat/abc-0000-tidy", "fix(abc-0000): tidy", "Refs ABC-12"); got != "ABC-12" {
		t.Fatalf("ABC-0000 placeholder should be skipped, got %q", got)
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
		"ABC-412 ingress: body size 25m (eng)": "ingress: body size 25m (eng)",
		"[ABC-412] ingress":                    "ingress",
		"ingress fix (abc-412)":                "ingress fix",
		"ABC-412":                              "",
		"feat(abc-412): monitor redis":         "monitor redis",
		"ingress: body size":                   "ingress: body size",
	} {
		if got := StripKey(in, "ABC-412"); got != want {
			t.Errorf("StripKey(%q) = %q, want %q", in, got, want)
		}
	}
}

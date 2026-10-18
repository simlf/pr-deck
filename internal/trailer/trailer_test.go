package trailer

import "testing"

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

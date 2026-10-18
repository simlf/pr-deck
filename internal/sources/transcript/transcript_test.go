package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanFindsCreatingPrompt(t *testing.T) {
	ix := Load(filepath.Join(t.TempDir(), "none.json"))
	if err := ix.Scan("testdata/projects", "github.example"); err != nil {
		t.Fatal(err)
	}
	w, ok := ix.Lookup("acme/infra-config#318")
	if !ok || !strings.HasPrefix(w.Prompt, "bump the ingress body size to 25m") {
		t.Fatalf("#318 why = %+v %v", w, ok)
	}
	if w.SessionID != "sess-a" || w.Cwd != "/work/infra-config" {
		t.Fatalf("#318 context = %+v", w)
	}
	w2, ok := ix.Lookup("acme/infra-config#319")
	if !ok || w2.Prompt != "now open the prod one too" {
		t.Fatalf("#319 why = %+v", w2)
	}
	if _, ok := ix.Lookup("acme/infra-config#999"); ok {
		t.Fatal("a PR merely viewed (gh pr view) must not get a why")
	}
}

func TestScanIsIncrementalAndPersists(t *testing.T) {
	dir := t.TempDir()
	src, _ := os.ReadFile("testdata/projects/-work-infra-config/session-a.jsonl")
	os.MkdirAll(filepath.Join(dir, "p"), 0o700)
	f := filepath.Join(dir, "p", "s.jsonl")
	os.WriteFile(f, src, 0o600)
	idx := filepath.Join(dir, "why.json")
	ix := Load(idx)
	ix.Scan(dir, "github.example")
	if err := ix.Save(idx); err != nil {
		t.Fatal(err)
	}
	again := Load(idx)
	if _, ok := again.Lookup("acme/infra-config#318"); !ok {
		t.Fatal("index did not persist")
	}
	delete(again.Whys, "acme/infra-config#318")
	again.Scan(dir, "github.example") // unchanged file: skipped
	if _, ok := again.Lookup("acme/infra-config#318"); ok {
		t.Fatal("unchanged file should not be rescanned")
	}
}

func TestOtherHostIgnored(t *testing.T) {
	ix := Load("")
	ix.Scan("testdata/projects", "github.com")
	if len(ix.Whys) != 0 {
		t.Fatalf("URLs on another host must be ignored, got %v", ix.Whys)
	}
}

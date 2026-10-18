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

func TestGrowingFileResumesWithContext(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "p"), 0o700)
	f := filepath.Join(dir, "p", "s.jsonl")
	part1 := `{"type":"user","message":{"content":"open the prod twin"},"cwd":"/w","sessionId":"S","timestamp":"2026-10-15T14:00:00Z"}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t9","input":{"command":"gh pr create"}}]}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_res` // partial line: the agent is still writing
	os.WriteFile(f, []byte(part1), 0o600)
	ix := Load("")
	ix.Scan(dir, "github.example")
	if len(ix.Whys) != 0 {
		t.Fatalf("nothing complete yet: %v", ix.Whys)
	}
	rest := `ult","tool_use_id":"t9","content":"https://github.example/acme/r/pull/5"}]},"cwd":"/w","sessionId":"S"}` + "\n"
	fh, _ := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0)
	fh.WriteString(rest)
	fh.Close()
	ix.Scan(dir, "github.example")
	w, ok := ix.Lookup("acme/r#5")
	if !ok || w.Prompt != "open the prod twin" {
		t.Fatalf("resumed scan lost the prompt or tool_use context: %+v %v", w, ok)
	}
	if ix.Files[f].Offset != int64(len(part1)+len(rest)) {
		t.Fatalf("offset = %d", ix.Files[f].Offset)
	}
}

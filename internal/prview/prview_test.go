package prview

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/simlf/pr-deck/internal/config"
)

const diff = `diff --git a/main.tf b/main.tf
index 1..2 100644
--- a/main.tf
+++ b/main.tf
@@ -10,3 +10,4 @@ resource "x" "y" {
   a = 1
-  b = 2
+  b = 3
+  c = 4
diff --git a/old.txt b/new.txt
--- a/old.txt
+++ b/new.txt
@@ -1 +1 @@
-x
+y
`

func keys(m *Model, ks ...string) {
	for _, k := range ks {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m.Update(msg)
	}
}

func TestParsePlacesLines(t *testing.T) {
	var got []string
	for _, l := range Parse(diff) {
		got = append(got, l.File+":"+strconv.Itoa(l.New))
	}
	want := "main.tf:0 main.tf:0 main.tf:0 main.tf:0 main.tf:10 main.tf:10 main.tf:11 main.tf:11 main.tf:12 new.txt:0 new.txt:0 new.txt:0 new.txt:1 new.txt:1 new.txt:1"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}

func TestNoteOnFileLine(t *testing.T) {
	var sent []string
	m := New("https://github.example/acme/infra/pull/7", diff, func(s string) error { sent = append(sent, s); return nil })
	keys(m, "j", "j", "j", "j", "j", "j", "j")
	if m.Where() != "main.tf:11" {
		t.Fatalf("where = %q", m.Where())
	}
	keys(m, "c", "w", "h", "y", "space", "3", "?", "enter")
	if len(sent) != 1 || sent[0] != "https://github.example/acme/infra/pull/7 main.tf:11 why 3?" {
		t.Fatalf("sent = %q", sent)
	}
	keys(m, "n")
	if m.Lines[m.cursor].File != "new.txt" || !strings.Contains(m.View(), "new.txt") {
		t.Fatalf("n jumps to the next file, at %q", m.Where())
	}
	off := New("u", diff, nil)
	keys(off, "c")
	if off.typing || !strings.Contains(off.msg, "[firstmate]") {
		t.Fatalf("notes need [firstmate]: %q", off.msg)
	}
}

func TestNoteRunsFmInbox(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	os.WriteFile(filepath.Join(dir, "fm-inbox.sh"), []byte("#!/bin/sh\necho \"$FM_HOME|$*\" > "+out+"\n"), 0o755)
	if err := Note(config.Firstmate{Home: "/fm", Bin: dir}, "u main.tf:11 why"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "/fm|note -- u main.tf:11 why\n" {
		t.Fatalf("fm-inbox got %q", b)
	}
	if err := Note(config.Firstmate{}, "x"); err == nil {
		t.Fatal("no [firstmate] config must refuse")
	}
}

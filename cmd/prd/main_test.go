package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/simlf/pr-deck/internal/cache"
	"github.com/simlf/pr-deck/internal/model"
)

func TestClickedURL(t *testing.T) {
	cases := map[string]string{
		`{"clicked_url":"https://github.example/acme/infra-config/pull/319","workspace_id":"w1"}`: "https://github.example/acme/infra-config/pull/319",
		`{"selected_text":"see https://github.example/a/b/pull/7 please"}`:                        "https://github.example/a/b/pull/7",
		``: "",
	}
	for in, want := range cases {
		if got := clickedURL(in); got != want {
			t.Errorf("clickedURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripFromCacheAndDoctor(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	os.WriteFile(cfgPath, []byte(`
envs = ["eng", "prod"]
[deck]
state_dir = "`+filepath.Join(dir, "state")+`"
cache_dir = "`+filepath.Join(dir, "cache")+`"
[github]
token_env = ["PRD_TEST_NO_SUCH_TOKEN"]
token_cmd = []
[why]
transcripts_dir = "`+filepath.Join(dir, "none")+`"
`), 0o600)
	t.Setenv("PRD_CONFIG", cfgPath)
	t.Setenv("HERDR_SOCKET_PATH", "")
	var out, errb bytes.Buffer
	if err := run([]string{"strip"}, &out, &errb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no data yet") {
		t.Fatalf("strip without cache = %q", out.String())
	}
	snap := model.Snapshot{At: time.Now(), PRs: []model.PR{{Repo: "acme/x", Number: 1, State: "OPEN", CI: model.CIPass,
		ReviewDecision: model.ReviewApproved, LastCommitAt: time.Now(), Purpose: "Do the thing"}}}
	if err := cache.Save(cache.SnapshotPath(filepath.Join(dir, "cache")), snap); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"strip", "--line", "1"}, &out, &errb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 open") || !strings.Contains(out.String(), "●1 ready") {
		t.Fatalf("strip line 1 = %q", out.String())
	}
	out.Reset()
	if err := run([]string{"doctor"}, &out, &errb); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"token missing", "jira:         not configured", "windows:      none", "snapshot:     1 PRs"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("doctor missing %q:\n%s", want, out.String())
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	t.Setenv("PRD_CONFIG", filepath.Join(t.TempDir(), "none.toml"))
	if err := run([]string{"nope"}, &out, &errb); err == nil {
		t.Fatal("want error")
	}
	if err := run([]string{"version"}, &out, &errb); err != nil || !strings.Contains(out.String(), "prd") {
		t.Fatalf("version: %v %q", err, out.String())
	}
}

func TestHerdrManifest(t *testing.T) {
	var m struct {
		ID    string `toml:"id"`
		Panes []struct {
			ID, Placement string
			Command       []string
		} `toml:"panes"`
		Actions []struct {
			ID      string
			Command []string
		} `toml:"actions"`
		LinkHandlers []struct{ ID, Pattern, Action string } `toml:"link_handlers"`
		Startup      []struct{ Command []string }           `toml:"startup"`
		Events       []struct {
			On      string
			Command []string
		} `toml:"events"`
	}
	if _, err := toml.DecodeFile("../../herdr-plugin.toml", &m); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{"board": true, "recall": true, "open": true, "daemon": true, "poke": true, "herdr-open": true, "strip": true}
	check := func(cmd []string) {
		if len(cmd) < 2 || cmd[0] != "prd" || !known[cmd[1]] {
			t.Errorf("manifest command %v does not match a prd subcommand", cmd)
		}
	}
	panes := map[string]bool{}
	for _, p := range m.Panes {
		panes[p.ID] = true
		check(p.Command)
		if p.Placement != "popup" {
			t.Errorf("pane %s placement %q", p.ID, p.Placement)
		}
	}
	actions := map[string]bool{}
	for _, a := range m.Actions {
		actions[a.ID] = true
		check(a.Command)
		if a.Command[1] == "herdr-open" && !panes[a.Command[2]] {
			t.Errorf("action %s opens unknown pane %s", a.ID, a.Command[2])
		}
	}
	for _, l := range m.LinkHandlers {
		if !actions[l.Action] {
			t.Errorf("link handler %s uses unknown action %s", l.ID, l.Action)
		}
		if !regexp.MustCompile(l.Pattern).MatchString("https://github.example/acme/infra-config/pull/319") {
			t.Errorf("link pattern %q misses a PR URL", l.Pattern)
		}
	}
	for _, s := range m.Startup {
		check(s.Command)
	}
	for _, e := range m.Events {
		check(e.Command)
	}
	if m.ID != "pr-deck" || len(m.Startup) != 1 || len(m.Events) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestTutorialPrintsTheGuide(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"tutorial"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"usage:", "KEYS", "MARKS", "FLOWS", "Merge in order"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("tutorial misses %q", want)
		}
	}
}

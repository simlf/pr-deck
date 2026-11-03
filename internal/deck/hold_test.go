package deck

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/simlf/pr-deck/internal/model"
)

func TestHoldAndTopic(t *testing.T) {
	gh := scenario()
	add := func(n int, branch, body string) {
		p := gh.prs["acme/infra-config#318"]
		p.Number, p.Branch, p.Title, p.Body = n, branch, branch, body
		p.URL = fmt.Sprintf("https://github.example/acme/infra-config/pull/%d", n)
		gh.prs[p.Key()] = p
	}
	add(330, "pdb-cutover-prod", "## TLDR\n- Purpose: PDB for api.\n- Topic: prod PDB\n- **Hold-until:** 2026-10-20 19:00")
	add(331, "pdb-worker-prod", "- Purpose: PDB for workers.\n- Topic: Prod PDB\n- Hold: waiting for the vendor")
	d := newDeck(t, gh, tue1102)
	v := view(t, d, tue1102)
	dated, bare := get(t, v, "acme/infra-config#330"), get(t, v, "acme/infra-config#331")

	if v.GroupKey(dated) != "~t:prod pdb" || v.GroupKey(bare) != v.GroupKey(dated) {
		t.Fatalf("topic groups: %q %q", v.GroupKey(dated), v.GroupKey(bare))
	}
	for _, p := range []model.PR{dated, bare} {
		if s := v.StateOf(p); s != StHeld || v.Flagged(p) {
			t.Fatalf("%s: state %s flagged %v", p.Ref(), s, v.Flagged(p))
		}
	}
	for _, e := range v.Notices() {
		if strings.Contains(e.Body, "#330") || strings.Contains(e.Body, "#331") {
			t.Fatalf("held PR in a notice: %+v", e)
		}
	}
	if c := v.Count(); c.Ready != 1 {
		t.Fatalf("ready count = %d, want only eng #318", c.Ready)
	}

	if pl := v.PlanMerge(bare); pl.Kind != PlanRefuse || !strings.Contains(pl.Message, "held: waiting for the vendor") {
		t.Fatalf("bare hold plan: %+v", pl)
	}
	// 19:00 Tue is after the window, so the schedule moves to Wed 09:30.
	wed0930 := time.Date(2026, 10, 21, 9, 30, 0, 0, time.Local)
	if pl := v.PlanMerge(dated); pl.Kind != PlanSchedule || !pl.At.Equal(wed0930) {
		t.Fatalf("dated hold plan: %+v", pl)
	}
	if ok, why := v.Recheck(dated); ok || !strings.HasPrefix(why, "held until") {
		t.Fatalf("recheck while held: %v %q", ok, why)
	}

	v.Now = wed0930
	if s := v.StateOf(dated); s != StReady {
		t.Fatalf("after the hold date: %s", s)
	}
	if ok, why := v.Recheck(dated); !ok {
		t.Fatalf("recheck after the hold date: %q", why)
	}
}

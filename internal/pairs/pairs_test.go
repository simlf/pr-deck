package pairs

import (
	"testing"

	"prdeck/internal/model"
)

var envs = []string{"eng", "prod"}

func TestDetectEnv(t *testing.T) {
	if got := DetectEnv(envs, "ops-412-body-size-prod", "", nil); got != "prod" {
		t.Fatalf("branch suffix: %q", got)
	}
	if got := DetectEnv(envs, "feature", "ingress: body size (eng)", nil); got != "eng" {
		t.Fatalf("title tag: %q", got)
	}
	if got := DetectEnv(envs, "x", "y", []string{"env:prod"}); got != "prod" {
		t.Fatalf("label: %q", got)
	}
	if got := DetectEnv(envs, "production-fix", "", nil); got != "" {
		t.Fatalf("substring must not match: %q", got)
	}
}

func TestStem(t *testing.T) {
	if a, b := Stem(envs, "ops-412-body-size-eng"), Stem(envs, "ops-412-body-size-prod"); a != b {
		t.Fatalf("stems differ: %q %q", a, b)
	}
}

func TestLinkTrailer(t *testing.T) {
	prs := []model.PR{
		{Repo: "acme/infra-config", Number: 318, Env: "eng"},
		{Repo: "acme/infra-config", Number: 319, Env: "prod", Body: "Purpose: x\nMerge-after: #318"},
	}
	Link(prs, envs, nil)
	if prs[0].Pair == nil || prs[0].Pair.Order != 1 || prs[0].Pair.Inferred {
		t.Fatalf("eng pair = %+v", prs[0].Pair)
	}
	p := prs[1].Pair
	if p == nil || p.Order != 2 || p.After != "acme/infra-config#318" || p.Source != "trailer" {
		t.Fatalf("prod pair = %+v", p)
	}
	if prs[0].Pair.ID != p.ID {
		t.Fatalf("chain ids differ")
	}
}

func TestLinkHeuristic(t *testing.T) {
	prs := []model.PR{
		{Repo: "acme/k8s", Number: 78, Env: "prod", Ticket: "OPS-415", Branch: "ops-415-redis-certs-prod"},
		{Repo: "acme/k8s", Number: 77, Env: "eng", Ticket: "OPS-415", Branch: "ops-415-redis-certs-eng"},
		{Repo: "acme/k8s", Number: 80, Env: "prod", Ticket: "OPS-500", Branch: "ops-500-other-prod"},
	}
	Link(prs, envs, nil)
	if prs[1].Pair == nil || prs[1].Pair.Order != 1 || !prs[1].Pair.Inferred {
		t.Fatalf("eng = %+v", prs[1].Pair)
	}
	if prs[0].Pair == nil || prs[0].Pair.Order != 2 || prs[0].Pair.After != "acme/k8s#77" {
		t.Fatalf("prod = %+v", prs[0].Pair)
	}
	if prs[2].Pair != nil {
		t.Fatalf("lone PR must not pair: %+v", prs[2].Pair)
	}
}

func TestManualOverridesAndUnpairs(t *testing.T) {
	prs := []model.PR{
		{Repo: "a/r", Number: 1},
		{Repo: "a/r", Number: 2, Body: "Merge-after: #1"},
		{Repo: "a/r", Number: 3},
	}
	Link(prs, envs, map[string]string{"a/r#3": "a/r#2"})
	if prs[2].Pair == nil || prs[2].Pair.Order != 3 || prs[2].Pair.Source != "manual" {
		t.Fatalf("manual chain = %+v", prs[2].Pair)
	}
	Link(prs, envs, map[string]string{"a/r#2": ""})
	if prs[1].Pair != nil {
		t.Fatalf("empty manual entry should unpair, got %+v", prs[1].Pair)
	}
}

func TestReferencedMissing(t *testing.T) {
	prs := []model.PR{{Repo: "a/r", Number: 2, Body: "Merge-after: #1"}}
	got := ReferencedMissing(prs, nil)
	if len(got) != 1 || got[0] != "a/r#1" {
		t.Fatalf("missing = %v", got)
	}
}

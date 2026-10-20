package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/simlf/pr-deck/internal/model"
)

const searchResp = `{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[
 {"id":"PR_1","number":319,"title":"ABC-412 ingress: body size 25m (prod)","url":"https://github.example/acme/infra-config/pull/319",
  "body":"Purpose: Same limit for prod.\nMerge-after: #318","headRefName":"abc-412-body-size-prod","isDraft":false,"state":"OPEN",
  "mergeable":"MERGEABLE","reviewDecision":"APPROVED","createdAt":"2026-10-15T14:05:00Z","updatedAt":"2026-10-16T09:00:00Z",
  "author":{"login":"me"},"repository":{"nameWithOwner":"acme/infra-config"},
  "labels":{"nodes":[{"name":"prod-change"}]},
  "latestReviews":{"nodes":[{"author":{"login":"dana"},"state":"APPROVED","body":"lgtm","submittedAt":"2026-10-16T08:00:00Z"}]},
  "commits":{"nodes":[{"commit":{"committedDate":"2026-10-15T14:04:00Z","statusCheckRollup":{"state":"FAILURE","contexts":{"nodes":[
    {"__typename":"CheckRun","conclusion":"FAILURE","checkSuite":{"workflowRun":{"databaseId":77}}},
    {"__typename":"CheckRun","conclusion":"SUCCESS","checkSuite":{"workflowRun":{"databaseId":78}}},
    {"__typename":"StatusContext"}]}}}}]}},
 {}
]}}}`

type fakeServer struct {
	mu      sync.Mutex
	queries []string
	vars    []map[string]any
	reruns  []string
}

func (f *fakeServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing auth header")
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/repos/acme/infra-config/issues/319/comments" {
			w.Write([]byte(planComments))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/rerun-failed-jobs") {
			f.reruns = append(f.reruns, r.URL.Path)
			w.WriteHeader(201)
			return
		}
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		f.queries = append(f.queries, body.Query)
		f.vars = append(f.vars, body.Variables)
		switch {
		case strings.Contains(body.Query, "search("):
			w.Write([]byte(searchResp))
		case strings.Contains(body.Query, "mergePullRequest"):
			w.Write([]byte(`{"data":{"mergePullRequest":{"pullRequest":{"state":"MERGED"}}}}`))
		default:
			w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
		}
	})
}

func TestSearchMapsFields(t *testing.T) {
	f := &fakeServer{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	c := New(srv.Client(), srv.URL, "tok", "org:acme")
	prs, err := c.SearchOpen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 {
		t.Fatalf("want 1 PR (empty node skipped), got %d", len(prs))
	}
	p := prs[0]
	if p.Key() != "acme/infra-config#319" || p.Branch != "abc-412-body-size-prod" || !p.HasLabel("prod-change") {
		t.Fatalf("mapping: %+v", p)
	}
	if p.CI != model.CIFail || len(p.FailedRuns) != 1 || p.FailedRuns[0] != 77 {
		t.Fatalf("CI mapping: %s %v", p.CI, p.FailedRuns)
	}
	if p.ReviewDecision != model.ReviewApproved || p.Reviewer != "@dana" {
		t.Fatalf("review mapping: %s %s", p.ReviewDecision, p.Reviewer)
	}
	if q := f.vars[0]["q"].(string); !strings.Contains(q, "author:@me") || !strings.HasSuffix(q, "org:acme") {
		t.Fatalf("search query = %q", q)
	}
}

func TestMergeAndRerun(t *testing.T) {
	f := &fakeServer{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	c := New(srv.Client(), srv.URL, "tok", "")
	prs, _ := c.SearchOpen(context.Background())
	if err := c.Merge(context.Background(), prs[0], "squash"); err != nil {
		t.Fatal(err)
	}
	last := f.vars[len(f.vars)-1]
	if last["id"] != "PR_1" || last["method"] != "SQUASH" {
		t.Fatalf("merge vars = %v", last)
	}
	n, err := c.RerunFailed(context.Background(), prs[0])
	if err != nil || n != 1 || f.reruns[0] != "/repos/acme/infra-config/actions/runs/77/rerun-failed-jobs" {
		t.Fatalf("rerun: n=%d err=%v paths=%v", n, err, f.reruns)
	}
}

func TestGraphQLURL(t *testing.T) {
	if got := New(nil, "https://ghe.example/api/v3", "", "").graphqlURL(); got != "https://ghe.example/api/graphql" {
		t.Fatalf("GHE graphql url = %s", got)
	}
	if got := New(nil, "https://api.github.com/", "", "").graphqlURL(); got != "https://api.github.com/graphql" {
		t.Fatalf("graphql url = %s", got)
	}
}

const planComments = `[
 {"html_url":"u1","body":"<!-- dflook/terraform-github-actions {\"workspace\":\"eng\"} -->\nTerraform plan in __infra__ in the __eng__ workspace\n<details open>\n<summary>Plan: 1 to add, 0 to change, 0 to destroy.</summary>\n\n` + "```hcl\\n+ x\\n```" + `\n</details>"},
 {"html_url":"u2","body":"<!-- dflook/terraform-github-actions {\"workspace\":\"prod\"} -->\n<details>\n<summary>No changes. Your infrastructure matches the configuration.</summary>\n</details>"},
 {"html_url":"u3","body":"looks good"},
 {"html_url":"u4","body":"Terraform plan in __modules/vpc__\n<details>\n<summary><strike>Plan: 0 to add, 1 to change, 0 to destroy.</strike></summary>\n</details>"},
 {"html_url":"u5","body":"Terraform plan in __modules/dns__\n<details>\n<summary>Plan generated.</summary>\n</details>"},
 {"html_url":"u6","body":"<!-- dflook/terraform-github-actions {\"workspace\":\"eng\"} -->\n<details>\n<summary>Plan: 0 to add, 2 to change, 3 to destroy.</summary>\n</details>"}
]`

func TestPlansKeepChangedWorkspaces(t *testing.T) {
	f := &fakeServer{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	c := New(srv.Client(), srv.URL, "tok", "")
	pls, err := c.Plans(context.Background(), model.PR{Repo: "acme/infra-config", Number: 319})
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Plan{
		{Workspace: "eng", Summary: "Plan: 0 to add, 2 to change, 3 to destroy.", Change: 2, Destroy: 3, URL: "u6"},
		{Workspace: "modules/dns", Summary: "Plan generated.", URL: "u5"},
	}
	if len(pls) != len(want) {
		t.Fatalf("plans = %+v", pls)
	}
	for i := range want {
		if !reflect.DeepEqual(pls[i], want[i]) {
			t.Errorf("plan %d = %+v, want %+v", i, pls[i], want[i])
		}
	}
}

func TestParsePlanResources(t *testing.T) {
	body, err := os.ReadFile("testdata/plan-production-par.md")
	if err != nil {
		t.Fatal(err)
	}
	pl, ok := parsePlan(string(body), "u")
	if !ok || pl.Workspace != "production-par" || pl.Add != 2 || pl.Destroy != 2 {
		t.Fatalf("plan = %+v", pl)
	}
	want := []model.PlanResource{
		{Action: "-/+", Address: `module.redis["cache"].ovh_cloud_project_database.this`},
		{Action: "+", Address: `ovh_cloud_project_database.opensearch_clusters["main"]`},
		{Action: "~", Address: "ovh_cloud_project_database_user.app"},
		{Action: "-", Address: "ovh_cloud_project_kube_nodepool.old"},
		{Action: "-", Address: `ovh_domain_zone_record.lb["api"]`},
		{Action: "-/+", Address: "ovh_cloud_project_database_ip_restrictions.this"},
		{Action: "+", Address: "ovh_vrack_cloudproject.vrack"},
	}
	if !reflect.DeepEqual(pl.Resources, want) {
		t.Fatalf("resources =\n%+v\nwant\n%+v", pl.Resources, want)
	}
}

func TestCommentsKeepPeopleOtherThanTheAuthor(t *testing.T) {
	var g gqlPR
	err := json.Unmarshal([]byte(`{"author":{"login":"me"},
	 "comments":{"nodes":[
	  {"author":{"__typename":"User","login":"dana"},"body":"why 25m?\nmore","createdAt":"2026-10-16T10:00:00Z","url":"c1"},
	  {"author":{"__typename":"Bot","login":"github-actions"},"body":"plan","createdAt":"2026-10-16T11:00:00Z","url":"c2"},
	  {"author":{"__typename":"User","login":"me"},"body":"done","createdAt":"2026-10-16T12:00:00Z","url":"c3"}]},
	 "reviews":{"nodes":[
	  {"author":{"__typename":"User","login":"lee"},"state":"CHANGES_REQUESTED","body":"","submittedAt":"2026-10-16T13:00:00Z","url":"r1",
	   "comments":{"nodes":[{"author":{"__typename":"User","login":"lee"},"body":"lower burst","path":"main.tf","line":0,"originalLine":12,"createdAt":"2026-10-16T13:00:01Z","url":"r1c"}]}},
	  {"author":{"__typename":"User","login":"kim"},"state":"APPROVED","body":"lgtm","submittedAt":"2026-10-16T14:00:00Z","url":"r2","comments":{"nodes":[]}},
	  {"author":{"__typename":"User","login":"kim"},"state":"COMMENTED","body":"","submittedAt":"2026-10-16T15:00:00Z","url":"r3","comments":{"nodes":[]}}]}}`), &g)
	if err != nil {
		t.Fatal(err)
	}
	got := g.toModel().Comments
	var urls []string
	for _, c := range got {
		urls = append(urls, c.URL)
	}
	if strings.Join(urls, " ") != "r1c r1 c1" {
		t.Fatalf("comments = %v", urls)
	}
	if got[0].Path != "main.tf" || got[0].Line != 12 || got[1].Body != "requested changes" || got[2].Body != "why 25m?" {
		t.Fatalf("comments = %+v", got)
	}
}

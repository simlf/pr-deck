package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

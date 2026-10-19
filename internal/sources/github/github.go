// Package github reads PR state from the GitHub GraphQL API and performs the
// few writes pr-deck allows: merge and rerun of failed CI jobs.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/simlf/pr-deck/internal/model"
)

// API is what the rest of pr-deck needs from GitHub. Tests use fakes.
type API interface {
	SearchOpen(ctx context.Context) ([]model.PR, error)
	Get(ctx context.Context, repo string, number int) (model.PR, error)
	Merge(ctx context.Context, p model.PR, method string) error
	RerunFailed(ctx context.Context, p model.PR) (int, error)
}

// Client talks to api.github.com or a GitHub Enterprise server.
type Client struct {
	HTTP   *http.Client
	APIURL string // REST base, e.g. https://api.github.com
	Token  string
	Query  string // extra search qualifiers

	mu      sync.Mutex
	nodeIDs map[string]string
}

func (c *Client) setID(key, id string) {
	c.mu.Lock()
	c.nodeIDs[key] = id
	c.mu.Unlock()
}

func (c *Client) id(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nodeIDs[key]
}

// New builds a client. apiURL is the REST base.
func New(httpc *http.Client, apiURL, token, query string) *Client {
	return &Client{HTTP: httpc, APIURL: strings.TrimRight(apiURL, "/"), Token: token, Query: query, nodeIDs: map[string]string{}}
}

func (c *Client) graphqlURL() string {
	if strings.HasSuffix(c.APIURL, "/v3") {
		return strings.TrimSuffix(c.APIURL, "/v3") + "/graphql"
	}
	return c.APIURL + "/graphql"
}

const prFields = `
fragment pr on PullRequest {
  id number title url body headRefName isDraft state mergeable reviewDecision createdAt updatedAt
  author { login }
  repository { nameWithOwner }
  labels(first: 20) { nodes { name } }
  latestReviews(first: 10) { nodes { author { login } state body submittedAt } }
  commits(last: 1) { nodes { commit { committedDate statusCheckRollup { state
    contexts(first: 50) { nodes { __typename
      ... on CheckRun { conclusion checkSuite { workflowRun { databaseId } } } } } } } } }
}`

const searchQuery = `query($q: String!, $after: String) {
  search(query: $q, type: ISSUE, first: 50, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { ... on PullRequest { ...pr } }
  }
}` + prFields

const getQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) { pullRequest(number: $number) { ...pr } }
}` + prFields

const mergeMutation = `mutation($id: ID!, $method: PullRequestMergeMethod!) {
  mergePullRequest(input: {pullRequestId: $id, mergeMethod: $method}) { pullRequest { state } }
}`

type gqlPR struct {
	ID             string                                  `json:"id"`
	Number         int                                     `json:"number"`
	Title          string                                  `json:"title"`
	URL            string                                  `json:"url"`
	Body           string                                  `json:"body"`
	HeadRefName    string                                  `json:"headRefName"`
	IsDraft        bool                                    `json:"isDraft"`
	State          string                                  `json:"state"`
	Mergeable      string                                  `json:"mergeable"`
	ReviewDecision string                                  `json:"reviewDecision"`
	CreatedAt      time.Time                               `json:"createdAt"`
	UpdatedAt      time.Time                               `json:"updatedAt"`
	Author         *struct{ Login string }                 `json:"author"`
	Repository     struct{ NameWithOwner string }          `json:"repository"`
	Labels         struct{ Nodes []struct{ Name string } } `json:"labels"`
	LatestReviews  struct {
		Nodes []struct {
			Author      *struct{ Login string } `json:"author"`
			State       string                  `json:"state"`
			Body        string                  `json:"body"`
			SubmittedAt time.Time               `json:"submittedAt"`
		}
	} `json:"latestReviews"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				CommittedDate     time.Time `json:"committedDate"`
				StatusCheckRollup *struct {
					State    string `json:"state"`
					Contexts struct {
						Nodes []struct {
							Typename   string `json:"__typename"`
							Conclusion string `json:"conclusion"`
							CheckSuite *struct {
								WorkflowRun *struct{ DatabaseID int64 } `json:"workflowRun"`
							} `json:"checkSuite"`
						}
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		}
	} `json:"commits"`
}

func (g gqlPR) toModel() model.PR {
	p := model.PR{
		Repo: g.Repository.NameWithOwner, Number: g.Number, Title: g.Title, URL: g.URL, Body: g.Body,
		Branch: g.HeadRefName, Draft: g.IsDraft, State: g.State, Mergeable: g.Mergeable,
		ReviewDecision: g.ReviewDecision, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt, CI: model.CINone,
	}
	if g.Author != nil {
		p.Author = g.Author.Login
	}
	for _, l := range g.Labels.Nodes {
		p.Labels = append(p.Labels, l.Name)
	}
	want := map[string]string{model.ReviewApproved: "APPROVED", model.ReviewChanges: "CHANGES_REQUESTED"}[g.ReviewDecision]
	reviews := g.LatestReviews.Nodes
	sort.Slice(reviews, func(i, j int) bool { return reviews[i].SubmittedAt.After(reviews[j].SubmittedAt) })
	for _, r := range reviews {
		if want != "" && r.State == want && r.Author != nil {
			p.Reviewer = "@" + r.Author.Login
			p.ReviewNote = firstLine(r.Body, 80)
			break
		}
	}
	if n := len(g.Commits.Nodes); n > 0 {
		c := g.Commits.Nodes[n-1].Commit
		p.LastCommitAt = c.CommittedDate
		if c.StatusCheckRollup != nil {
			switch c.StatusCheckRollup.State {
			case "SUCCESS":
				p.CI = model.CIPass
			case "FAILURE", "ERROR":
				p.CI = model.CIFail
			case "PENDING", "EXPECTED":
				p.CI = model.CIRunning
			}
			seen := map[int64]bool{}
			for _, ctx := range c.StatusCheckRollup.Contexts.Nodes {
				if ctx.Typename != "CheckRun" || ctx.CheckSuite == nil || ctx.CheckSuite.WorkflowRun == nil {
					continue
				}
				switch ctx.Conclusion {
				case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "CANCELLED":
					id := ctx.CheckSuite.WorkflowRun.DatabaseID
					if !seen[id] {
						seen[id] = true
						p.FailedRuns = append(p.FailedRuns, id)
					}
				}
			}
		}
	}
	return p
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

func (c *Client) do(ctx context.Context, method, url string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("github %s %s: HTTP %d: %s", method, url, resp.StatusCode, firstLine(string(b), 200))
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

func (c *Client) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.do(ctx, http.MethodPost, c.graphqlURL(), map[string]any{"query": query, "variables": vars}, &env); err != nil {
		return err
	}
	if len(env.Errors) > 0 {
		return fmt.Errorf("github graphql: %s", env.Errors[0].Message)
	}
	return json.Unmarshal(env.Data, out)
}

// SearchOpen lists open PRs authored by the token's user.
func (c *Client) SearchOpen(ctx context.Context) ([]model.PR, error) {
	q := strings.TrimSpace("is:pr is:open author:@me archived:false " + c.Query)
	var out []model.PR
	var after *string
	for page := 0; page < 10; page++ {
		var data struct {
			Search struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []gqlPR `json:"nodes"`
			} `json:"search"`
		}
		vars := map[string]any{"q": q}
		if after != nil {
			vars["after"] = *after
		}
		if err := c.graphql(ctx, searchQuery, vars, &data); err != nil {
			return nil, err
		}
		for _, n := range data.Search.Nodes {
			if n.Number == 0 {
				continue
			}
			p := n.toModel()
			c.setID(p.Key(), n.ID)
			out = append(out, p)
		}
		if !data.Search.PageInfo.HasNextPage {
			break
		}
		cur := data.Search.PageInfo.EndCursor
		after = &cur
	}
	return out, nil
}

// Get fetches one PR in any state (used for merged siblings).
func (c *Client) Get(ctx context.Context, repo string, number int) (model.PR, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return model.PR{}, fmt.Errorf("bad repo %q", repo)
	}
	var data struct {
		Repository struct {
			PullRequest *gqlPR `json:"pullRequest"`
		} `json:"repository"`
	}
	if err := c.graphql(ctx, getQuery, map[string]any{"owner": owner, "name": name, "number": number}, &data); err != nil {
		return model.PR{}, err
	}
	if data.Repository.PullRequest == nil {
		return model.PR{}, fmt.Errorf("%s#%d not found", repo, number)
	}
	p := data.Repository.PullRequest.toModel()
	c.setID(p.Key(), data.Repository.PullRequest.ID)
	return p, nil
}

// Merge merges p with method (squash | merge | rebase).
func (c *Client) Merge(ctx context.Context, p model.PR, method string) error {
	id := c.id(p.Key())
	if id == "" {
		fresh, err := c.Get(ctx, p.Repo, p.Number)
		if err != nil {
			return err
		}
		id = c.id(fresh.Key())
	}
	var out json.RawMessage
	return c.graphql(ctx, mergeMutation, map[string]any{"id": id, "method": strings.ToUpper(method)}, &out)
}

// RerunFailed reruns the failed jobs of every failed workflow run on p's
// head commit and returns how many runs were restarted.
func (c *Client) RerunFailed(ctx context.Context, p model.PR) (int, error) {
	n := 0
	for _, id := range p.FailedRuns {
		url := fmt.Sprintf("%s/repos/%s/actions/runs/%d/rerun-failed-jobs", c.APIURL, p.Repo, id)
		if err := c.do(ctx, http.MethodPost, url, nil, nil); err != nil {
			return n, err
		}
		n++
	}
	if n == 0 {
		return 0, fmt.Errorf("no failed GitHub Actions runs on %s", p.Ref())
	}
	return n, nil
}

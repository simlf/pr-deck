// Package model holds the data pr-deck shows: pull requests enriched with
// ticket topics, pairing, review-channel status and the "why".
package model

import (
	"fmt"
	"time"
)

// CI rollup states.
const (
	CIPass    = "pass"
	CIFail    = "fail"
	CIRunning = "running"
	CINone    = "none"
)

// GitHub review decisions.
const (
	ReviewApproved = "APPROVED"
	ReviewChanges  = "CHANGES_REQUESTED"
	ReviewRequired = "REVIEW_REQUIRED"
)

// PR is one pull request as pr-deck sees it.
type PR struct {
	Repo   string `json:"repo"` // owner/name
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Body   string `json:"body"`
	Branch string `json:"branch"`
	Author string `json:"author"`

	State     string `json:"state"` // OPEN | MERGED | CLOSED
	Draft     bool   `json:"draft"`
	CI        string `json:"ci"`
	Mergeable string `json:"mergeable"` // MERGEABLE | CONFLICTING | UNKNOWN

	// FailedRuns are workflow run ids with failed jobs, for rerun.
	FailedRuns []int64 `json:"failed_runs,omitempty"`

	ReviewDecision string   `json:"review_decision"`
	Reviewer       string   `json:"reviewer,omitempty"`
	ReviewNote     string   `json:"review_note,omitempty"`
	Labels         []string `json:"labels,omitempty"`

	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	LastCommitAt time.Time `json:"last_commit_at"`

	// Enrichment.
	Ticket       string     `json:"ticket,omitempty"`
	Topic        string     `json:"topic,omitempty"`
	TicketStatus string     `json:"ticket_status,omitempty"`
	TicketURL    string     `json:"ticket_url,omitempty"`
	Env          string     `json:"env,omitempty"`
	Purpose      string     `json:"purpose,omitempty"`
	Why          *Why       `json:"why,omitempty"`
	Pair         *PairRef   `json:"pair,omitempty"`
	Posted       *time.Time `json:"posted,omitempty"`
	PostedKnown  bool       `json:"posted_known"`
	// When the slow sources were last asked, so refreshes stay within
	// Jira and Slack rate limits.
	TicketFetched time.Time `json:"ticket_fetched,omitempty"`
	PostedChecked time.Time `json:"posted_checked,omitempty"`
	// Sibling marks a PR fetched only because an open PR pairs with it.
	Sibling bool `json:"sibling,omitempty"`
}

// Key identifies a PR across repos: owner/repo#123.
func (p PR) Key() string { return Key(p.Repo, p.Number) }

// Key builds a PR key.
func Key(repo string, n int) string { return fmt.Sprintf("%s#%d", repo, n) }

// RepoName is the repo without its owner.
func (p PR) RepoName() string {
	for i := len(p.Repo) - 1; i >= 0; i-- {
		if p.Repo[i] == '/' {
			return p.Repo[i+1:]
		}
	}
	return p.Repo
}

// Ref is the short display reference: repo#123.
func (p PR) Ref() string { return fmt.Sprintf("%s#%d", p.RepoName(), p.Number) }

// Merged reports whether the PR is merged.
func (p PR) Merged() bool { return p.State == "MERGED" }

// HasLabel reports whether the PR carries label l.
func (p PR) HasLabel(l string) bool {
	for _, x := range p.Labels {
		if x == l {
			return true
		}
	}
	return false
}

// Why records the agent prompt that created a PR.
type Why struct {
	Prompt    string    `json:"prompt"`
	SessionID string    `json:"session_id,omitempty"`
	Cwd       string    `json:"cwd,omitempty"`
	At        time.Time `json:"at"`
}

// PairRef places a PR in an ordered eng → prod chain.
type PairRef struct {
	ID       string `json:"id"`              // stable chain id
	Order    int    `json:"order"`           // 1-based merge order
	After    string `json:"after,omitempty"` // key of the PR that must merge first
	Inferred bool   `json:"inferred,omitempty"`
	Source   string `json:"source"` // trailer | heuristic | manual
}

// Snapshot is what the daemon caches and every view renders.
type Snapshot struct {
	At     time.Time `json:"at"`
	PRs    []PR      `json:"prs"`
	Errors []string  `json:"errors,omitempty"`
}

// Find returns the PR with key, if present.
func (s Snapshot) Find(key string) (PR, bool) {
	for _, p := range s.PRs {
		if p.Key() == key {
			return p, true
		}
	}
	return PR{}, false
}

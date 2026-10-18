// Package jira fetches ticket summaries and statuses so pr-deck can label
// groups by topic instead of by key.
package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Ticket is the part of a Jira issue pr-deck shows.
type Ticket struct {
	Key     string
	Summary string
	Status  string
}

// API looks tickets up. Tests use fakes.
type API interface {
	Get(ctx context.Context, key string) (Ticket, error)
	BrowseURL(key string) string
}

// Client is a Jira Cloud/Server REST client using basic auth (email + API
// token) or a bearer token when email is empty.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Email   string
	Token   string
}

// BrowseURL is the ticket's web page.
func (c *Client) BrowseURL(key string) string {
	if c.BaseURL == "" {
		return ""
	}
	return strings.TrimRight(c.BaseURL, "/") + "/browse/" + url.PathEscape(key)
}

// Get returns the ticket's summary and status.
func (c *Client) Get(ctx context.Context, key string) (Ticket, error) {
	if c.BaseURL == "" {
		return Ticket{}, fmt.Errorf("jira.base_url not configured")
	}
	u := strings.TrimRight(c.BaseURL, "/") + "/rest/api/2/issue/" + url.PathEscape(key) + "?fields=summary,status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Ticket{}, err
	}
	switch {
	case c.Email != "" && c.Token != "":
		req.SetBasicAuth(c.Email, c.Token)
	case c.Token != "":
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Ticket{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Ticket{}, fmt.Errorf("jira %s: HTTP %d", key, resp.StatusCode)
	}
	var body struct {
		Key    string `json:"key"`
		Fields struct {
			Summary string `json:"summary"`
			Status  struct {
				Name string `json:"name"`
			} `json:"status"`
		} `json:"fields"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Ticket{}, err
	}
	return Ticket{Key: key, Summary: strings.TrimSpace(body.Fields.Summary), Status: body.Fields.Status.Name}, nil
}

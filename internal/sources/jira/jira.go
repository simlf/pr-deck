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
	var body struct {
		Key    string `json:"key"`
		Fields struct {
			Summary string `json:"summary"`
			Status  struct {
				Name string `json:"name"`
			} `json:"status"`
		} `json:"fields"`
	}
	if err := c.get(ctx, "/rest/api/2/issue/"+url.PathEscape(key)+"?fields=summary,status", &body); err != nil {
		return Ticket{}, fmt.Errorf("jira %s: %w", key, err)
	}
	return Ticket{Key: key, Summary: strings.TrimSpace(body.Fields.Summary), Status: body.Fields.Status.Name}, nil
}

// Me returns the display name the credentials log in as. Jira Cloud answers
// a rejected token with 404 on issues (it treats the call as anonymous), so
// this is the check that tells a bad token from a missing ticket.
func (c *Client) Me(ctx context.Context) (string, error) {
	var body struct {
		DisplayName string `json:"displayName"`
	}
	if err := c.get(ctx, "/rest/api/2/myself", &body); err != nil {
		return "", fmt.Errorf("jira: %w", err)
	}
	return body.DisplayName, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	if c.BaseURL == "" {
		return fmt.Errorf("jira.base_url not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+path, nil)
	if err != nil {
		return err
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
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

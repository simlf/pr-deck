// Package slack answers when a PR was first posted in the team review
// channel, and posts a review request there when the user confirms one.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// API finds the first post of a PR link in a channel and posts to it.
// Tests use fakes.
type API interface {
	FirstPost(ctx context.Context, channel, prURL string) (*time.Time, error)
	Post(ctx context.Context, channel, text string) error
}

// Client uses Slack's search.messages and chat.postMessage, which need a
// user token with the search:read and chat:write scopes.
type Client struct {
	HTTP   *http.Client
	APIURL string // https://slack.com/api
	Token  string
}

// FirstPost returns the earliest message in channel containing prURL, or
// nil when it was never posted.
func (c *Client) FirstPost(ctx context.Context, channel, prURL string) (*time.Time, error) {
	if c.Token == "" {
		return nil, fmt.Errorf("slack token not configured")
	}
	ch := strings.TrimPrefix(channel, "#")
	q := url.Values{}
	q.Set("query", fmt.Sprintf("%q in:#%s", prURL, ch))
	q.Set("sort", "timestamp")
	q.Set("sort_dir", "asc")
	q.Set("count", "1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.APIURL, "/")+"/search.messages?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		OK       bool   `json:"ok"`
		Error    string `json:"error"`
		Messages struct {
			Matches []struct {
				TS string `json:"ts"`
			} `json:"matches"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if !body.OK {
		return nil, fmt.Errorf("slack search: %s", body.Error)
	}
	if len(body.Messages.Matches) == 0 {
		return nil, nil
	}
	t, err := parseTS(body.Messages.Matches[0].TS)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func parseTS(ts string) (time.Time, error) {
	sec, frac, _ := strings.Cut(ts, ".")
	s, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("slack ts %q: %w", ts, err)
	}
	var ns int64
	if frac != "" {
		for len(frac) < 9 {
			frac += "0"
		}
		ns, _ = strconv.ParseInt(frac[:9], 10, 64)
	}
	return time.Unix(s, ns).UTC(), nil
}

// Post sends text to channel as the token's user.
func (c *Client) Post(ctx context.Context, channel, text string) error {
	if c.Token == "" {
		return fmt.Errorf("slack token not configured")
	}
	b, err := json.Marshal(map[string]string{"channel": strings.TrimPrefix(channel, "#"), "text": text})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.APIURL, "/")+"/chat.postMessage", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var body struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Needed string `json:"needed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	switch {
	case body.OK:
		return nil
	case body.Error == "missing_scope":
		return fmt.Errorf("slack token lacks the %s scope: add it to the app's user token scopes and reinstall the app", nonEmpty(body.Needed, "chat:write"))
	default:
		return fmt.Errorf("slack post: %s", body.Error)
	}
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// AuthTest checks the token and returns its user, team and granted scopes.
func (c *Client) AuthTest(ctx context.Context) (user, team, scopes string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.APIURL, "/")+"/auth.test", nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		User  string `json:"user"`
		Team  string `json:"team"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", "", err
	}
	if !body.OK {
		return "", "", "", fmt.Errorf("slack auth.test: %s", body.Error)
	}
	return body.User, body.Team, resp.Header.Get("X-OAuth-Scopes"), nil
}

// Package slack answers one read-only question: when was this PR first
// posted in the team review channel? It never posts anything.
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// API finds the first post of a PR link in a channel. Tests use fakes.
type API interface {
	FirstPost(ctx context.Context, channel, prURL string) (*time.Time, error)
}

// Client uses Slack's search.messages, which needs a user token with the
// search:read scope.
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

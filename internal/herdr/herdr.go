// Package herdr is a minimal client for herdr's socket API (protocol 22),
// covering only what pr-deck uses: listing agents, reporting display-only
// pane/workspace metadata tokens, and showing toast notifications.
//
// Requests and responses are single JSON objects, one per line:
//
//	{"id":"1","method":"agent.list","params":{}}
//	{"id":"1","result":{"type":"agent_list","agents":[...]}}
package herdr

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

// Agent is the subset of herdr's AgentInfo pr-deck needs.
type Agent struct {
	PaneID        string `json:"pane_id"`
	WorkspaceID   string `json:"workspace_id"`
	Cwd           string `json:"cwd"`
	ForegroundCwd string `json:"foreground_cwd"`
	Agent         string `json:"agent"`
	Status        string `json:"agent_status"`
	Session       *struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"agent_session"`
}

// SessionID is the agent's session identity when herdr reports one by id.
func (a Agent) SessionID() string {
	if a.Session != nil && a.Session.Kind == "id" {
		return a.Session.Value
	}
	return ""
}

// Dir is the agent's working directory.
func (a Agent) Dir() string {
	if a.ForegroundCwd != "" {
		return a.ForegroundCwd
	}
	return a.Cwd
}

// Client sends one request per connection, which keeps it robust across
// herdr server restarts.
type Client struct {
	Socket  string
	Timeout time.Duration
	mu      sync.Mutex
	seq     int
}

// Error is an error response from herdr.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return "herdr: " + e.Code + ": " + e.Message }

func (c *Client) call(method string, params any, out any) error {
	if c.Socket == "" {
		return fmt.Errorf("herdr: no socket (set HERDR_SOCKET_PATH or herdr.socket_path)")
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	conn, err := net.DialTimeout("unix", c.Socket, timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	c.mu.Lock()
	c.seq++
	id := "prd-" + strconv.Itoa(c.seq)
	c.mu.Unlock()
	req, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var resp struct {
			ID     string          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *Error          `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &resp) != nil || resp.ID != id {
			continue // events or unrelated lines
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return fmt.Errorf("herdr: connection closed without a response to %s", method)
}

// Agents lists agent panes.
func (c *Client) Agents() ([]Agent, error) {
	var res struct {
		Agents []Agent `json:"agents"`
	}
	if err := c.call("agent.list", map[string]any{}, &res); err != nil {
		return nil, err
	}
	return res.Agents, nil
}

// PaneTokens reports display-only tokens on a pane. A nil value clears a
// token. ttl makes stale tokens vanish if pr-deck stops refreshing them.
func (c *Client) PaneTokens(paneID, source string, tokens map[string]*string, ttl time.Duration) error {
	return c.call("pane.report_metadata", map[string]any{
		"pane_id": paneID, "source": source, "tokens": tokens, "ttl_ms": ttlMS(ttl),
	}, nil)
}

// WorkspaceTokens reports display-only tokens on a workspace.
func (c *Client) WorkspaceTokens(workspaceID, source string, tokens map[string]*string, ttl time.Duration) error {
	return c.call("workspace.report_metadata", map[string]any{
		"workspace_id": workspaceID, "source": source, "tokens": tokens, "ttl_ms": ttlMS(ttl),
	}, nil)
}

// Notify shows a toast.
func (c *Client) Notify(title, body, position string) error {
	p := map[string]any{"title": title, "body": body, "sound": "none"}
	if position != "" {
		p["position"] = position
	}
	return c.call("notification.show", p, nil)
}

func ttlMS(d time.Duration) any {
	if d <= 0 {
		return nil
	}
	ms := d.Milliseconds()
	if ms > 86400000 {
		ms = 86400000
	}
	return ms
}

// OpenPane opens one of a plugin's manifest panes (popup, overlay, ...)
// with extra environment, e.g. the PR to focus.
func (c *Client) OpenPane(pluginID, entrypoint string, env map[string]string) error {
	p := map[string]any{"plugin_id": pluginID, "entrypoint": entrypoint, "focus": true}
	if len(env) > 0 {
		p["env"] = env
	}
	return c.call("plugin.pane.open", p, nil)
}

// Str is a helper for token values.
func Str(s string) *string { return &s }

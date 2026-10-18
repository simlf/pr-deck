package herdr

import (
	"bufio"
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeHerdr is a unix-socket server speaking herdr's line protocol.
type fakeHerdr struct {
	mu   sync.Mutex
	reqs []map[string]any
}

func serve(t *testing.T, f *fakeHerdr) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "h.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				sc := bufio.NewScanner(conn)
				for sc.Scan() {
					var req map[string]any
					json.Unmarshal(sc.Bytes(), &req)
					f.mu.Lock()
					f.reqs = append(f.reqs, req)
					f.mu.Unlock()
					id := req["id"]
					// An unrelated event line first, as a subscription would send.
					conn.Write([]byte(`{"event":"noise"}` + "\n"))
					var resp any
					switch req["method"] {
					case "agent.list":
						resp = map[string]any{"id": id, "result": map[string]any{"type": "agent_list", "agents": []any{
							map[string]any{"pane_id": "w1:p2", "workspace_id": "w1", "cwd": "/w/infra", "agent_status": "idle",
								"agent_session": map[string]any{"kind": "id", "value": "S1", "agent": "claude", "source": "x"}},
						}}}
					case "notification.show":
						resp = map[string]any{"id": id, "error": map[string]any{"code": "bad_params", "message": "nope"}}
					default:
						resp = map[string]any{"id": id, "result": map[string]any{"type": "ok"}}
					}
					b, _ := json.Marshal(resp)
					conn.Write(append(b, '\n'))
				}
			}(conn)
		}
	}()
	return sock
}

func TestAgentsTokensNotify(t *testing.T) {
	f := &fakeHerdr{}
	c := &Client{Socket: serve(t, f), Timeout: 2 * time.Second}
	agents, err := c.Agents()
	if err != nil || len(agents) != 1 || agents[0].SessionID() != "S1" || agents[0].Dir() != "/w/infra" {
		t.Fatalf("agents = %+v, %v", agents, err)
	}
	if err := c.PaneTokens("w1:p2", "custom:pr-deck", map[string]*string{"pr": Str("①#318●"), "old": nil}, time.Minute); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	last := f.reqs[len(f.reqs)-1]
	f.mu.Unlock()
	params := last["params"].(map[string]any)
	if last["method"] != "pane.report_metadata" || params["pane_id"] != "w1:p2" || params["ttl_ms"].(float64) != 60000 {
		t.Fatalf("report_metadata request = %v", last)
	}
	toks := params["tokens"].(map[string]any)
	if toks["pr"] != "①#318●" || toks["old"] != nil {
		t.Fatalf("tokens = %v", toks)
	}
	err = c.Notify("t", "b", "top-right")
	if e, ok := err.(*Error); !ok || e.Code != "bad_params" {
		t.Fatalf("error response should surface, got %v", err)
	}
}

func TestNoSocket(t *testing.T) {
	if _, err := (&Client{}).Agents(); err == nil {
		t.Fatal("want error without socket")
	}
}

func TestOpenPane(t *testing.T) {
	f := &fakeHerdr{}
	c := &Client{Socket: serve(t, f), Timeout: 2 * time.Second}
	if err := c.OpenPane("pr-deck", "board", map[string]string{"PRD_FOCUS": "https://x/pull/1"}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	last := f.reqs[len(f.reqs)-1]
	f.mu.Unlock()
	p := last["params"].(map[string]any)
	if last["method"] != "plugin.pane.open" || p["plugin_id"] != "pr-deck" || p["entrypoint"] != "board" {
		t.Fatalf("open pane request = %v", last)
	}
	if p["env"].(map[string]any)["PRD_FOCUS"] != "https://x/pull/1" {
		t.Fatalf("env not passed: %v", p)
	}
}

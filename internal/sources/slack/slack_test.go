package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFirstPost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if !strings.Contains(q, "in:#team-reviews") || !strings.Contains(q, "/pull/88") {
			t.Errorf("query = %q", q)
		}
		if strings.Contains(q, "/pull/203") {
			w.Write([]byte(`{"ok":true,"messages":{"matches":[]}}`))
			return
		}
		w.Write([]byte(`{"ok":true,"messages":{"matches":[{"ts":"1792000000.000100"}]}}`))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), APIURL: srv.URL, Token: "xoxp-test"}
	at, err := c.FirstPost(context.Background(), "#team-reviews", "https://github.example/acme/runbooks/pull/88")
	if err != nil || at == nil || !at.Equal(time.Unix(1792000000, 100000).UTC()) {
		t.Fatalf("FirstPost = %v, %v", at, err)
	}
}

func TestNotPostedAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "bad") {
			w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
			return
		}
		w.Write([]byte(`{"ok":true,"messages":{"matches":[]}}`))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), APIURL: srv.URL, Token: "x"}
	at, err := c.FirstPost(context.Background(), "team-reviews", "https://x/pull/88")
	if err != nil || at != nil {
		t.Fatalf("not posted: %v %v", at, err)
	}
	if _, err := c.FirstPost(context.Background(), "team-reviews", "bad"); err == nil {
		t.Fatal("want slack error")
	}
	if _, err := (&Client{}).FirstPost(context.Background(), "c", "u"); err == nil {
		t.Fatal("want missing-token error")
	}
}

func TestPostAndMissingScope(t *testing.T) {
	var got map[string]string
	scope := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if !scope {
			w.Write([]byte(`{"ok":false,"error":"missing_scope","needed":"chat:write","provided":"search:read"}`))
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), APIURL: srv.URL, Token: "x"}
	if err := c.Post(context.Background(), "#team-reviews", "Review please"); err != nil || got["channel"] != "team-reviews" || got["text"] != "Review please" {
		t.Fatalf("post: %v %v", got, err)
	}
	scope = false
	if err := c.Post(context.Background(), "#team-reviews", "x"); err == nil || !strings.Contains(err.Error(), "lacks the chat:write scope") {
		t.Fatalf("missing scope must say so: %v", err)
	}
}

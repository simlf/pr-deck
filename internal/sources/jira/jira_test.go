package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "me@example.test" || p != "tok" {
			t.Errorf("basic auth = %q %q %v", u, p, ok)
		}
		if r.URL.Path != "/rest/api/2/issue/ABC-412" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"key":"ABC-412","fields":{"summary":"Allow bigger uploads (ingress 25 MB)","status":{"name":"In Progress"}}}`))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, Email: "me@example.test", Token: "tok"}
	tk, err := c.Get(context.Background(), "ABC-412")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Summary != "Allow bigger uploads (ingress 25 MB)" || tk.Status != "In Progress" {
		t.Fatalf("ticket = %+v", tk)
	}
	if got := c.BrowseURL("ABC-412"); got != srv.URL+"/browse/ABC-412" {
		t.Fatalf("browse = %s", got)
	}
}

func TestMe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, p, _ := r.BasicAuth(); p != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"displayName":"Ada Lovelace"}`))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, Email: "me@example.test", Token: "tok"}
	if name, err := c.Me(context.Background()); err != nil || name != "Ada Lovelace" {
		t.Fatalf("Me = %q, %v", name, err)
	}
	c.Token = "bad"
	if _, err := c.Me(context.Background()); err == nil || err.Error() != "jira: HTTP 401" {
		t.Fatalf("bad token err = %v", err)
	}
}

func TestGetUnconfigured(t *testing.T) {
	if _, err := (&Client{}).Get(context.Background(), "ABC-1"); err == nil {
		t.Fatal("want error without base url")
	}
}

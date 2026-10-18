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
		if r.URL.Path != "/rest/api/2/issue/OPS-412" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"key":"OPS-412","fields":{"summary":"Allow bigger uploads (ingress 25 MB)","status":{"name":"In Progress"}}}`))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, Email: "me@example.test", Token: "tok"}
	tk, err := c.Get(context.Background(), "OPS-412")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Summary != "Allow bigger uploads (ingress 25 MB)" || tk.Status != "In Progress" {
		t.Fatalf("ticket = %+v", tk)
	}
	if got := c.BrowseURL("OPS-412"); got != srv.URL+"/browse/OPS-412" {
		t.Fatalf("browse = %s", got)
	}
}

func TestGetUnconfigured(t *testing.T) {
	if _, err := (&Client{}).Get(context.Background(), "OPS-1"); err == nil {
		t.Fatal("want error without base url")
	}
}

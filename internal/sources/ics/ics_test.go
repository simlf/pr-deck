package ics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const feed = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" +
	"BEGIN:VEVENT\r\nSUMMARY:Team holiday\r\nDTSTART;VALUE=DATE:20261111\r\nDTEND;VALUE=DATE:20261112\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nSUMMARY:Long break\r\nDTSTART;VALUE=DATE:20261224\r\nDTEND;VALUE=DATE:20261227\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nSUMMARY:Timed\r\nDTSTART:20260501T090000Z\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestParse(t *testing.T) {
	got, err := Parse(strings.NewReader(feed))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-05-01", "2026-11-11", "2026-12-24", "2026-12-25", "2026-12-26"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestLoadHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(feed))
	}))
	defer srv.Close()
	got, err := Load(context.Background(), srv.Client(), srv.URL+"/team.ics")
	if err != nil || len(got) != 5 {
		t.Fatalf("Load = %v, %v", got, err)
	}
}

// Package ics reads holiday dates from an iCalendar feed (a team calendar
// URL or a local .ics file). Only the dates matter: every day an event
// covers becomes a holiday.
package ics

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// Load fetches src (http(s) URL or file path) and returns YYYY-MM-DD dates.
func Load(ctx context.Context, client *http.Client, src string) ([]string, error) {
	if src == "" {
		return nil, nil
	}
	var r io.ReadCloser
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "webcal://") {
		u := strings.Replace(src, "webcal://", "https://", 1)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode/100 != 2 {
			resp.Body.Close()
			return nil, fmt.Errorf("holidays ics: HTTP %d", resp.StatusCode)
		}
		r = resp.Body
	} else {
		f, err := os.Open(src)
		if err != nil {
			return nil, err
		}
		r = f
	}
	defer r.Close()
	return Parse(r)
}

// Parse extracts the dates covered by every VEVENT.
func Parse(r io.Reader) ([]string, error) {
	lines, err := unfold(r)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	var start, end time.Time
	var allDayEnd bool
	in := false
	for _, ln := range lines {
		name, val := split(ln)
		switch {
		case ln == "BEGIN:VEVENT":
			in, start, end, allDayEnd = true, time.Time{}, time.Time{}, false
		case ln == "END:VEVENT":
			if in && !start.IsZero() {
				last := start
				if !end.IsZero() {
					last = end
					if allDayEnd {
						last = end.AddDate(0, 0, -1) // DTEND is exclusive for dates
					}
				}
				for d := start; !d.After(last); d = d.AddDate(0, 0, 1) {
					set[d.Format("2006-01-02")] = true
				}
			}
			in = false
		case in && name == "DTSTART":
			start, _ = parseDate(val)
		case in && name == "DTEND":
			var dateOnly bool
			end, dateOnly = parseDate(val)
			allDayEnd = dateOnly
		}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}

func unfold(r io.Reader) ([]string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var out []string
	for sc.Scan() {
		ln := strings.TrimRight(sc.Text(), "\r")
		if (strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t")) && len(out) > 0 {
			out[len(out)-1] += ln[1:]
			continue
		}
		out = append(out, ln)
	}
	return out, sc.Err()
}

// split returns the property name without parameters and its value:
// "DTSTART;VALUE=DATE:20261111" → "DTSTART", "20261111".
func split(ln string) (string, string) {
	head, val, ok := strings.Cut(ln, ":")
	if !ok {
		return "", ""
	}
	name, _, _ := strings.Cut(head, ";")
	return strings.ToUpper(name), val
}

func parseDate(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if len(v) >= 8 {
		if t, err := time.Parse("20060102", v[:8]); err == nil {
			return t, len(v) == 8
		}
	}
	return time.Time{}, false
}

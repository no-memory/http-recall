package splunk

import (
	"testing"
	"time"
)

func TestConvertBasic(t *testing.T) {
	rs := Ruleset{
		Rules: []Rule{{
			ID: "R1",
			Match: Match{Method: "POST", PathRegex: "^/api/orders/create$"},
			Template: Template{
				Method:  "LOG",
				URL:     "/api/orders/create?trace=${trace_id}",
				Headers: map[string]string{"X-Client-IP": "${client_ip}"},
				Body:    `{"order_id":"${order_id}"}`,
			},
			SkipMissing: true,
		}},
	}
	conv, err := NewConverter(rs)
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]string{
		{"_time": "2026-08-09T14:02:11.482+08:00", "method": "POST", "uri_path": "/api/orders/create",
			"trace_id": "t-1", "client_ip": "10.0.0.1", "order_id": "ORD-1"},
		{"_time": "2026-08-09T14:02:12.000+08:00", "method": "POST", "uri_path": "/api/orders/create",
			"trace_id": "t-2", "client_ip": "10.0.0.2", "order_id": "ORD-2"},
	}
	set, err := conv.Convert(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(set.Items))
	}
	it := set.Items[0]
	if time.Duration(it.At) != 0 {
		t.Errorf("first item At = %v, want 0", it.At)
	}
	if got := time.Duration(set.Items[1].At); got != 518*time.Millisecond {
		t.Errorf("second item At = %v, want 518ms", got)
	}
	spec := it.Spec
	if spec.Method != "POST" || spec.URL != "/api/orders/create?trace=t-1" {
		t.Errorf("spec mismatch: %+v", spec)
	}
	if spec.Headers["X-Client-IP"] != "10.0.0.1" {
		t.Errorf("header bind: %v", spec.Headers)
	}
	if spec.Body != `{"order_id":"ORD-1"}` {
		t.Errorf("body bind: %s", spec.Body)
	}
}

func TestConvertSkipsNonMatchingAndMissingFields(t *testing.T) {
	rs := Ruleset{
		Rules: []Rule{{
			ID: "R1",
			Match: Match{Method: "GET", PathRegex: "^/api/orders/[0-9]+$"},
			Template: Template{Method: "LOG", URL: "/api/orders/${order_id}"},
			SkipMissing: true,
		}},
	}
	conv, _ := NewConverter(rs)
	rows := []map[string]string{
		{"_time": "0", "method": "POST", "uri_path": "/api/orders/create"}, // wrong method
		{"_time": "1", "method": "GET", "uri_path": "/api/orders/abc"},     // path not numeric
		{"_time": "2", "method": "GET", "uri_path": "/api/orders/42"},      // missing order_id
		{"_time": "3", "method": "GET", "uri_path": "/api/orders/7", "order_id": "7"}, // ok
	}
	set, err := conv.Convert(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Items) != 1 {
		t.Fatalf("got %d items, want 1 (only the last row matches)", len(set.Items))
	}
	if set.Items[0].Spec.URL != "/api/orders/7" {
		t.Errorf("url = %s", set.Items[0].Spec.URL)
	}
}

func TestConvertSortsByTime(t *testing.T) {
	rs := Ruleset{
		Rules: []Rule{{
			ID: "R1",
			Template: Template{Method: "GET", URL: "/x"},
		}},
	}
	conv, _ := NewConverter(rs)
	rows := []map[string]string{
		{"_time": "2026-08-09T14:02:15.000+08:00"},
		{"_time": "2026-08-09T14:02:11.000+08:00"},
		{"_time": "2026-08-09T14:02:13.000+08:00"},
	}
	set, err := conv.Convert(rows)
	if err != nil {
		t.Fatal(err)
	}
	got := []time.Duration{}
	for _, it := range set.Items {
		got = append(got, time.Duration(it.At))
	}
	want := []time.Duration{0, 2 * time.Second, 4 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("At[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestParseLogTime(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{"2026-08-09T14:02:11.482+08:00", time.Date(2026, 8, 9, 14, 2, 11, 482000000, time.FixedZone("", 8*3600))},
		{"2026-08-09 14:02:11.482", time.Date(2026, 8, 9, 14, 2, 11, 482000000, time.UTC)},
		{"1786263300000", time.Unix(1786263300, 0)},
		{"1786263300.5", time.Unix(1786263300, 500000000)},
	}
	for _, c := range cases {
		got, err := parseLogTime(c.in)
		if err != nil {
			t.Errorf("parseLogTime(%q): %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parseLogTime(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if _, err := parseLogTime(""); err == nil {
		t.Error("empty time should error")
	}
}

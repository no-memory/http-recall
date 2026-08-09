package httprecall

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
)

type chartHTTPResponse struct {
	Values []json.RawMessage `json:"values"`
	Time   string            `json:"time"`
}

func newTestCharts(t *testing.T, dataFunc func() *ChartsReport) *Charts {
	t.Helper()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	charts, err := NewCharts(ln, dataFunc, nil, "test benchmark")
	if err != nil {
		t.Fatal(err)
	}
	return charts
}

func handleChartRequest(charts *Charts, path string) *fasthttp.Response {
	var req fasthttp.Request
	var ctx fasthttp.RequestCtx
	req.SetRequestURI(path)
	ctx.Init(&req, nil, nil)
	charts.Handler(&ctx)
	return &ctx.Response
}

func decodeChartResponse(t *testing.T, resp *fasthttp.Response) chartHTTPResponse {
	t.Helper()

	if resp.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode(), resp.Body())
	}
	var got chartHTTPResponse
	if err := json.Unmarshal(resp.Body(), &got); err != nil {
		t.Fatalf("invalid chart JSON: %v\n%s", err, resp.Body())
	}
	if _, err := time.Parse(timeFormat, got.Time); err != nil {
		t.Fatalf("time %q does not match %q: %v", got.Time, timeFormat, err)
	}
	return got
}

func TestChartsHandlerDataEndpoints(t *testing.T) {
	charts := newTestCharts(t, func() *ChartsReport {
		latency := Stats{}
		latency.Update(float64(10 * time.Millisecond))
		latency.Update(float64(30 * time.Millisecond))
		return &ChartsReport{
			RPS:         99.5,
			Latency:     latency,
			CodeMap:     map[int]int64{200: 2, 503: 1},
			Concurrency: 8,
		}
	})

	tests := []struct {
		name      string
		path      string
		wantItems int
		assert    func(t *testing.T, got chartHTTPResponse)
	}{
		{
			name:      "latency",
			path:      apiPath + latencyView,
			wantItems: 3,
			assert: func(t *testing.T, got chartHTTPResponse) {
				var min, mean, max float64
				if err := json.Unmarshal(got.Values[0], &min); err != nil || min != 10 {
					t.Fatalf("latency min = %v err=%v, want 10", min, err)
				}
				if err := json.Unmarshal(got.Values[1], &mean); err != nil || mean != 20 {
					t.Fatalf("latency mean = %v err=%v, want 20", mean, err)
				}
				if err := json.Unmarshal(got.Values[2], &max); err != nil || max != 30 {
					t.Fatalf("latency max = %v err=%v, want 30", max, err)
				}
			},
		},
		{
			name:      "rps",
			path:      apiPath + rpsView,
			wantItems: 1,
			assert: func(t *testing.T, got chartHTTPResponse) {
				var rps float64
				if err := json.Unmarshal(got.Values[0], &rps); err != nil || rps != 99.5 {
					t.Fatalf("rps = %v err=%v, want 99.5", rps, err)
				}
			},
		},
		{
			name:      "code",
			path:      apiPath + codeView,
			wantItems: 1,
			assert: func(t *testing.T, got chartHTTPResponse) {
				var codes map[string]int64
				if err := json.Unmarshal(got.Values[0], &codes); err != nil {
					t.Fatalf("code map is invalid: %v", err)
				}
				if codes["200"] != 2 || codes["503"] != 1 {
					t.Fatalf("codes = %#v, want 200=2 and 503=1", codes)
				}
			},
		},
		{
			name:      "concurrency",
			path:      apiPath + concurrencyView,
			wantItems: 1,
			assert: func(t *testing.T, got chartHTTPResponse) {
				var concurrency int
				if err := json.Unmarshal(got.Values[0], &concurrency); err != nil || concurrency != 8 {
					t.Fatalf("concurrency = %v err=%v, want 8", concurrency, err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeChartResponse(t, handleChartRequest(charts, tt.path))
			if len(got.Values) != tt.wantItems {
				t.Fatalf("values len = %d, want %d", len(got.Values), tt.wantItems)
			}
			tt.assert(t, got)
		})
	}
}

func TestChartsHandlerDataEndpointsWithNoData(t *testing.T) {
	charts := newTestCharts(t, func() *ChartsReport { return nil })

	for _, tt := range []struct {
		path      string
		wantNulls int
	}{
		{apiPath + latencyView, 3},
		{apiPath + rpsView, 1},
		{apiPath + codeView, 1},
		{apiPath + concurrencyView, 1},
	} {
		t.Run(tt.path, func(t *testing.T) {
			got := decodeChartResponse(t, handleChartRequest(charts, tt.path))
			if len(got.Values) != tt.wantNulls {
				t.Fatalf("values len = %d, want %d", len(got.Values), tt.wantNulls)
			}
			for i, raw := range got.Values {
				if string(raw) != "null" {
					t.Fatalf("values[%d] = %s, want null", i, raw)
				}
			}
		})
	}
}

func TestChartsHandlerServesPageAndNotFound(t *testing.T) {
	charts := newTestCharts(t, func() *ChartsReport { return nil })

	page := handleChartRequest(charts, "/")
	if page.StatusCode() != fasthttp.StatusOK || len(page.Body()) == 0 {
		t.Fatalf("GET / status=%d body len=%d, want 200 with body", page.StatusCode(), len(page.Body()))
	}
	if got := string(page.Header.ContentType()); got != "text/html" {
		t.Fatalf("GET / content-type = %q, want text/html", got)
	}
	// the page must be self-contained: inline canvas JS, no external deps
	for _, want := range []string{"HTTP·RECALL", "canvas", "/data/rps", "fetchJSON"} {
		if !strings.Contains(string(page.Body()), want) {
			t.Fatalf("GET / missing %q in body", want)
		}
	}

	missing := handleChartRequest(charts, "/missing")
	if missing.StatusCode() != fasthttp.StatusNotFound {
		t.Fatalf("missing status=%d, want 404", missing.StatusCode())
	}
}

func TestChartsHandlerProgressEndpoint(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	charts, err := NewCharts(ln, func() *ChartsReport { return nil },
		func() ReplayProgress {
			return ReplayProgress{Sent: 4, Total: 5, Speed: 2, SimTime: 3619 * time.Millisecond, VU: 10}
		}, "replay test")
	if err != nil {
		t.Fatal(err)
	}

	got := decodeChartResponse(t, handleChartRequest(charts, apiPath+progressView))
	if len(got.Values) != 1 {
		t.Fatalf("values len = %d, want 1", len(got.Values))
	}
	var p struct {
		Sent   int64   `json:"sent"`
		Total  int64   `json:"total"`
		Speed  float64 `json:"speed"`
		SimMS  float64 `json:"sim_ms"`
		VU     int     `json:"vu"`
	}
	if err := json.Unmarshal(got.Values[0], &p); err != nil {
		t.Fatalf("progress payload invalid: %v", err)
	}
	if p.Sent != 4 || p.Total != 5 || p.Speed != 2 || p.VU != 10 {
		t.Fatalf("progress = %+v", p)
	}
	if p.SimMS < 3618 || p.SimMS > 3620 {
		t.Fatalf("sim_ms = %v, want ~3619", p.SimMS)
	}

	// benchmark mode (nil progress func) returns null
	charts2 := newTestCharts(t, func() *ChartsReport { return nil })
	got2 := decodeChartResponse(t, handleChartRequest(charts2, apiPath+progressView))
	if string(got2.Values[0]) != "null" {
		t.Fatalf("benchmark progress = %s, want null", got2.Values[0])
	}
}

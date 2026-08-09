package httprecall

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
)

// ─────────────────────────────────────────────────────────────────────────
// RequestSpec — one HTTP request template, derived from a log entry.
// URL is a path (+query) that will be sent to the target host from ClientOpt.
// ─────────────────────────────────────────────────────────────────────────
type RequestSpec struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// ReplayDuration accepts both a JSON string ("1500ms") and a raw int
// (nanoseconds) for ergonomic request-set files.
type ReplayDuration time.Duration

func (d *ReplayDuration) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		v, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		*d = ReplayDuration(v)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*d = ReplayDuration(time.Duration(n))
	return nil
}

func (d ReplayDuration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// ReplayItem — a request fired at At (relative offset from the set start,
// i.e. the log's _time delta from the earliest entry).
type ReplayItem struct {
	At   ReplayDuration `json:"at"`
	Spec *RequestSpec   `json:"spec"`
}

type ReplaySet struct {
	Items []ReplayItem `json:"items"`
}

// ReplayOptions controls the replay scheduler.
type ReplayOptions struct {
	Speed      float64       // time compression: 2 = replay twice as fast
	VU         int           // max concurrent in-flight requests (0 = unlimited)
	StartAt    time.Duration // skip requests before this offset
	EndAt      time.Duration // skip requests at/after this offset (0 = no limit)
	FailPolicy string        // continue | pause | abort
}

// ReplayRequester replays a timestamped request set against a target host,
// preserving the relative timing between requests (scaled by Speed) and
// bounding concurrency with a VU slot pool (excess requests queue in order).
//
// It reuses Requester.DoRequest for execution and pushes the exact same
// *ReportRecord stream, so the existing report / printer / charts layers
// work unchanged.
type ReplayRequester struct {
	items      []ReplayItem
	opts       ReplayOptions
	base       *Requester // shares DoRequest + httpClient + recordChan
	recordChan chan *ReportRecord

	cancel    context.CancelFunc
	closeOnce sync.Once
	wg        sync.WaitGroup

	failed     int64
	readBytes  int64
	writeBytes int64

	// live progress counters for UI layers.
	sent    int64
	simTime int64 // nanoseconds of simulated log offset reached
}

// ReplayProgress is a point-in-time snapshot of a replay run, for the TUI.
type ReplayProgress struct {
	Sent    int64
	Total   int64
	Speed   float64
	SimTime time.Duration // simulated log offset reached
	VU      int
}

// Progress returns a consistent snapshot of the run so far.
func (r *ReplayRequester) Progress() ReplayProgress {
	return ReplayProgress{
		Sent:    atomic.LoadInt64(&r.sent),
		Total:   int64(len(r.items)),
		Speed:   r.opts.Speed,
		SimTime: time.Duration(atomic.LoadInt64(&r.simTime)),
		VU:      r.opts.VU,
	}
}

func NewReplayRequester(items []ReplayItem, opts ReplayOptions, errWriter io.Writer, clientOpt *ClientOpt) (*ReplayRequester, error) {
	if opts.Speed <= 0 {
		opts.Speed = 1
	}
	if opts.VU <= 0 {
		opts.VU = 1
	}
	// Reuse NewRequester to build the fasthttp.HostClient (connection pool,
	// proxy, TLS) and the buffered record channel. We never call base.Run(),
	// only base.DoRequest.
	base, err := NewRequester(opts.VU, 0, 0, nil, errWriter, clientOpt, 0)
	if err != nil {
		return nil, err
	}
	return &ReplayRequester{
		items:      items,
		opts:       opts,
		base:       base,
		recordChan: base.recordChan,
	}, nil
}

func (r *ReplayRequester) RecordChan() <-chan *ReportRecord {
	return r.recordChan
}

func (r *ReplayRequester) Cancel() {
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *ReplayRequester) closeRecord() {
	r.closeOnce.Do(func() { close(r.recordChan) })
}

// Run schedules the request set. One goroutine per in-window item waits for
// its trigger time, then acquires a VU slot (blocking = queue, preserving
// order) and executes. When the window is exhausted it drains the slot pool
// and closes the record channel.
func (r *ReplayRequester) Run() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	defer cancel()

	// Anchor the report elapsed clock (same global the benchmark mode uses).
	atomic.StoreInt64(&startTimeUnixNano, time.Now().UnixNano())

	sem := make(chan struct{}, r.opts.VU)

	for _, it := range r.items {
		if it.At < ReplayDuration(r.opts.StartAt) {
			continue
		}
		if r.opts.EndAt > 0 && time.Duration(it.At) >= r.opts.EndAt {
			break // items are expected to be sorted ascending
		}
		delay := time.Duration(float64(time.Duration(it.At)-r.opts.StartAt) / r.opts.Speed)

		r.wg.Add(1)
		go func(item ReplayItem) {
			defer r.wg.Done()
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			select {
			case sem <- struct{}{}: // acquire VU slot (queues in order)
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			r.execute(item)
		}(it)
	}

	r.wg.Wait()
	r.closeRecord()
}

func (r *ReplayRequester) execute(it ReplayItem) {
	spec := it.Spec
	if spec == nil {
		return
	}
	req := &fasthttp.Request{}
	resp := &fasthttp.Response{}

	method := spec.Method
	if method == "" {
		method = "GET"
	}
	// Start from the target host's common header (Host, content-type, etc.)
	// then apply the per-request spec on top.
	r.base.httpHeader.CopyTo(&req.Header)
	req.Header.SetMethod(method)
	req.SetRequestURI(spec.URL)
	for k, v := range spec.Headers {
		req.Header.Set(k, v)
	}
	if spec.Body != "" {
		req.SetBodyRaw([]byte(spec.Body))
	}

	rr := recordPool.Get().(*ReportRecord)
	r.base.DoRequest(req, resp, rr)
	rr.readBytes = atomic.LoadInt64(&r.readBytes)
	rr.writeBytes = atomic.LoadInt64(&r.writeBytes)
	rr.method = method
	rr.url = spec.URL
	r.recordChan <- rr // pool is returned by StreamReport.Collect

	// advance the live progress counters
	atomic.StoreInt64(&r.simTime, int64(time.Duration(it.At)))
	atomic.AddInt64(&r.sent, 1)

	if rr.error != "" {
		n := atomic.AddInt64(&r.failed, 1)
		switch r.opts.FailPolicy {
		case "abort":
			r.Cancel()
		case "pause":
			if n%50 == 0 {
				time.Sleep(2 * time.Second)
			}
		}
	}
}

// runReplay wires the replay scheduler into the same report / printer /
// charts pipeline used by the benchmark mode.
func runReplay(cfg *Config, clientOpt ClientOpt, errWriter io.Writer) error {
	data, err := os.ReadFile(cfg.ReplayFile)
	if err != nil {
		return err
	}
	var set ReplaySet
	if err := json.Unmarshal(data, &set); err != nil {
		return fmt.Errorf("invalid replay set: %w", err)
	}

	opts := ReplayOptions{
		Speed:      cfg.Speed,
		VU:         cfg.Concurrency,
		StartAt:    cfg.ReplayFrom,
		EndAt:      cfg.ReplayTo,
		FailPolicy: cfg.FailPolicy,
	}
	rq, err := NewReplayRequester(set.Items, opts, errWriter, &clientOpt)
	if err != nil {
		return err
	}

	desc := fmt.Sprintf("Replaying %d request(s) at %.1fx speed with %d VU → %s",
		len(set.Items), cfg.Speed, cfg.Concurrency, clientOpt.url)
	fmt.Fprintln(os.Stderr, desc)

	go rq.Run()
	report := NewStreamReport()
	go report.Collect(rq.RecordChan())

	var ln net.Listener
	if cfg.Listen != "" {
		ln, err = net.Listen("tcp", cfg.Listen)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "@ Real-time charts is listening on http://%s\n", ln.Addr().String())
	}
	if ln != nil {
		charts, err := NewCharts(ln, report.Charts, rq.Progress, desc)
		if err != nil {
			return err
		}
		go charts.Serve(cfg.AutoOpenBrowser)
	}

	// terminal printer (or injected TUI)
	return renderRun(cfg, report, RunMeta{Mode: "replay", Desc: desc, Progress: rq.Progress})
}

package httprecall

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// Config carries every CLI option into the run orchestration. The cmd layer
// (kingpin) builds it; this package executes benchmark or replay mode.
type Config struct {
	URL         string
	Concurrency int
	ReqRate     *rate.Limit
	RampUp      int
	Requests    int64
	Duration    time.Duration
	Interval    time.Duration
	Seconds     bool
	JSON        bool

	ReplayFile string
	Speed      float64
	FailPolicy string
	ReplayFrom time.Duration
	ReplayTo   time.Duration

	Method      string
	MethodSet   bool
	Headers     []string
	Host        string
	ContentType string
	Body        string
	Stream      bool
	Cert        string
	Key         string
	Insecure    bool

	Listen          string
	Timeout         time.Duration
	DialTimeout     time.Duration
	ReqWriteTimeout time.Duration
	RespReadTimeout time.Duration
	Socks5          string
	HTTPProxy       string
	AutoOpenBrowser bool
	Clean           bool
	OutputErrors    string
	Summary         bool
	UnixSocket      string

	// UI selects how the live run is rendered. nil = ANSI terminal printer.
	UI UIRenderer
}

// RunMeta describes the active run to a UIRenderer.
type RunMeta struct {
	// Mode is "benchmark" or "replay".
	Mode string
	// Desc is the human-readable run description shown at startup.
	Desc string
	// Progress returns replay progress; nil in benchmark mode.
	Progress func() ReplayProgress
}

// UIRenderer renders a run's live statistics. Render must block until the
// run finishes (report.Done() closes). Implemented by the tui package.
type UIRenderer interface {
	Render(report *StreamReport, meta RunMeta) error
}

// Run executes either the benchmark mode or the replay mode and blocks until
// the run finishes (Ctrl-C or natural completion).
func Run(cfg *Config) error {
	var err error
	var bodyBytes []byte
	var bodyFile string

	if cfg.Body != "" {
		if strings.HasPrefix(cfg.Body, "@") {
			fileName := cfg.Body[1:]
			if _, err = os.Stat(fileName); err != nil {
				return err
			}
			if cfg.Stream {
				bodyFile = fileName
			} else {
				bodyBytes, err = os.ReadFile(fileName)
				if err != nil {
					return err
				}
			}
		} else {
			bodyBytes = []byte(cfg.Body)
		}

		if !cfg.MethodSet {
			cfg.Method = "POST"
		}
	}

	errWriter := io.Discard
	if cfg.OutputErrors != "" {
		errWriter, err = os.Create(cfg.OutputErrors)
		if err != nil {
			return err
		}
	}

	clientOpt := ClientOpt{
		url:       cfg.URL,
		method:    cfg.Method,
		headers:   cfg.Headers,
		bodyBytes: bodyBytes,
		bodyFile:  bodyFile,

		certPath: cfg.Cert,
		keyPath:  cfg.Key,
		insecure: cfg.Insecure,

		maxConns:     cfg.Concurrency,
		doTimeout:    cfg.Timeout,
		readTimeout:  cfg.RespReadTimeout,
		writeTimeout: cfg.ReqWriteTimeout,
		dialTimeout:  cfg.DialTimeout,

		socks5Proxy: cfg.Socks5,
		httpProxy:   cfg.HTTPProxy,
		contentType: cfg.ContentType,
		host:        cfg.Host,
		unixSocket:  cfg.UnixSocket,
	}

	// Replay mode: replay a timestamped request set instead of benchmarking.
	if cfg.ReplayFile != "" {
		return runReplay(cfg, clientOpt, errWriter)
	}

	requester, err := NewRequester(cfg.Concurrency, cfg.Requests, cfg.Duration, cfg.ReqRate, errWriter, &clientOpt, cfg.RampUp)
	if err != nil {
		return err
	}

	// description
	var desc string
	desc = fmt.Sprintf("Benchmarking %s", cfg.URL)
	if cfg.Requests > 0 {
		desc += fmt.Sprintf(" with %d request(s)", cfg.Requests)
	}
	if cfg.Duration > 0 {
		desc += fmt.Sprintf(" for %s", cfg.Duration.String())
	}
	if cfg.RampUp > 0 {
		desc += fmt.Sprintf(" with ramp up %d pre second", cfg.RampUp)
	}
	desc += fmt.Sprintf(" using %d connection(s).", cfg.Concurrency)
	fmt.Fprintln(os.Stderr, desc)

	// charts listener
	var ln net.Listener
	if cfg.Listen != "" {
		ln, err = net.Listen("tcp", cfg.Listen)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "@ Real-time charts is listening on http://%s\n", ln.Addr().String())
	}
	fmt.Fprintln(os.Stderr, "")

	// do request
	go requester.Run()

	// metrics collection
	report := NewStreamReport()
	go report.Collect(requester.RecordChan())

	if ln != nil {
		// serve charts data
		charts, err := NewCharts(ln, report.Charts, desc)
		if err != nil {
			return err
		}
		go charts.Serve(cfg.AutoOpenBrowser)
	}

	// terminal printer (or injected TUI)
	return renderRun(cfg, report, RunMeta{Mode: "benchmark", Desc: desc})
}

// renderRun blocks on either the injected UI renderer or the default ANSI
// terminal printer, whichever the caller chose via Config.UI.
func renderRun(cfg *Config, report *StreamReport, meta RunMeta) error {
	if cfg.UI != nil {
		return cfg.UI.Render(report, meta)
	}
	printer := NewPrinter(cfg.Requests, cfg.Duration, !cfg.Clean, cfg.Summary)
	printer.PrintLoop(report.Snapshot, cfg.Interval, cfg.Seconds, cfg.JSON, report.Done())
	return nil
}

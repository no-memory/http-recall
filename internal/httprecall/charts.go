package httprecall

import (
	_ "embed"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	cors "github.com/AdhityaRamadhanus/fasthttpcors"
	"github.com/valyala/fasthttp"
)

//go:embed web.html
var webPage []byte

var (
	apiPath         = "/data/"
	latencyView     = "latency"
	rpsView         = "rps"
	codeView        = "code"
	concurrencyView = "concurrency"
	timeFormat      = "15:04:05"
)

type Metrics struct {
	Values []interface{} `json:"values"`
	Time   string        `json:"time"`
}

type Charts struct {
	ln       net.Listener
	dataFunc func() *ChartsReport
}

func NewCharts(ln net.Listener, dataFunc func() *ChartsReport, desc string) (*Charts, error) {
	c := &Charts{ln: ln, dataFunc: dataFunc}
	return c, nil
}

func (c *Charts) Handler(ctx *fasthttp.RequestCtx) {
	path := string(ctx.Path())
	if strings.HasPrefix(path, apiPath) {
		view := path[len(apiPath):]
		var values []interface{}
		reportData := c.dataFunc()
		switch view {
		case latencyView:
			if reportData != nil {
				values = append(values, reportData.Latency.min/1e6)
				values = append(values, reportData.Latency.Mean()/1e6)
				values = append(values, reportData.Latency.max/1e6)
			} else {
				values = append(values, nil, nil, nil)
			}
		case rpsView:
			if reportData != nil {
				values = append(values, reportData.RPS)
			} else {
				values = append(values, nil)
			}
		case codeView:
			if reportData != nil {
				values = append(values, reportData.CodeMap)
			} else {
				values = append(values, nil)
			}
		case concurrencyView:
			if reportData != nil {
				values = append(values, reportData.Concurrency)
			} else {
				values = append(values, nil)
			}
		}
		metrics := &Metrics{
			Time:   time.Now().Format(timeFormat),
			Values: values,
		}
		_ = json.NewEncoder(ctx).Encode(metrics)
	} else if path == "/" {
		ctx.SetContentType("text/html")
		_, _ = ctx.Write(webPage)
	} else {
		ctx.Error("NotFound", fasthttp.StatusNotFound)
	}
}

func (c *Charts) Serve(open bool) {
	server := fasthttp.Server{
		Handler: cors.DefaultHandler().CorsMiddleware(c.Handler),
	}
	if open {
		go openBrowser("http://" + c.ln.Addr().String())
	}
	_ = server.Serve(c.ln)
}

// openBrowser go/src/cmd/internal/browser/browser.go
func openBrowser(url string) bool {
	var cmds [][]string
	if exe := os.Getenv("BROWSER"); exe != "" {
		cmds = append(cmds, []string{exe})
	}
	switch runtime.GOOS {
	case "darwin":
		cmds = append(cmds, []string{"/usr/bin/open"})
	case "windows":
		cmds = append(cmds, []string{"cmd", "/c", "start"})
	default:
		if os.Getenv("DISPLAY") != "" {
			// xdg-open is only for use in a desktop environment.
			cmds = append(cmds, []string{"xdg-open"})
		}
	}
	cmds = append(cmds,
		[]string{"chrome"},
		[]string{"google-chrome"},
		[]string{"chromium"},
		[]string{"firefox"},
	)
	for _, args := range cmds {
		cmd := exec.Command(args[0], append(args[1:], url)...)
		if cmd.Start() == nil && appearsSuccessful(cmd, 3*time.Second) {
			return true
		}
	}
	return false
}

// appearsSuccessful reports whether the command appears to have run successfully.
// If the command runs longer than the timeout, it's deemed successful.
// If the command runs within the timeout, it's deemed successful if it exited cleanly.
func appearsSuccessful(cmd *exec.Cmd, timeout time.Duration) bool {
	errc := make(chan error, 1)
	go func() {
		errc <- cmd.Wait()
	}()

	select {
	case <-time.After(timeout):
		return true
	case err := <-errc:
		return err == nil
	}
}

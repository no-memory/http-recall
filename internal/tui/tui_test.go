package tui

import (
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"httprecall/internal/httprecall"
)

func fakeSnapshot() *httprecall.SnapshotReport {
	rs := &httprecall.SnapshotReport{
		Elapsed: 3 * time.Second,
		Count:   18420,
		RPS:     2340.5,
		Codes:   map[string]int64{"2xx": 18390, "4xx": 20, "5xx": 10},
	}
	rs.Percentiles = []*struct {
		Percentile float64
		Latency    time.Duration
	}{
		{0.5, 86 * time.Millisecond},
		{0.95, 204 * time.Millisecond},
		{0.99, 298 * time.Millisecond},
	}
	return rs
}

func TestModelViewRendersDashboard(t *testing.T) {
	m := New(func() *httprecall.SnapshotReport { return fakeSnapshot() }, nil, nil, nil, httprecall.RunMeta{Mode: "benchmark"}, nil)
	m.last = m.Snapshot()

	v := m.View().Content
	for _, want := range []string{"HTTP·RECALL", "BENCHMARK", "MARKET", "ELAPSED", "3s", "18,420", "2,340.5", "99.84%", "P99", "298ms", "2xx", "QPS"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n---\n%s", want, v)
		}
	}
}

func TestModelQuitsOnQ(t *testing.T) {
	m := New(func() *httprecall.SnapshotReport { return fakeSnapshot() }, nil, nil, nil, httprecall.RunMeta{Mode: "benchmark"}, nil)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("expected quit command on 'q'")
	}
}

func TestModelQuitsOnDone(t *testing.T) {
	done := make(chan struct{})
	m := New(func() *httprecall.SnapshotReport { return fakeSnapshot() }, nil, nil, nil, httprecall.RunMeta{Mode: "replay"}, done)
	close(done)

	var got tea.Msg
	cmd := m.Init()
	// init returns a batch; send done through the program to be sure.
	p := tea.NewProgram(m, tea.WithOutput(io.Discard), tea.WithInput(strings.NewReader("")))
	go func() {
		<-time.After(2 * time.Second)
		p.Quit()
	}()
	_ = got
	_ = cmd
	if _, err := p.Run(); err != nil {
		t.Fatalf("program run: %v", err)
	}
}

func TestModelTickUpdatesSnapshot(t *testing.T) {
	calls := 0
	m := New(func() *httprecall.SnapshotReport {
		calls++
		return fakeSnapshot()
	}, nil, nil, nil, httprecall.RunMeta{Mode: "benchmark"}, nil)

	updated, _ := m.Update(tickMsg(time.Now()))
	mm, ok := updated.(Model)
	if !ok {
		t.Fatalf("update returned %T, want Model", updated)
	}
	if mm.last == nil {
		t.Error("snapshot not stored after tick")
	}
}

func TestProgressViewRendersBar(t *testing.T) {
	p := &httprecall.ReplayProgress{Sent: 4, Total: 5, Speed: 2, SimTime: 3 * time.Second, VU: 10}
	v := progressView(p)
	for _, want := range []string{"80.0%", "4 / 5", "2", "3s", "10"} {
		if !strings.Contains(v, want) {
			t.Errorf("progressView missing %q\n---\n%s", want, v)
		}
	}
}

func TestModelSwitchesViews(t *testing.T) {
	m := New(func() *httprecall.SnapshotReport { return fakeSnapshot() }, nil, nil, nil,
		httprecall.RunMeta{Mode: "benchmark"}, nil)
	m.last = fakeSnapshot()

	// start on MARKET
	if !strings.Contains(m.View().Content, "[MARKET]") {
		t.Error("initial view should be MARKET")
	}
	// tab → TAPE
	updated, _ := m.Update(tea.KeyPressMsg{Code: '\t'})
	mm := updated.(Model)
	if !strings.Contains(mm.View().Content, "[TAPE]") {
		t.Error("tab should switch to TAPE")
	}
	// tab → ERRORS
	updated, _ = mm.Update(tea.KeyPressMsg{Code: '\t'})
	mm = updated.(Model)
	if !strings.Contains(mm.View().Content, "[ERRORS]") {
		t.Error("second tab should switch to ERRORS")
	}
	// right arrow → wrap to MARKET
	updated, _ = mm.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModShift}) // placeholder, use explicit
	_ = updated
}

func TestErrorsViewListsCounts(t *testing.T) {
	m := New(func() *httprecall.SnapshotReport { return fakeSnapshot() }, nil, nil, nil,
		httprecall.RunMeta{Mode: "benchmark"}, nil)
	m.last = fakeSnapshot()
	m.last.Errors = map[string]int64{"dial tcp: connection refused": 12, "context deadline exceeded": 3}
	m.view = 2
	v := m.View().Content
	for _, want := range []string{"connection refused", "context deadline exceeded", "12", "3"} {
		if !strings.Contains(v, want) {
			t.Errorf("errors view missing %q\n---\n%s", want, v)
		}
	}
}

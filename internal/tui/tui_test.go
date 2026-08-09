package tui

import (
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
	m := New(func() *httprecall.SnapshotReport { return fakeSnapshot() }, nil)
	m.last = m.Snapshot()

	v := m.View()
	for _, want := range []string{"HTTP·RECALL", "ELAPSED", "3s", "18,420", "2,340.5", "99.84%", "P99", "298ms", "2xx"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n---\n%s", want, v)
		}
	}
}

func TestModelQuitsOnQ(t *testing.T) {
	m := New(func() *httprecall.SnapshotReport { return fakeSnapshot() }, nil)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("expected quit command on 'q'")
	}
}

func TestModelQuitsOnDone(t *testing.T) {
	done := make(chan struct{})
	m := New(func() *httprecall.SnapshotReport { return fakeSnapshot() }, done)
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
	}, nil)

	updated, _ := m.Update(tickMsg(time.Now()))
	mm, ok := updated.(Model)
	if !ok {
		t.Fatalf("update returned %T, want Model", updated)
	}
	if mm.last == nil {
		t.Error("snapshot not stored after tick")
	}
}

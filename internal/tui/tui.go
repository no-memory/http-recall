// Package tui renders the benchmark / replay run as a Bubble Tea terminal
// application: a market-terminal style dashboard fed by the stream report.
//
// It is a drop-in alternative to the ANSI printer in internal/httprecall;
// the cmd layer picks which renderer to use (--ui terminal|tui).
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"

	"httprecall/internal/httprecall"
)

// ─────────────────────────────────────────────────────────────────────────
// palette (trading-terminal)
// ─────────────────────────────────────────────────────────────────────────

var (
	cyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8")).Bold(true)
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e"))
	coral  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff4757"))
	amber  = lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b"))
	dim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#5f7186"))
	bold   = lipgloss.NewStyle().Bold(true)
	accent = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#03111a")).
		Background(lipgloss.Color("#38bdf8")).
		Bold(true).
		Padding(0, 2)

	box = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#22304a")).
		Padding(0, 2)
)

// panel renders a titled bordered panel: a cyan title bar above a box.
func panel(title, body string) string {
	return accent.Render(" " + title + " ") + "\n" + box.Render(body)
}

// Model is the Bubble Tea state for the dashboard.
type Model struct {
	Snapshot func() *httprecall.SnapshotReport
	Recent   func() []httprecall.RecentRequest
	RPSHist  func() []float64
	Done     <-chan struct{}

	width  int
	height int
	last   *httprecall.SnapshotReport
	// tape keeps the last shown request outcomes (newest first).
	tape []httprecall.RecentRequest
	// hist keeps the last shown per-second RPS samples.
	hist []float64
}

func New(snapFn func() *httprecall.SnapshotReport, recentFn func() []httprecall.RecentRequest, rpsFn func() []float64, done <-chan struct{}) Model {
	return Model{Snapshot: snapFn, Recent: recentFn, RPSHist: rpsFn, Done: done}
}

// ── messages ──────────────────────────────────────────────────────────────

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type doneMsg struct{}

func waitDone(done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		if done == nil {
			return nil
		}
		<-done
		return doneMsg{}
	}
}

// ── tea lifecycle ─────────────────────────────────────────────────────────

func (m Model) Init() tea.Cmd {
	return tea.Batch(tick(), waitDone(m.Done))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	case tickMsg:
		m.last = m.Snapshot()
		if m.Recent != nil {
			rr := m.Recent()
			// newest first for the tape
			for i, j := 0, len(rr)-1; i < j; i, j = i+1, j-1 {
				rr[i], rr[j] = rr[j], rr[i]
			}
			m.tape = rr
		}
		if m.RPSHist != nil {
			m.hist = m.RPSHist()
		}
		return m, tick()
	case doneMsg:
		return m, tea.Quit
	}
	return m, nil
}

// ── view ──────────────────────────────────────────────────────────────────

func (m Model) View() tea.View {
	rs := m.last
	if rs == nil {
		rs = &httprecall.SnapshotReport{}
	}

	var b strings.Builder

	// header
	b.WriteString(accent.Render(" HTTP·RECALL "))
	b.WriteString(dim.Render("  log replay · load test   "))
	b.WriteString("\n\n")

	// KPI row
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
		kpi("ELAPSED", rs.Elapsed.Round(time.Millisecond).String(), cyan),
		kpi("COUNT", formatInt(rs.Count), bold),
		kpi("RPS", formatFloat(rs.RPS), cyan),
		kpi("SUCCESS", successRate(rs), green),
		kpi("CONC", fmt.Sprintf("%d", rs.Concurrency()), bold),
	))
	b.WriteString("\n")

	// percentile row
	b.WriteString(panel("LATENCY PERCENTILES", percentiles(rs)))
	b.WriteString("\n\n")

	// status codes
	b.WriteString(panel("STATUS CODES", statusCodes(rs)))
	b.WriteString("\n\n")

	// QPS sparkline
	b.WriteString(panel("QPS — LAST 60s", sparkline(m.hist)))
	b.WriteString("\n\n")

	// request tape
	b.WriteString(panel("REQUEST TAPE", tape(m.tape)))
	b.WriteString("\n\n")

	// footer
	b.WriteString(dim.Render("q / ctrl+c quit"))

	return tea.NewView(b.String())
}

// sparkline renders a single-line density bar chart of the RPS history.
func sparkline(data []float64) string {
	const density = "▁▂▃▄▅▆▇█"
	if len(data) == 0 {
		return dim.Render("collecting…")
	}
	max := 0.0
	for _, v := range data {
		if v > max {
			max = v
		}
	}
	if max <= 0 {
		return dim.Render(strings.Repeat("▁", len(data)))
	}
	var b strings.Builder
	for _, v := range data {
		idx := int(v / max * 7.99)
		if idx > 7 {
			idx = 7
		}
		b.WriteString(cyan.Render(string(density[idx])))
	}
	return b.String()
}

// tape renders the most recent request outcomes as a market-style ticker.
func tape(rows []httprecall.RecentRequest) string {
	if len(rows) == 0 {
		return dim.Render("waiting for traffic…")
	}
	show := rows
	if len(show) > 12 {
		show = show[:12]
	}
	lines := make([]string, 0, len(show))
	for _, r := range show {
		codeStyle := dim
		switch {
		case r.Code >= 200 && r.Code < 300:
			codeStyle = green
		case r.Code >= 400 && r.Code < 500:
			codeStyle = amber
		case r.Code >= 500:
			codeStyle = coral
		case r.Error != "":
			codeStyle = coral
		}
		method := r.Method
		if method == "" {
			method = "GET"
		}
		status := fmt.Sprintf("%d", r.Code)
		if r.Error != "" {
			status = "ERR"
		}
		lines = append(lines, fmt.Sprintf("%s %s  %s  %s",
			codeStyle.Render(status),
			bold.Render(method),
			dim.Render(truncate(r.URL, 52)),
			dim.Render(r.Cost.Round(time.Microsecond).String()),
		))
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n-1]) + "…"
}

func kpi(label, value string, valueStyle lipgloss.Style) string {
	return panel(label, valueStyle.Render(value))
}

func formatInt(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func formatFloat(f float64) string {
	s := fmt.Sprintf("%.1f", f)
	if i := strings.IndexByte(s, '.'); i > 0 {
		return formatInt(int64(f)) + s[i:]
	}
	return s
}

func successRate(rs *httprecall.SnapshotReport) string {
	if rs.Count == 0 {
		return "—"
	}
	ok := rs.Codes["2xx"]
	return fmt.Sprintf("%.2f%%", float64(ok)/float64(rs.Count)*100)
}

func percentiles(rs *httprecall.SnapshotReport) string {
	if len(rs.Percentiles) == 0 {
		return dim.Render("no data yet…")
	}
	parts := make([]string, 0, len(rs.Percentiles))
	for _, p := range rs.Percentiles {
		label := fmt.Sprintf("P%g", p.Percentile*100)
		val := p.Latency.Round(time.Microsecond).String()
		style := dim
		if p.Percentile >= 0.99 {
			style = amber
		}
		parts = append(parts, dim.Render(label)+"  "+style.Render(val))
	}
	return strings.Join(parts, "   ")
}

func statusCodes(rs *httprecall.SnapshotReport) string {
	if rs.Count == 0 {
		return dim.Render("waiting for traffic…")
	}
	order := []string{"1xx", "2xx", "3xx", "4xx", "5xx"}
	rows := make([]string, 0, len(order))
	for _, k := range order {
		n := rs.Codes[k]
		style := dim
		switch k {
		case "2xx":
			style = green
		case "4xx":
			style = amber
		case "5xx":
			style = coral
		}
		rows = append(rows, fmt.Sprintf("%s  %s", dim.Render(k), style.Render(fmt.Sprintf("%7d", n))))
	}
	return strings.Join(rows, "\n")
}

// Renderer implements httprecall.UIRenderer with the Bubble Tea dashboard.
type Renderer struct{}

func (Renderer) Render(report *httprecall.StreamReport, desc string) error {
	return RunProgram(report)
}

// RunProgram boots a full-screen Bubble Tea app for the given report feed
// and blocks until the run completes (done closes) or the user quits.
func RunProgram(report *httprecall.StreamReport) error {
	p := tea.NewProgram(New(report.Snapshot, report.RecentRequests, report.RPSHistory, report.Done()))
	_, err := p.Run()
	return err
}

package splunk

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"httprecall/internal/httprecall"
)

// ─────────────────────────────────────────────────────────────────────────
// Mapping rules — mirror the "Request Mapping" page of the design prototype:
// a log row that matches `match` is rendered through `template` with
// ${field} variables bound to log fields.
// ─────────────────────────────────────────────────────────────────────────

type Match struct {
	Method    string `json:"method,omitempty"`     // empty = any
	PathRegex string `json:"path_regex,omitempty"` // empty = any
}

type Template struct {
	Method  string            `json:"method"`  // "LOG" = take from log's method field
	URL     string            `json:"url"`     // may contain ${field}
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

type Rule struct {
	ID           string   `json:"id"`
	Match        Match    `json:"match"`
	Template     Template `json:"template"`
	SkipMissing  bool     `json:"skip_missing,omitempty"` // default true
	DefaultValue string   `json:"default_value,omitempty"`
}

type Ruleset struct {
	Rules []Rule `json:"rules"`
	// Field names inside the log rows.
	MethodField string `json:"method_field,omitempty"` // default "method"
	PathField   string `json:"path_field,omitempty"`   // default "uri_path"
	TimeField   string `json:"time_field,omitempty"`   // default "_time"
}

func (rs *Ruleset) defaults() {
	if rs.MethodField == "" {
		rs.MethodField = "method"
	}
	if rs.PathField == "" {
		rs.PathField = "uri_path"
	}
	if rs.TimeField == "" {
		rs.TimeField = "_time"
	}
}

func LoadRuleset(path string) (*Ruleset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rs Ruleset
	if err := json.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("invalid ruleset: %w", err)
	}
	rs.defaults()
	return &rs, nil
}

// ─────────────────────────────────────────────────────────────────────────
// Converter
// ─────────────────────────────────────────────────────────────────────────

type Converter struct {
	Ruleset Ruleset
	re      []*regexp.Regexp // compiled per rule
}

func NewConverter(rs Ruleset) (*Converter, error) {
	rs.defaults()
	c := &Converter{Ruleset: rs}
	for i := range rs.Rules {
		if rs.Rules[i].Match.PathRegex != "" {
			re, err := regexp.Compile(rs.Rules[i].Match.PathRegex)
			if err != nil {
				return nil, fmt.Errorf("rule %s: bad path_regex: %w", rs.Rules[i].ID, err)
			}
			c.re = append(c.re, re)
		} else {
			c.re = append(c.re, nil)
		}
	}
	return c, nil
}

// Convert turns Splunk log rows into a ReplaySet. Each row is a field map.
// The earliest _time across all rows anchors the set; every item's At is the
// delta from it (so out-of-order input still yields correct offsets).
func (c *Converter) Convert(rows []map[string]string) (*httprecall.ReplaySet, error) {
	set := &httprecall.ReplaySet{}

	// Find the earliest timestamp first: it anchors the replay offsets.
	anchor := time.Time{}
	for _, row := range rows {
		if t, err := parseLogTime(row[c.Ruleset.TimeField]); err == nil {
			if anchor.IsZero() || t.Before(anchor) {
				anchor = t
			}
		}
	}

	skipped := 0
	for _, row := range rows {
		item, ok := c.convertRow(row, anchor)
		if !ok {
			skipped++
			continue
		}
		set.Items = append(set.Items, *item)
	}

	// ReplayRequester expects items sorted ascending by At.
	sort.Slice(set.Items, func(i, j int) bool {
		return time.Duration(set.Items[i].At) < time.Duration(set.Items[j].At)
	})

	if len(set.Items) == 0 {
		return nil, fmt.Errorf("no log rows matched any rule (%d rows, %d skipped)", len(rows), skipped)
	}
	return set, nil
}

func (c *Converter) convertRow(row map[string]string, anchor time.Time) (*httprecall.ReplayItem, bool) {
	method := strings.ToUpper(row[c.Ruleset.MethodField])
	path := row[c.Ruleset.PathField]

	ruleIdx := -1
	for i := range c.Ruleset.Rules {
		r := &c.Ruleset.Rules[i]
		if r.Match.Method != "" && strings.ToUpper(r.Match.Method) != method {
			continue
		}
		if r.Match.PathRegex != "" {
			if c.re[i] == nil || !c.re[i].MatchString(path) {
				continue
			}
		}
		ruleIdx = i
		break
	}
	if ruleIdx < 0 {
		return nil, false
	}
	rule := &c.Ruleset.Rules[ruleIdx]

	// Resolve template variables.
	methodOut := rule.Template.Method
	if methodOut == "LOG" {
		methodOut = method
	}
	urlOut, ok := c.bind(rule, row, rule.Template.URL)
	if !ok {
		return nil, false
	}
	headers := make(map[string]string, len(rule.Template.Headers))
	for k, v := range rule.Template.Headers {
		hv, ok := c.bind(rule, row, v)
		if !ok {
			return nil, false
		}
		headers[k] = hv
	}
	bodyOut, ok := c.bind(rule, row, rule.Template.Body)
	if !ok {
		return nil, false
	}

	// Compute At from _time delta against the earliest row.
	at := time.Duration(0)
	if !anchor.IsZero() {
		if t, err := parseLogTime(row[c.Ruleset.TimeField]); err == nil {
			at = t.Sub(anchor)
			if at < 0 {
				at = 0
			}
		}
	}

	return &httprecall.ReplayItem{
		At: httprecall.ReplayDuration(at),
		Spec: &httprecall.RequestSpec{
			Method:  methodOut,
			URL:     urlOut,
			Headers: headers,
			Body:    bodyOut,
		},
	}, true
}

// bind renders a template string, replacing ${field} with the log value.
// Returns ok=false when a referenced field is missing (respecting SkipMissing
// and DefaultValue).
func (c *Converter) bind(rule *Rule, row map[string]string, tmpl string) (string, bool) {
	if !strings.Contains(tmpl, "${") {
		return tmpl, true
	}
	var sb strings.Builder
	rest := tmpl
	for {
		idx := strings.Index(rest, "${")
		if idx < 0 {
			sb.WriteString(rest)
			break
		}
		end := strings.Index(rest[idx:], "}")
		if end < 0 {
			sb.WriteString(rest)
			break
		}
		sb.WriteString(rest[:idx])
		field := rest[idx+2 : idx+end]
		val, ok := row[field]
		if !ok {
			if rule.SkipMissing {
				return "", false
			}
			val = rule.DefaultValue
		}
		sb.WriteString(val)
		rest = rest[idx+end+1:]
	}
	return sb.String(), true
}

// parseLogTime accepts RFC3339/ISO8601, epoch seconds and epoch millis.
func parseLogTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05.999999999", s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t, nil
	}
	// epoch seconds or millis
	if f, err := parseFloat(s); err == nil {
		sec := int64(f)
		nsec := int64((f - float64(sec)) * 1e9)
		if sec > 1e12 { // epoch millis
			sec = sec / 1000
			nsec = int64((f - float64(sec)*1000) * 1e6)
		}
		return time.Unix(sec, nsec), nil
	}
	return time.Time{}, fmt.Errorf("unrecognized time format: %q", s)
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

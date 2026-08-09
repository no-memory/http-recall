package splunk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────
// Minimal Splunk REST client — just enough for the log-replay pipeline:
// create a search job, poll until done, fetch the results as JSON.
// ─────────────────────────────────────────────────────────────────────────

type Client struct {
	BaseURL string // e.g. https://splunk.internal:8089
	Token   string // Splunk auth token (Authorization: Splunk <token>)
	HTTP    *http.Client
	Poll    time.Duration // job polling interval
	Timeout time.Duration // max job wait
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		Poll:    500 * time.Millisecond,
		Timeout: 60 * time.Second,
	}
}

type searchJob struct {
	ID string `json:"sid"`
}

// Search runs an ad-hoc search and returns the result rows as field maps.
// The query runs in "blocking" mode: we create the job, wait for completion
// and fetch results.
func (c *Client) Search(query string, maxCount int) ([]map[string]string, error) {
	if maxCount <= 0 {
		maxCount = 10000
	}

	// 1. create the job
	form := url.Values{}
	form.Set("search", query)
	form.Set("exec_mode", "blocking")
	form.Set("output_mode", "json")
	form.Set("max_count", strconv.Itoa(maxCount))
	form.Set("earliest_time", "-15m")
	form.Set("latest_time", "now")

	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/services/search/jobs", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.auth(req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create search job: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("create search job: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	job := &searchJob{}
	if err := json.NewDecoder(resp.Body).Decode(job); err != nil {
		return nil, fmt.Errorf("decode job response: %w", err)
	}

	// 2. fetch results (blocking mode means the job finished already)
	return c.fetchResults(job.ID)
}

func (c *Client) auth(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Splunk "+c.Token)
	}
}

// fetchResults gets the job's results. Splunk's JSON output has the shape
// {"results": [{"_time": "...", "field": "value", ...}, ...]} — we normalize
// every cell to a string so the converter has a uniform view.
func (c *Client) fetchResults(sid string) ([]map[string]string, error) {
	u := c.BaseURL + "/services/search/jobs/" + url.PathEscape(sid) + "/results"
	req, err := http.NewRequest(http.MethodGet, u+"?output_mode=json", nil)
	if err != nil {
		return nil, err
	}
	c.auth(req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch results: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fetch results: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// Splunk may prefix JSON with some non-JSON bytes; find the object start.
	if i := bytes.IndexByte(raw, '{'); i > 0 {
		raw = raw[i:]
	}
	var payload struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode results: %w", err)
	}

	rows := make([]map[string]string, 0, len(payload.Results))
	for _, r := range payload.Results {
		row := make(map[string]string, len(r))
		for k, v := range r {
			row[k] = cellString(v)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func cellString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

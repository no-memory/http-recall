// Command splunk converts Splunk logs into a replay request set for httprecall.
//
// Usage:
//
//	# fetch from Splunk
//	splunk --url https://splunk.internal:8089 --token $SPLUNK_TOKEN \
//	       --query 'index=api_gateway sourcetype=access_log' \
//	       --rules demo/rules.json --output requests.json
//
//	# or convert an offline log dump (JSON array or JSONL)
//	splunk --logs-file logs.json --rules demo/rules.json --output requests.json
//
// The output file feeds httprecall directly:
//
//	httprecall http://staging.internal --replay-file requests.json --speed 5 -c 200
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"httprecall/internal/splunk"
)

func main() {
	var (
		baseURL  = flag.String("url", "", "Splunk REST endpoint, e.g. https://splunk.internal:8089")
		token    = flag.String("token", "", "Splunk auth token (or $SPLUNK_TOKEN)")
		query    = flag.String("query", "", "SPL search query")
		logsFile = flag.String("logs-file", "", "local log dump (JSON array or JSONL) instead of querying Splunk")
		rules    = flag.String("rules", "", "mapping ruleset JSON (required)")
		output   = flag.String("output", "-", "output request-set file ('-' = stdout)")
		maxCount = flag.Int("max-count", 10000, "max rows to fetch from Splunk")
	)
	flag.Parse()

	if *rules == "" {
		fail("--rules is required")
	}
	rs, err := splunk.LoadRuleset(*rules)
	if err != nil {
		fail("load ruleset: %v", err)
	}

	var rows []map[string]string
	if *logsFile != "" {
		rows, err = loadLogs(*logsFile)
		if err != nil {
			fail("load logs: %v", err)
		}
	} else {
		if *baseURL == "" {
			fail("either --url (Splunk) or --logs-file is required")
		}
		if *query == "" {
			fail("--query is required when querying Splunk")
		}
		tk := *token
		if tk == "" {
			tk = os.Getenv("SPLUNK_TOKEN")
		}
		rows, err = splunk.NewClient(*baseURL, tk).Search(*query, *maxCount)
		if err != nil {
			fail("splunk search: %v", err)
		}
	}

	conv, err := splunk.NewConverter(*rs)
	if err != nil {
		fail("converter: %v", err)
	}
	set, err := conv.Convert(rows)
	if err != nil {
		fail("convert: %v", err)
	}

	data, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		fail("marshal: %v", err)
	}

	if *output == "-" {
		fmt.Println(string(data))
	} else {
		if err := os.WriteFile(*output, data, 0o644); err != nil {
			fail("write: %v", err)
		}
	}
	fmt.Fprintf(os.Stderr, "converted %d of %d log rows into %d request(s) → %s\n",
		len(set.Items), len(rows), len(set.Items), *output)
}

// loadLogs reads either a JSON array of objects or JSONL (one object per line).
func loadLogs(path string) ([]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rows []map[string]string
	dec := json.NewDecoder(f)
	if err := dec.Decode(&rows); err == nil {
		return rows, nil
	}

	// fall back to JSONL
	_, _ = f.Seek(0, io.SeekStart)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var row map[string]string
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("JSONL row: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, sc.Err()
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "splunk: "+format+"\n", args...)
	os.Exit(1)
}

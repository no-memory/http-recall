# HTTP Recall <!-- omit in toc -->

[![GitHub license](https://img.shields.io/github/license/six-ddc/plow.svg)](https://github.com/six-ddc/plow/blob/main/LICENSE)
[![made-with-Go](https://img.shields.io/badge/Made%20with-Go-1f425f.svg)](http://golang.org)

HTTP Recall is an HTTP(S) benchmarking **and log-replay** tool, written in Golang. It uses
excellent [fasthttp](https://github.com/valyala/fasthttp#http-client-comparison-with-nethttp) instead of Go's default
net/http due to its lightning fast performance.

It ships two modes that share the same stats / terminal / web-chart pipeline:

- **Benchmark** — run at a specified concurrency (`-c`) and real-time record summary statistics, histogram of
  execution time and percentiles, displayed on Web UI and terminal. Run for a duration (`-d`), a fixed number of
  requests (`-n`), or until Ctrl-C.
- **Replay** — replay a timestamped request set (e.g. derived from Splunk access logs) against a target environment,
  preserving the relative timing between requests. Supports speed compression (`--speed`), window slicing
  (`--replay-start/--replay-end`), VU-bounded concurrency and failure policies. See [REPLAY.md](REPLAY.md).

The real-time histograms and quantiles use stream-based algorithms inspired
by [prometheus](https://github.com/prometheus/client_golang) with low memory and CPU bounds, so there is almost no
additional performance overhead for benchmarking.

```text
❯ httprecall http://127.0.0.1:8080/hello -c 20
Benchmarking http://127.0.0.1:8080/hello using 20 connection(s).
@ Real-time charts is listening on http://[::]:18888

Summary:
  Elapsed        8.6s
  Count        969657
    2xx        776392
    4xx        193265
  RPS      112741.713
  Reads    10.192MB/s
  Writes    6.774MB/s

Statistics    Min       Mean     StdDev      Max
  Latency     32µs      176µs     37µs     1.839ms
  RPS       108558.4  112818.12  2456.63  115949.98

Latency Percentile:
  P50     P75    P90    P95    P99   P99.9  P99.99
  173µs  198µs  222µs  238µs  274µs  352µs  498µs

Latency Histogram:
  141µs  273028  ■■■■■■■■■■■■■■■■■■■■■■■■
  177µs  458955  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■
  209µs  204717  ■■■■■■■■■■■■■■■■■■
  235µs   26146  ■■
  269µs    6029  ■
  320µs     721
  403µs      58
  524µs       3
```

Replay mode:

```text
❯ httprecall http://staging.internal --replay-file demo/replay-orders.json --speed 5 -c 200
Replaying 18 request(s) at 5.0x speed with 200 VU → http://staging.internal
```

- [Installation](#installation)
- [Usage](#usage)
    - [Benchmark mode](#benchmark-mode)
    - [Replay mode](#replay-mode)
    - [Options](#options)
    - [Examples](#examples)
- [Project layout](#project-layout)
- [Acknowledgements](#acknowledgements)
- [License](#license)

## Installation

### Build from source

```bash
git clone <this-repo> && cd http-recall
GOPROXY=https://goproxy.cn,direct go build -o httprecall ./cmd/httprecall
```

## Usage

### Benchmark mode

```bash
httprecall http://127.0.0.1:8080/ -c 20 -n 10000 -d 10s
```

### Replay mode

```bash
httprecall http://target.internal --replay-file demo/replay-orders.json --speed 2 -c 8
```

See [REPLAY.md](REPLAY.md) for the request-set format and replay options.

### Options

```bash
usage: httprecall [<flags>] <url>

An HTTP(S) benchmark and log-replay tool with real-time web UI and terminal displaying

Examples:

  httprecall http://127.0.0.1:8080/ -c 20 -n 100000
  httprecall https://httpbin.org/post -c 20 -d 5m --body @file.json -T 'application/json' -m POST
  httprecall http://127.0.0.1:8080 --replay-file demo/replay-orders.json --speed 5 -c 200

Flags:
      --help                     Show context-sensitive help.
  -c, --concurrency=1            Number of connections to run concurrently
      --rate=infinity            Number of requests per time unit, examples: --rate 50 --rate 10/ms
      --ramp-up=-1               Concurrently will increase pre seconds
  -n, --requests=-1              Number of requests to run
  -d, --duration=DURATION        Duration of test, examples: -d 10s -d 3m
  -i, --interval=200ms           Print snapshot result every interval, use 0 to print once at the end
      --seconds                  Use seconds as time unit to print
      --json                     Print snapshot result as JSON
      --replay-file=REPLAY-FILE  Replay a timestamped request set (JSON) instead of benchmarking
      --speed=1                  Replay time compression factor, e.g. 1 2 5 10 30
      --fail-policy=continue     Replay failure policy: continue|pause|abort
      --replay-start=0           Replay: skip requests before this offset, e.g. 30s 5m
      --replay-end=0             Replay: skip requests at/after this offset, e.g. 10m
  -b, --body=BODY                HTTP request body, if body starts with '@' the rest will be considered a file's path from which to read the actual body content
      --stream                   Specify whether to stream file specified by '--body @file' using chunked encoding or to read into memory
  -m, --method="GET"             HTTP method
  -H, --header=K:V ...           Custom HTTP headers
      --host=HOST                Host header
  -T, --content=CONTENT          Content-Type header
      --cert=CERT                Path to the client's TLS Certificate
      --key=KEY                  Path to the client's TLS Certificate Private Key
  -k, --insecure                 Controls whether a client verifies the server's certificate chain and host name
      --listen=":18888"          Listen addr to serve Web UI
      --timeout=DURATION         Timeout for each http request
      --dial-timeout=DURATION    Timeout for dial addr
      --req-timeout=DURATION     Timeout for full request writing
      --resp-timeout=DURATION    Timeout for full response reading
      --socks5=ip:port           Socks5 proxy
      --http-proxy=username:password@ip:port
                                 Set HTTP proxy
      --auto-open-browser        Specify whether auto open browser to show web charts
      --[no-]clean               Clean the histogram bar once its finished. Default is true
      --output-errors=OUTPUT-ERRORS
                                 Output errors to file
      --summary                  Only print the summary without realtime reports
      --unix-socket=UNIX-SOCKET  Unix domain socket path to use for connection
      --version                  Show application version.

  Flags default values also read from env HTTPRECALL_SOME_FLAG, such as HTTPRECALL_TIMEOUT=5s equals to --timeout=5s

Args:
  <url>  Request url
```

### Examples

Basic benchmark:

```bash
httprecall http://127.0.0.1:8080/ -c 20 -n 10000 -d 10s
```

POST a json file:

```bash
httprecall https://httpbin.org/post -c 20 --body @file.json -T 'application/json' -m POST
```

Replay a spike window 10x faster:

```bash
httprecall http://staging.internal --replay-file demo/replay-orders.json --speed 10 -c 500
```

## Project layout

```
cmd/httprecall/          CLI entry (benchmark + replay)
cmd/splunk/               log → request-set converter (Splunk or offline logs)
internal/httprecall/     engine: benchmark + replay schedulers, stats, terminal & web UI
internal/splunk/          Splunk client + mapping-rules converter
bench_server/            throwaway HTTP server for local testing
demo/                    sample replay request sets
preview/ tui/            design prototypes (Open Design HTML / Textual TUI)
```

## Acknowledgements

Forked from [plow](https://github.com/six-ddc/plow) (Apache-2.0, © six-ddc). The benchmark engine, real-time
stats and web charts are derived from plow; replay mode is an original addition.

## License

See [LICENSE](LICENSE).

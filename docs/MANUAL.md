# HTTP Recall 使用手册

HTTP Recall 是一个 **HTTP(S) 压测 + 日志回放**工具，基于 fasthttp 实现，单二进制、
三种实时 UI（ANSI 终端 / Bubble Tea TUI / Web）。

- **压测（Benchmark）**：按并发/速率/时长生成请求，实时统计 QPS、延迟分位、状态码。
- **回放（Replay）**：把 Splunk 等来源的访问日志转成带时间戳的请求集，按日志时间差还原
  真实请求节奏，支持倍速压缩、窗口裁剪、并发限制、失败策略。

---

## 1. 安装与构建

要求：Go ≥ 1.26（本仓库 `go.mod` 声明 `go 1.26`）。

```bash
git clone <repo> && cd http-recall

# 国内网络建议设置代理
export GOPROXY=https://goproxy.cn,direct

# 构建
go build -o httprecall ./cmd/httprecall
go build -o splunk    ./cmd/splunk
```

二进制运行：`./httprecall`（压测+回放）、`./splunk`（日志转换）。

---

## 2. 压测模式

```bash
./httprecall <url> [flags]
```

### 常用参数

| 参数 | 说明 | 默认 |
|---|---|---|
| `-c, --concurrency` | 并发连接数（= goroutine 数） | 1 |
| `-n, --requests` | 固定请求数（-1 = 不限） | -1 |
| `-d, --duration` | 持续时长，如 `10s`、`5m` | — |
| `--rate` | 速率限制，如 `50`、`10/ms` | infinity |
| `--ramp-up` | 每秒增加并发数 | -1 |
| `-m, --method` | HTTP 方法 | GET |
| `-b, --body` | 请求体（`@file` 读文件） | — |
| `-H, --header` | 请求头 `K:V`（可多次） | — |
| `-T, --content` | Content-Type | — |
| `--timeout` | 单请求超时 | — |
| `--listen` | Web UI 监听地址（空=关闭） | `:18888` |
| `--ui` | `terminal`(ANSI) / `tui`(Bubble Tea) | terminal |
| `--interval` | 终端刷新间隔 | 200ms |
| `--json` | 输出 JSON 快照 | — |
| `--summary` | 只打印汇总 | — |
| `--insecure` | 跳过 TLS 校验 | — |
| `--output-errors` | 错误输出到文件 | — |

### 示例

```bash
# 100 并发，跑 30 秒
./httprecall http://api.example.com/ -c 100 -d 30s

# 固定 10 万请求，带 JSON body
./httprecall http://api.example.com/v1/charge -c 50 -n 100000 \
  -m POST -b @body.json -T 'application/json'

# 阶梯爬坡：每秒 +20 并发直到 200
./httprecall http://api.example.com/ -c 200 --ramp-up 20 -d 2m
```

---

## 3. 回放模式

```bash
./httprecall <target-url> --replay-file <request-set.json> [flags]
```

### 请求集格式

```json
{
  "items": [
    { "at": "0ms",   "spec": { "method": "POST", "url": "/api/orders/create",
                               "headers": {"X-Trace-Id": "t-001"},
                               "body": "{\"order_id\":\"ORD-88421\"}" } },
    { "at": "449ms", "spec": { "method": "GET",  "url": "/api/orders/88421" } }
  ]
}
```

- `at`：相对集合起点的偏移（= 日志 `_time` 与最早一条的差值）。支持字符串
  （`"1500ms"` / `"3s"` / `"5m"`）或纳秒整数。
- `url`：目标路径（+query），发往 `<target-url>` 指定的主机——回放永远打到目标环境。
- 调度器按 `at/speed` 触发；超出 `--concurrency` 的请求排队，保持相对顺序。

### 回放参数

| 参数 | 说明 | 默认 |
|---|---|---|
| `--replay-file` | 请求集 JSON（触发回放模式） | — |
| `--speed` | 倍速（时间压缩）：`1` 实时、`5` 五倍速 | 1 |
| `-c, --concurrency` | 最大并发（VU），超出排队保序 | 1 |
| `--replay-start` | 跳过该偏移之前的请求，如 `30s` | 0 |
| `--replay-end` | 跳过该偏移之后的请求，如 `10m` | 0 |
| `--fail-policy` | `continue` / `pause`（每 50 失败暂停 2s）/ `abort` | continue |

### 示例

```bash
# 1:1 实时回放
./httprecall http://staging.internal --replay-file requests.json -c 200

# 10 倍速压缩回放，只看 30s~10m 窗口
./httprecall http://staging.internal --replay-file requests.json \
  --speed 10 -c 500 --replay-start 30s --replay-end 10m

# 连续失败即终止
./httprecall http://staging.internal --replay-file requests.json --fail-policy abort
```

### 回放进度

- TUI（`--ui tui`）和 Web UI（`--listen`）实时显示：
  `SENT x/y · SPEED N× · SIM 模拟时间 · VU 并发` + 进度条。
- Web 的 `/data/progress` 端点返回同数据（JSON）。

---

## 4. 从 Splunk 生成请求集（cmd/splunk）

```bash
# 方式一：直接查询 Splunk（Search API）
./splunk --url https://splunk.internal:8089 --token $SPLUNK_TOKEN \
  --query 'index=api_gateway sourcetype=access_log
           | fields _time, method, uri_path, trace_id, client_ip, order_id
           | sort _time asc' \
  --rules demo/rules.json --output requests.json

# 方式二：转换离线日志导出（JSON 数组或 JSONL）
./splunk --logs-file logs.json --rules demo/rules.json --output requests.json
```

### 映射规则（demo/rules.json）

对应产品设计稿的 Request Mapping 页：每条规则 = 匹配条件 + 请求模板。

```json
{
  "rules": [
    {
      "id": "R01-create-order",
      "match": { "method": "POST", "path_regex": "^/api/orders/create$" },
      "template": {
        "method": "LOG",
        "url": "/api/orders/create?trace_id=${trace_id}",
        "headers": { "X-Client-IP": "${client_ip}", "X-Trace-Id": "${trace_id}" },
        "body": "{\"order_id\":\"${order_id}\",\"amount\":${amount}}"
      },
      "skip_missing": true
    }
  ],
  "method_field": "method",
  "path_field": "uri_path",
  "time_field": "_time"
}
```

- `match.method`（空=任意）、`match.path_regex`（空=任意）
- `template` 中的 `${field}` 自动绑定日志字段；`method: "LOG"` 表示沿用日志里的方法
- 缺失字段：`skip_missing: true` 跳过该行，否则用 `default_value`
- 时间：`_time` 支持 RFC3339 / `2006-01-02 15:04:05` / epoch 秒 / epoch 毫秒；
  偏移以**全部日志中最早的时间**为基准（乱序输入也能算出正确偏移）

---

## 5. 实时 UI

### 5.1 ANSI 终端（默认 `--ui terminal`）

实时打印 KPI、直方图、分位，plow 原版风格。

### 5.2 Bubble Tea TUI（`--ui tui`）

```
HTTP·RECALL  REPLAY  MARKET TAPE [ERRORS]
REPLAY PROGRESS  ████████████░░░░░ 80%   SENT 4/5 SPEED 2× SIM 3.6s VU 10
ELAPSED / COUNT / RPS / SUCCESS / CONC
LATENCY PERCENTILES    STATUS CODES    QPS — LAST 60s    REQUEST TIMELINE
REQUEST TAPE / ERRORS（独立视图）
tab / ← → 切换 · 1 2 3 跳转 · q 退出
```

- **MARKET**：KPI + 分位 + 状态码 + QPS sparkline + 回放进度 + 方法着色时间轴
- **TAPE**：全屏请求流（30 条，方法/路径/状态/耗时）
- **ERRORS**：错误计数 + 最近失败请求
- **REQUEST TIMELINE**：方法色块（GET 青 / POST 绿 / PUT 琥珀 / DEL 珊瑚）+ 状态色块
  （2xx 绿 / 4xx 琥珀 / 5xx 珊瑚），旧→新从左到右
- 回放/压测结束自动退出；`q` / `ctrl+c` 手动退出

### 5.3 Web UI（`--listen :18888`）

浏览器打开 `http://localhost:18888`。自包含单页，零外部依赖，每秒轮询 `/data/*`：

- ticker：RPS / 并发 / 延迟均值 / P99 / 2xx / 5xx
- REPLAY PROGRESS：回放进度条（仅回放模式）
- REQS/SEC：canvas 面积图（RPS）+ 延迟线，60 秒窗口
- REQUEST TIMELINE：方法/状态色块序列（最新在右）
- RESPONSE STATUS CODES：2xx/3xx/4xx/5xx 卡片（百分比 + 柱条）

数据端点（JSON，1s 粒度）：`/data/rps`、`/data/latency`、`/data/code`、
`/data/concurrency`、`/data/progress`、`/data/methods`。

---

## 6. 多核与性能

- `go.uber.org/automaxprocs` 在 `init()` 自动设置 `GOMAXPROCS` = 容器可用 CPU 数
  （k8s CPU quota 感知）。
- 并发模型：`-c N` 创建 N 个 goroutine，每个阻塞在 fasthttp `Do()`；fasthttp
  `HostClient` 维护 N 连接池，每个连接一个 reader goroutine 做 HTTP 解析（CPU 密集），
  由 Go 调度器分布到多核。
- 实测（32 核）：`GOMAXPROCS=1` ≈ 177k RPS，默认 ≈ 320k RPS（≈1.8×）。
- 调优建议：`-c` 从 4×核心数起步扫拐点；本机压测目标用 `bench_server`：

```bash
go run ./bench_server -p 18080
./httprecall http://127.0.0.1:18080/ -c 100 -d 10s --ui tui
```

---

## 7. 项目结构

```
cmd/httprecall/          CLI 入口（kingpin flags → Config）
cmd/splunk/              日志 → 请求集转换器
internal/httprecall/     引擎：压测/回放调度器、统计、终端 & Web UI
internal/splunk/         Splunk REST 客户端 + 映射规则转换器
internal/tui/            Bubble Tea TUI
bench_server/            本地压测目标服务器
demo/                    rules.json / logs-sample.json / replay-orders.json
docs/                    Web UI 截图等
REPLAY.md                回放模式速查
```

## 8. 开发与测试

```bash
export GOPROXY=https://goproxy.cn,direct
go build ./...
go test ./...
go vet ./...
```

覆盖：压测/回放调度、统计（分位/直方图）、Splunk 转换（规则绑定/时间差/乱序）、
TUI（渲染/视图切换/退出）、Web（数据端点 JSON 结构）。

---

## 9. FAQ

**Q：回放和压测能同时跑吗？**
A：当前一条命令一个模式。需要同时时，开两个进程（注意 `--listen` 端口错开）。

**Q：请求集很大（几十万条）会怎样？**
A：回放调度为"每请求一个 goroutine + 定时器"，Go 运行时能扛数十万级定时器；
VU 槽限制并发，超出排队保序。超大集合建议先用 `--replay-start/--replay-end` 切片。

**Q：TUI 在 `script`/CI 管道里不渲染？**
A：Bubble Tea v2 的渲染器需要真实 TTY（终端能力协商）。管道/CI 请用默认 ANSI
模式（`--ui terminal`）或 `--summary`。

**Q：怎么接真实 Splunk？**
A：`splunk --url ... --token ... --query ... --rules ... --output requests.json`，
然后把 `requests.json` 交给 `httprecall --replay-file`。认证用
`Authorization: Splunk <token>`（环境变量 `SPLUNK_TOKEN`）。

**Q：为什么回放目标总是指向 `--url`？**
A：这是特性——日志来自生产，回放打到 staging/本地，避免误伤线上。请求集里的
`url` 是路径，主机由命令行目标决定。

---

## 10. 致谢与许可

Fork 自 [plow](https://github.com/six-ddc/plow)（Apache-2.0, © six-ddc）。
压测引擎、实时统计、Web 图表接口源自 plow；回放调度、Splunk 转换、Bubble Tea TUI
为本项目新增。许可见根目录 `LICENSE`。

# Replay mode (新增功能)

在 plow 压测能力之上新增的**请求回放**模式：按日志 `_time` 时间差还原真实请求节奏，
可倍速压缩、裁剪窗口、限制并发，全部复用 plow 的统计 / 终端 / Web 图表管线。

## 用法

```bash
# 构建
GOPROXY=https://goproxy.cn,direct go build -o plow .

# 回放请求集（默认 1x 倍速）
./plow http://target.internal --replay-file demo/replay-orders.json

# 2x 倍速 + 8 并发 + 只看 30s~10m 窗口
./plow http://target.internal \
  --replay-file demo/replay-orders.json \
  --speed 2 --concurrency 8 \
  --replay-start 30s --replay-end 10m

# 失败即终止 / 连续失败暂停
./plow http://target.internal --replay-file set.json --fail-policy abort
./plow http://target.internal --replay-file set.json --fail-policy pause

# 实时 Web 图表（默认 :18888）
./plow http://target.internal --replay-file set.json --listen :18888
```

## 请求集格式

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

- `at`：相对集合起点的偏移，即日志 `_time` 与最早一条的差值；支持 `"1500ms"` / `"3s"` / `"5m"` 或纳秒整数
- `url`：目标路径（+query），发往 `--url` 指定的目标 host（回放永远打到目标环境）
- 请求按 `at` 升序，调度器按 `at/speed` 触发；超出 `--concurrency` 的请求排队，保持相对顺序

## 与压测模式的关系

| | 压测（原 plow） | 回放（新增） |
|---|---|---|
| 调度模型 | goroutine 池 + rate limiter | 时间戳序列 + 倍速压缩 |
| 请求模板 | 单一 URL/method/body | 每请求独立 spec |
| 统计/报告/UI | 共用 | 共用（同一 recordChan 管线） |

## 从 Splunk 生成请求集

```spl
index=api_gateway sourcetype=access_log
| where status_code > 0 AND duration > 0
| fields _time, method, uri_path, query, status_code, duration
| sort _time asc
```

导出后按映射规则（见设计稿 Request Mapping 页）把日志字段绑定为 `spec` 即可。

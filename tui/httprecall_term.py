"""
HTTP Recall TERM — Splunk log replay & load test, as a terminal app.

Run (from this directory):
    uv run --with textual python httprecall_term.py
or:
    pip install textual && python httprecall_term.py

Keys: Tab / Shift+Tab cycle panes · arrows move in tables · q quits
The command prompt at the bottom accepts: replay | loadtest | history ...
"""
from __future__ import annotations

import random
from datetime import datetime

from textual import on
from textual.app import App, ComposeResult
from textual.containers import Grid, Horizontal, Vertical, VerticalScroll
from textual.widgets import (
    DataTable,
    Footer,
    Header,
    Input,
    Label,
    Sparkline,
    Static,
    TabbedContent,
    TabPane,
)

# ── palette (trading-terminal) ──────────────────────────────────────────
BG = "#070b12"
FG = "#e6edf3"
CYAN = "#38bdf8"
BUY = "#22c55e"
SELL = "#ff4757"
WARN = "#f59e0b"
DIM = "#5f7186"

QUOTES = [
    ("ORD-RP1", "REPLAY", "2,340", "152ms", "+4.2%", "RUN"),
    ("PAY-LT3", "LOAD", "5,120", "298ms", "+12.1%", "RUN"),
    ("USR-RP2", "REPLAY", "1,840", "98ms", "-1.8%", "DONE"),
    ("INV-LT1", "LOAD", "3,040", "1204ms", "+304%", "FAIL"),
    ("SRC-LT2", "LOAD", "8,880", "244ms", "+2.0%", "DONE"),
    ("TRD-RP3", "REPLAY", "2,010", "176ms", "-0.5%", "DONE"),
]

TAPE_PATHS = ["/api/orders/create", "/api/orders/88421", "/v1/charge",
              "/api/cart/items/77", "/api/user/8812/cart"]


def now_ms() -> str:
    return datetime.now().strftime("%H:%M:%S")


class Ticker(Static):
    """Top marquee: task QPS / P99 / error deltas."""

    def on_mount(self) -> None:
        self.set_interval(2.0, self.refresh_ticker)
        self.refresh_ticker()

    def refresh_ticker(self) -> None:
        items = [
            ("ORD-RP1", "2340 QPS", "+4.2%", BUY),
            ("PAY-LT3", "5120 QPS", "+12.1%", BUY),
            ("USR-RP2", "1840 QPS", "-1.8%", BUY),
            ("INV-LT1", "3040 QPS", "+304%", SELL),
            ("P99 PAY-LT3", "298ms", "+41ms", SELL),
            ("ERR RATE", "1.88%", "+0.3%", SELL),
            ("SUCCESS", "99.82%", "+0.21%", BUY),
        ]
        line = "  │  ".join(f"[b]{s}[/b] {d} [{c}]{chg}[/{c}]" for s, d, chg, c in items)
        self.update(f"[{CYAN}]HTTP·RECALL[/]  │  {line}")


class QuotesTable(DataTable):
    def on_mount(self) -> None:
        self.cursor_type = "row"
        self.add_columns("SYM", "TYPE", "QPS", "P99", "ΔP99", "STS")
        for row in QUOTES:
            self.add_row(*row)


class ErrorBook(Static):
    def on_mount(self) -> None:
        self.update(self.render_book())
        self.set_interval(3.0, lambda: self.update(self.render_book()))

    def render_book(self) -> str:
        rows = [
            (now_ms(), "/v1/charge", f"[{SELL}]9752[/]", 58),
            (now_ms(), "/stock/query", f"[{WARN}]5212[/]", 31),
            (now_ms(), "/cart/sync", f"[{WARN}]1847[/]", 11),
        ]
        return "\n".join(
            f"{t}  {p:<16} {c:>8}" for t, p, c, _ in rows
        )


class Tape(Static):
    """Live request tape — buy green / sell coral rows."""

    def on_mount(self) -> None:
        self._lines: list[str] = []
        self.set_interval(1.1, self.push)
        for _ in range(6):
            self.push()

    def push(self) -> None:
        ok = random.random() > 0.08
        color = BUY if ok else SELL
        st = "200" if ok else "5" + str(random.randint(0, 2))
        path = random.choice(TAPE_PATHS)
        line = f"{now_ms()}  [{color}]▮[/] {path:<28} [{color}]{st}[/] {random.randint(30, 120)}ms"
        self._lines.insert(0, line)
        self.update("\n".join(self._lines[:10]))


class Percentiles(Static):
    def on_mount(self) -> None:
        self.update(self.body())

    def body(self) -> str:
        rows = [
            ("P50", 34, "86ms", BUY),
            ("P95", 52, "204ms", CYAN),
            ("P99", 71, "298ms", WARN),
            ("TIMEOUT", 9, "0.4%", SELL),
        ]
        out = []
        for name, pct, val, color in rows:
            fill = "█" * (pct // 5)
            out.append(f"{name:<9} [{color}]{fill:<20}[/] {val:>7}")
        return "\n".join(out)


class ReplayPane(VerticalScroll):
    def compose(self) -> ComposeResult:
        yield Label("[b]REPLAY TAPE[/] — order-svc 14:02 spike · 1×  ·  [cyan]RUNNING[/]", id="rp-title")
        yield Static("", id="rp-tape")
        yield Static("SENT: 12,034 / 18,420   OK: 99.94%   QPS: 2,340   AVG: 152ms", id="rp-stats")
        yield Static("", id="rp-tl")

    def on_mount(self) -> None:
        self._lines: list[str] = []
        self.set_interval(0.9, self.tick)

    def tick(self) -> None:
        samples = [
            ("POST", "/api/orders/create", "201", 84),
            ("GET", "/api/orders/88421", "200", 41),
            ("PUT", "/api/orders/88421/status", "202", 118),
            ("GET", "/api/orders/88421/pay", "500", 1204),
        ]
        m, p, s, ms = random.choice(samples)
        color = BUY if s.startswith("2") else SELL
        tape = self.query_one("#rp-tape", Static)
        self._lines.insert(0, f"{now_ms()}  [{color}]{m:<4}[/] {p:<26} [{color}]{s}[/] {ms}ms")
        tape.update("\n".join(self._lines[:9]))

        pct = random.randint(65, 88)
        bar = "█" * (pct // 4)
        tl = self.query_one("#rp-tl", Static)
        tl.update(f"[{CYAN}]{bar:<24}[/] {pct}%  14:02:00 ────────▶ 14:11:00")


class LoadPane(VerticalScroll):
    def compose(self) -> ComposeResult:
        yield Label("[b]LOAD TEST[/] — pay-gateway staged ramp ·  [cyan]RUNNING[/]", id="lt-title")
        yield Label("QPS: 5,120   VU: 500   ERR: 1.88%   ELAPSED: 04:12/10:00", id="lt-stats")
        yield Sparkline([], summary_function=max, id="lt-spark")
        yield Static("", id="lt-tape")

    def on_mount(self) -> None:
        self._series = [3100, 3400, 3800, 4200, 4400, 4700, 4950, 5120]
        self._lines: list[str] = []
        self.set_interval(1.2, self.tick)

    def tick(self) -> None:
        self._series.append(5000 + random.randint(-320, 320))
        self._series = self._series[-40:]
        spark = self.query_one("#lt-spark", Sparkline)
        spark.data = self._series
        ok = random.random() > 0.06
        st = 200 if ok else (502 if random.random() > 0.5 else 429)
        color = BUY if ok else SELL
        chk = "PASS" if ok else ("RATE" if st == 429 else "FAIL")
        tape = self.query_one("#lt-tape", Static)
        self._lines.insert(0, f"{now_ms()}  [{color}]POST[/] /v1/charge  [{color}]{st}[/] {random.randint(80, 2000)}ms  [{color}]{chk}[/]")
        tape.update("\n".join(self._lines[:9]))


class CmdBar(Input):
    """Bottom command prompt: type a pane name + Enter."""


class HTTPRecallTerm(App):
    """Market-terminal re-implementation of the HTTP Recall dashboard."""

    TITLE = "HTTP RECALL TERM"
    SUB_TITLE = "splunk logs → replay → load test"
    CSS = f"""
    Screen {{
        background: {BG};
        color: {FG};
    }}
    #ticker {{
        background: #060a10;
        color: {DIM};
        padding: 0 2 0 2;
        height: 1;
        border-bottom: solid {DIM};
    }}
    TabbedContent {{
        height: 1fr;
    }}
    TabPane {{
        padding: 1 2;
    }}
    #market-grid {{
        grid-size: 3;
        grid-columns: 1fr 2fr 1fr;
        grid-gutter: 1 2;
    }}
    .box {{
        border: round {DIM};
        background: #0d1522;
        padding: 0 1;
        height: auto;
    }}
    .box-title {{
        color: {CYAN};
        text-style: bold;
    }}
    DataTable {{
        height: auto;
        max-height: 14;
    }}
    Sparkline {{
        height: 5;
    }}
    #rp-tape, #lt-tape, #tape {{
        height: auto;
    }}
    #cmd {{
        dock: bottom;
        height: 3;
        border-top: solid {DIM};
        background: #0a1019;
    }}
    """

    def compose(self) -> ComposeResult:
        yield Header(show_clock=True)
        yield Ticker("", id="ticker")
        with TabbedContent(initial="market"):
            with TabPane("MARKET", id="market"):
                with Grid(id="market-grid"):
                    with Vertical():
                        yield Label("RUN WATCH", classes="box-title")
                        yield QuotesTable(classes="box")
                        yield Label("ERROR BOOK", classes="box-title")
                        yield ErrorBook(classes="box")
                    with Vertical():
                        yield Label("THROUGHPUT — REPLAY & LOAD", classes="box-title")
                        yield Sparkline([120, 240, 380, 520, 760, 980, 1150, 1320, 1480,
                                         1610, 1580, 1490, 1560, 1720, 1880, 2050, 2240,
                                         2380, 2450, 2320, 2180, 2260, 2140], id="main-spark",
                                        summary_function=max, classes="box")
                        yield Label("REQUEST TAPE — LIVE", classes="box-title")
                        yield Tape(classes="box")
                    with Vertical():
                        yield Label("LATENCY PERCENTILES", classes="box-title")
                        yield Percentiles(classes="box")
                        yield Label("GLOBAL 24H", classes="box-title")
                        yield Static(
                            f"REQUESTS   1,284,560\nSUCCESS    [{BUY}]99.82%[/]\nACTIVE     [{CYAN}]3[/] RUNS",
                            classes="box",
                        )
            with TabPane("SPL SEARCH", id="spl"):
                yield Static(
                    "[dim]# extract gateway access logs for replay[/]\n"
                    "[cyan]index[/]=api_gateway [cyan]sourcetype[/]=access_log\n"
                    "[cyan]|[/] [cyan]where[/] status_code > 0 [cyan]AND[/] duration > 0\n"
                    "[cyan]|[/] [cyan]fields[/] _time, client_ip, method, uri_path, query\n"
                    "[cyan]|[/] [cyan]sort[/] _time [cyan]asc[/]\n\n"
                    "HITS: 18,420   MATCH: 96.2%   CONVERTIBLE: 16,210",
                    id="spl-out", classes="box",
                )
                yield Static(
                    f"FIELDS: [cyan]_time[/] [cyan]client_ip[/] [cyan]method[/] [cyan]uri_path[/] "
                    f"[cyan]query[/] [cyan]status_code[/] [cyan]duration[/] [{BUY}]order_id[/]",
                    id="spl-fields", classes="box",
                )
            with TabPane("MAPPING", id="mapping"):
                yield Static(
                    "[b]R01 CREATE ORDER[/]  [cyan]POST /api/orders/create[/]  [green]ENABLED[/]\n"
                    "    MATCH: method=POST & path=/api/orders/create\n"
                    "    TPL:   POST /api/orders/create?${trace_id}\n"
                    "    vars:  ${order_id}←field · ${trace_id}←field · ${target_host}←env\n\n"
                    "[b]R02 GET ORDER[/]     [cyan]GET /api/orders/{id}[/]  [green]ENABLED[/]\n"
                    "    MATCH: path=/api/orders/\\d+ & method=GET\n"
                    "    TPL:   GET /api/orders/${order_id}   (regex capture)\n\n"
                    "[b]R03 CART UPDATE[/]   [cyan]PUT /api/cart[/]  [yellow]REVIEW[/]\n"
                    "    3% missing item_id → skipped\n\n"
                    "[b]R04 PAY CALLBACK[/]  [cyan]POST /v1/charge[/]  [green]ENABLED[/]\n"
                    "    body passthrough",
                    id="mapping-out", classes="box",
                )
            with TabPane("REPLAY", id="replay"):
                yield ReplayPane()
            with TabPane("LOAD TEST", id="loadtest"):
                yield LoadPane()
            with TabPane("HISTORY", id="history"):
                yield Static("", id="hist-table", classes="box")
        yield CmdBar(placeholder="❯ replay | loadtest | mapping | history …", id="cmd")
        yield Footer()

    def on_mount(self) -> None:
        table = self.query_one("#hist-table", Static)
        rows = [
            ("PAY-LT3", "LOAD", "RUN", "892,005", "98.12", "298", "5,120"),
            ("ORD-RP1", "REPLAY", "RUN", "421,882", "99.94", "152", "2,340"),
            ("INV-LT1", "LOAD", "FAIL", "214,300", "91.37", "1204", "3,040"),
            ("USR-RP2", "REPLAY", "DONE", "96,120", "100.00", "98", "1,840"),
            ("SRC-LT2", "LOAD", "DONE", "1,208,442", "99.71", "244", "8,880"),
        ]
        lines = ["[b]SYM       TYPE    STS    REQ        OK%     P99    QPS[/]"]
        for r in rows:
            stc = BUY if r[2] == "DONE" else SELL if r[2] == "FAIL" else CYAN
            okc = BUY if float(r[4]) >= 99 else WARN
            lines.append(f"{r[0]:<10}{r[1]:<8}[{stc}]{r[2]:<6}[/]{r[3]:>9}  "
                         f"[{okc}]{r[4]:>6}[/]{r[5]:>6}{r[6]:>8}")
        table.update("\n".join(lines))

    @on(CmdBar.Submitted)
    def on_cmd(self, event: CmdBar.Submitted) -> None:
        cmd = event.value.strip().lower()
        tabs = self.query_one(TabbedContent)
        targets = {
            "market": "market", "1": "market",
            "spl": "spl", "search": "spl", "2": "spl",
            "mapping": "mapping", "3": "mapping",
            "replay": "replay", "4": "replay",
            "loadtest": "loadtest", "load": "loadtest", "5": "loadtest",
            "history": "history", "6": "history",
        }
        if cmd in targets:
            tabs.active = targets[cmd]
            event.input.value = ""
        elif cmd in ("q", "quit", "exit"):
            self.exit()
        else:
            event.input.value = ""


if __name__ == "__main__":
    HTTPRecallTerm().run()

# OAI Response Meter

OAI Response Meter is a local usage observer for OpenAI Responses API traffic.
It is built for cases where you want token and cost visibility without changing
the client, SDK, endpoint, request body, or application code.

Unlike tools that act as an OpenAI-compatible endpoint, this project sits beside
normal traffic as an explicit HTTPS proxy powered by `mitmdump`. The client still
believes it is talking to the original service, and the upstream service still
sees the usual client request shape. There is no extra "third-party client"
request pattern introduced by a relay endpoint, and no need to teach every
caller about a new base URL.

The proxy behaves as a faithful local listener: it watches completed Responses
API messages, extracts only usage metadata, and lets the original request flow
continue normally. A thin Python addon handles the mitmproxy integration and
sends best-effort Unix datagram events to a Go daemon, which writes SQLite and
JSONL records and serves the embedded dashboard.

## Current Shape

- `mitm/addon.py` observes HTTPS JSON, SSE, and WebSocket completed Responses
  API responses.
- `oai-meter run` starts the Go daemon and wraps `mitmdump`.
- `oai-meter run` also starts a local dashboard at `http://127.0.0.1:8081`
  by default.
- Usage records are written to `data/usage.db` and `data/usage.jsonl` by default.
- The project assumes a compatible `mitmdump` binary already exists at
  `bin/mitmdump`, or that you pass `--mitmdump`.

## Install mitmdump

For v1, place the executable manually:

```bash
mkdir -p bin
cp /path/to/mitmdump bin/mitmdump
chmod +x bin/mitmdump
```

The wrapper resolves mitmdump in this order:

1. `--mitmdump /path/to/mitmdump`
2. `./bin/mitmdump`
3. `mitmdump` from `PATH`

## Run

```bash
go run ./cmd/oai-meter run --listen-host 127.0.0.1 --listen-port 8080
```

Then configure the client or system proxy to use:

```text
http://127.0.0.1:8080
```

When the command starts successfully, it also prints a dashboard URL such as:

```text
dashboard: http://127.0.0.1:8081
```

mitmproxy certificate trust is still required for HTTPS MITM. Follow the normal
mitmproxy certificate setup for your OS.

Useful flags:

```text
--mitmdump      explicit mitmdump binary path
--socket        Unix datagram socket path, default /tmp/oai-meter.sock
--db            SQLite path, default data/usage.db
--jsonl         JSONL audit path, default data/usage.jsonl
--config        JSON config file path
--prices        JSON price catalog path, default configs/prices.json
--upstream-proxy upstream explicit HTTP(S) proxy URL
--listen-host   mitmdump listen host, default 127.0.0.1
--listen-port   mitmdump listen port, default 8080
--no-dashboard  disable the local dashboard
--dashboard-host dashboard listen host, default 127.0.0.1
--dashboard-port dashboard listen port, default 8081
--queue-size    Python addon queue size, default 10000
--verbose       print sanitized debug logs
```

`--verbose` prints local meter logs such as the selected paths, received usage
events, token counts, invalid datagrams, and SQLite batch write summaries. It
does not print prompts, request bodies, response bodies, Authorization headers,
cookies, or full WebSocket messages.

The wrapper starts `mitmdump` in quiet mode so raw mitmproxy traffic logs do not
mix into the meter output.

## Upstream Proxy

If your machine needs a proxy to reach OpenAI, configure an upstream proxy. The
wrapper passes it to mitmdump as:

```text
--mode upstream:<proxy-url>
```

Resolution order:

1. `--upstream-proxy`
2. JSON config file passed with `--config`
3. Environment variables:
   `OAI_METER_UPSTREAM_PROXY`, `HTTPS_PROXY`, `https_proxy`, `HTTP_PROXY`,
   `http_proxy`, `ALL_PROXY`, `all_proxy`
4. No upstream proxy, using mitmproxy's regular explicit HTTP(S) proxy mode

Example:

```bash
go run ./cmd/oai-meter run \
  --listen-port 8080 \
  --upstream-proxy http://127.0.0.1:7890
```

Config file example:

```json
{
  "upstream_proxy": "http://127.0.0.1:7890",
  "prices": "configs/prices.json"
}
```

Only `http://` and `https://` upstream proxies are accepted for mitmproxy
upstream mode.

## Data Policy

The meter records only usage metadata:

- timestamp
- transport
- host and path
- response id
- previous response id and chain root response id
- model
- input, output, total, cached, and reasoning token counts
- observed Codex rate-limit metadata, when an internal `codex.rate_limits`
  WebSocket event is present

The scope is intentionally narrow:

- `api.openai.com/v1/responses`
- `chatgpt.com/backend-api/codex`

The mitmproxy wrapper also sets `allow_hosts` to `api.openai.com` and
`chatgpt.com` by default. Other HTTPS traffic is not intercepted by mitmproxy's
HTTP/WebSocket addon path; when upstream mode is configured, that traffic is
still forwarded through the configured upstream proxy.

It does not persist Authorization headers, cookies, prompts, request bodies,
response bodies, generated content, or full WebSocket messages.

The dashboard API is read-only and exposes only the same stored metadata:
timestamps, transport, host, path, response IDs, model, and token counts.

`codex.rate_limits` is not a documented OpenAI API event. When it appears, the
meter stores it best-effort in a separate table with the raw JSON and extracted
window reset timestamps. If expected fields are missing, the event is skipped
and `--verbose` logs a short hint.

## Dashboard

The embedded dashboard polls every 5 seconds and includes:

- overview KPI cards
- token or estimated cost KPI cards
- token or estimated cost trend chart
- model breakdown chart
- conversation chain rollups
- raw usage event table

No prompt, request body, response body, generated content, or message text is
rendered by the dashboard.

Estimated cost uses `configs/prices.json` by default and can be overridden with
`--prices`. Missing price files disable cost estimates, and missing model rates
mark only those tokens as unpriced. Prices are stored as USD per 1M tokens and
are meant to be edited by the user when OpenAI pricing changes.

## Proxy Overhead Check

To estimate the overhead added to ordinary non-target HTTPS traffic, compare a
direct baseline with the same request through the local mitmproxy listener. The
examples below use `hey` and public test endpoints; results depend on your
network and on the public endpoint's own tail latency.

By default, the wrapper sets mitmproxy `allow_hosts` to only `api.openai.com`
and `chatgpt.com`. The non-target requests below should therefore pass through
mitmproxy without HTTP/WebSocket interception or addon parsing. If upstream mode
is configured, they still use that upstream proxy.

Small requests:

```bash
hey -z 30s -c 50 https://httpbingo.org/get

hey -z 30s -c 50 \
  -x http://127.0.0.1:8080 \
  https://httpbingo.org/get
```

Large responses:

```bash
hey -z 30s -c 10 https://httpbingo.org/bytes/262144

hey -z 30s -c 10 \
  -x http://127.0.0.1:8080 \
  https://httpbingo.org/bytes/262144
```

Dense small-request load:

```bash
hey -z 60s -c 200 https://httpbingo.org/get

hey -z 60s -c 200 \
  -x http://127.0.0.1:8080 \
  https://httpbingo.org/get
```

Compare these fields between direct and proxied runs:

- `Requests/sec`
- `Average`
- p95 and p99 in `Latency distribution`
- non-2xx/3xx status codes

These checks intentionally exercise non-target traffic. For such requests the
measured overhead should mostly be mitmproxy's tunnel forwarding and connection
handling, not TLS interception, WebSocket/HTTP event handling, or usage
extraction.

## Frontend Development

The frontend source lives in `frontend/` and builds into
`internal/dashboard/static/` for Go embedding.

```bash
cd frontend
npm install
npm run build
```

## Development Checks

```bash
go test ./...
cd frontend && npm run typecheck
cd frontend && npm run build
python3 -m py_compile mitm/addon.py mitm/addon_test.py
python3 -m unittest discover -s mitm -p '*_test.py'
```

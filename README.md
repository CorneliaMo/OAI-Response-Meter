# OAI Response Meter

Local usage metering for OpenAI Responses API traffic observed through
`mitmdump`.

This project keeps mitmproxy as the MITM engine and uses a thin Python addon to
extract usage metadata. The addon sends best-effort Unix datagram events to a Go
daemon, which writes SQLite and JSONL records.

## Current Shape

- `mitm/addon.py` observes HTTPS JSON, SSE, and WebSocket completed responses.
- `oai-meter run` starts the Go daemon and wraps `mitmdump`.
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

mitmproxy certificate trust is still required for HTTPS MITM. Follow the normal
mitmproxy certificate setup for your OS.

Useful flags:

```text
--mitmdump      explicit mitmdump binary path
--socket        Unix datagram socket path, default /tmp/oai-meter.sock
--db            SQLite path, default data/usage.db
--jsonl         JSONL audit path, default data/usage.jsonl
--listen-host   mitmdump listen host, default 127.0.0.1
--listen-port   mitmdump listen port, default 8080
--queue-size    Python addon queue size, default 10000
--verbose       print sanitized debug logs
```

`--verbose` prints local meter logs such as the selected paths, received usage
events, token counts, invalid datagrams, and SQLite batch write summaries. It
does not print prompts, request bodies, response bodies, Authorization headers,
cookies, or full WebSocket messages.

The wrapper starts `mitmdump` in quiet mode so raw mitmproxy traffic logs do not
mix into the meter output.

## Data Policy

The meter records only usage metadata:

- timestamp
- transport
- host and path
- response id
- model
- input, output, total, cached, and reasoning token counts

It does not persist Authorization headers, cookies, prompts, request bodies,
response bodies, generated content, or full WebSocket messages.

## Development Checks

```bash
go test ./...
python3 -m py_compile mitm/addon.py mitm/addon_test.py
python3 -m unittest discover -s mitm -p '*_test.py'
```

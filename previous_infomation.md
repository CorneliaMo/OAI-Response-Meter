# OpenAI Responses API Metering Notes

This project is archived as an implementation experiment. The notes below summarize what was learned about metering OpenAI Responses API traffic through a local MITM proxy.

## Traffic Shapes

### HTTPS JSON

API key mode normally uses:

```text
https://api.openai.com/v1/responses
```

Codex ChatGPT-login mode may use:

```text
https://chatgpt.com/backend-api/codex
```

For non-streaming Responses API calls, usage is available in the final JSON response object:

```json
{
  \"id\": \"resp_...\",
  \"model\": \"gpt-...\",
  \"usage\": {
    \"input_tokens\": 123,
    \"output_tokens\": 456,
    \"total_tokens\": 579,
    \"input_tokens_details\": {
      \"cached_tokens\": 0
    },
    \"output_tokens_details\": {
      \"reasoning_tokens\": 12
    }
  }
}
```

Older or adjacent API shapes may use Chat Completions names:

- `prompt_tokens` instead of `input_tokens`
- `completion_tokens` instead of `output_tokens`
- `prompt_tokens_details.cached_tokens`
- `completion_tokens_details.reasoning_tokens`

### SSE Streaming

Responses streaming over HTTPS uses Server-Sent Events. The proxy sees an HTTP response with `Content-Type: text/event-stream`.

The useful event is:

```text
event: response.completed
data: {...}
```

The `data:` JSON contains a completed response object, usually under `response`:

```json
{
  \"type\": \"response.completed\",
  \"response\": {
    \"id\": \"resp_...\",
    \"model\": \"gpt-...\",
    \"usage\": {
      \"input_tokens\": 123,
      \"output_tokens\": 456,
      \"total_tokens\": 579
    }
  }
}
```

For metering, ignore deltas and intermediate output events. Do not add token counts from partial events. Extract usage once from `response.completed`.

### WebSocket Mode

Responses API also has a WebSocket transport. OpenAI's WebSocket mode documentation describes the endpoint as:

```text
wss://api.openai.com/v1/responses
```

The client sends JSON events such as:

```json
{
  \"type\": \"response.create\",
  \"response\": {
    \"model\": \"gpt-...\",
    \"input\": \"...\"
  }
}
```

The docs state that server events match the existing Responses streaming event model. In practice, that means the same metering rule applies: observe server-to-client text messages and extract usage from `response.completed`.

Do not treat WebSocket traffic as an HTTP response body. After the `101 Switching Protocols` response, the connection becomes a bidirectional WebSocket frame stream. A proxy must parse WebSocket messages or use a proxy framework that exposes WebSocket message hooks.

## Usage Scope

`response.completed.response.usage` should be treated as the final usage for that one response object.

It is not:

- a delta for a single streamed chunk
- a running total for a whole conversation
- a cumulative total for every response linked by `previous_response_id`

If a client sends another `response.create`, even with `previous_response_id`, that creates another response with its own usage. Record that response separately.

Important nuance: with `previous_response_id`, the next response's `input_tokens` may include context that the model processes from prior turns. It is still the usage for the current response, not only the newly typed user text.

Recommended record key:

```text
response_id
```

Use it for de-duplication. If the same `response.completed` is observed twice, record usage once.

## What To Record

For a metering proxy, record only metadata and token accounting:

- timestamp
- method
- host/path
- status code, when applicable
- transport: `https-json`, `sse`, or `websocket`
- response id
- model
- error, when applicable
- duration
- `input_tokens`
- `output_tokens`
- `total_tokens`
- `cached_tokens`
- `reasoning_tokens`

Do not record:

- `Authorization`
- API keys
- cookies
- request body / prompt
- response body / generated content
- WebSocket client messages
- WebSocket server text, except transient parsing for usage extraction

For an archive-quality implementation, remove body-capture flags and database columns entirely instead of keeping disabled body fields around.

## MITM Proxy Lessons

### Do Not Hand-Roll HTTP Response Serialization

An early implementation manually handled CONNECT, TLS, request parsing, upstream RoundTrip, and response serialization.

That caused a curl hang:

- client to proxy spoke HTTP/1.x
- proxy to OpenAI received an HTTP/2 response
- proxy wrote an invalid `HTTP/2.0 401 ...` status line back to an HTTP/1.x client
- proxy removed `Content-Length`
- proxy wrote `Connection: close` but did not actually close the TLS connection
- curl waited for EOF until interrupted

Use a mature proxy core for CONNECT, MITM, HTTP/1.1 response writing, chunking, and connection lifecycle.

### `goproxy` Is Fine For HTTP/S, Weak For WebSocket Metering

`github.com/elazarl/goproxy` supports:

- HTTP proxying
- CONNECT
- HTTPS MITM
- request and response hooks
- WebSocket tunneling/proxying internally

It does not expose a clean WebSocket message hook. Wrapping `resp.Body` in a normal response hook breaks WebSocket upgrade handling because goproxy expects the `101 Switching Protocols` body to also implement `io.ReadWriter`.

Observed failure:

```text
Unable to use Websocket connection
WebSocket protocol error: Handshake not finished
```

Fix for pass-through:

- detect `101 Switching Protocols`
- or `Connection: Upgrade` plus `Upgrade: websocket`
- return the original response untouched

That preserves WebSocket functionality but does not meter WebSocket messages.

### WebSocket Metering Needs Message-Level Hooks

To meter WebSocket mode correctly, observe only server-to-client text messages and parse JSON messages looking for `response.completed`.

A frame-level implementation must handle:

- client masking
- server unmasked frames
- fragmentation
- ping / pong / close
- binary vs text messages
- optional compression such as `permessage-deflate`
- preserving ordering and backpressure

Implementing this inside a generic Go HTTP proxy becomes proxy infrastructure work. It is easy to drift into rebuilding a small part of mitmproxy.

### mitmproxy Is A Better Fit For WebSocket-Aware Metering

mitmproxy exposes addon hooks for WebSocket flows:

- `websocket_start(flow)`
- `websocket_message(flow)`
- `websocket_end(flow)`

That is closer to the needed abstraction: handle complete WebSocket messages, inspect only server-to-client text messages, and leave frame/protocol details to a mature proxy.

Recommended architecture if this project is revived:

- use mitmproxy as the MITM engine
- write a small addon for usage extraction
- store usage in SQLite or JSONL
- optionally keep a Go CLI only as a wrapper for config, process management, and reports

## Domain Scope For Codex

Codex can use different base URLs depending on auth mode:

```text
OPENAI_API_KEY / API key mode:
https://api.openai.com/v1

ChatGPT login mode:
https://chatgpt.com/backend-api/codex
```

A proxy that only watches `api.openai.com` will miss ChatGPT-login Codex traffic.

Suggested MITM domain scope:

- `api.openai.com:443`
- `chatgpt.com:443`

Suggested record scope:

- all relevant OpenAI API paths on `api.openai.com`
- only `/backend-api/codex` paths on `chatgpt.com`

Do not record general ChatGPT web traffic.

## Practical Extraction Rules

### HTTPS JSON

1. Let the HTTP proxy framework forward the response normally.
2. Tee the response body without changing semantics.
3. Parse final JSON.
4. Read `id`, `model`, and `usage`.
5. Write one usage record.

### SSE

1. Stream and flush to the client as bytes arrive.
2. Parse `data:` lines as an observer only.
3. Ignore `[DONE]`.
4. Ignore deltas for token accounting.
5. On `response.completed`, extract `response.id`, `response.model`, and `response.usage`.
6. Write one usage record per response id.

### WebSocket

1. Do not wrap the HTTP `101` response body unless the wrapper preserves `io.ReadWriter`.
2. Prefer a proxy framework with WebSocket message hooks.
3. Observe only server-to-client text messages.
4. Parse each message as JSON.
5. On `type == \"response.completed\"`, extract `response.id`, `response.model`, and `response.usage`.
6. De-duplicate by response id.
7. Never persist message content.

## Final Recommendation

For a reliable metering tool:

- use Go for CLI/reporting if desired
- use SQLite/JSONL for local usage records
- avoid prompt/body capture entirely
- use a mature proxy core
- for WebSocket support, prefer mitmproxy-style message hooks over hand-written frame parsing

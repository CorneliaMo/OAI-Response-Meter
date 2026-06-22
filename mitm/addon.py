import asyncio
import json
import os
import socket
from datetime import datetime, timezone
from typing import Any, Optional


SCHEMA_VERSION = 1
DEFAULT_QUEUE_SIZE = 10000
SOCKET_ENV = "OAI_METER_SOCKET"
QUEUE_SIZE_ENV = "OAI_METER_QUEUE_SIZE"


class UsageMeterAddon:
    def __init__(self) -> None:
        self.socket_path = os.environ.get(SOCKET_ENV, "/tmp/oai-meter.sock")
        self.queue_size = _env_int(QUEUE_SIZE_ENV, DEFAULT_QUEUE_SIZE)
        self.queue: Optional[asyncio.Queue[dict[str, Any]]] = None
        self.sender_task: Optional[asyncio.Task[None]] = None
        self.dropped_queue_full = 0
        self.dropped_send_error = 0
        self.sent = 0

    def running(self) -> None:
        self.queue = asyncio.Queue(maxsize=self.queue_size)
        self.sender_task = asyncio.create_task(self._sender())

    def done(self) -> None:
        if self.sender_task is not None:
            self.sender_task.cancel()

    def response(self, flow: Any) -> None:
        event = extract_http_usage(flow)
        if event is not None:
            self._enqueue(event)

    def websocket_message(self, flow: Any) -> None:
        event = extract_websocket_usage(flow)
        if event is not None:
            self._enqueue(event)

    def _enqueue(self, event: dict[str, Any]) -> None:
        if self.queue is None:
            return
        try:
            self.queue.put_nowait(event)
        except asyncio.QueueFull:
            self.dropped_queue_full += 1

    async def _sender(self) -> None:
        assert self.queue is not None
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)
        sock.setblocking(False)
        try:
            while True:
                event = await self.queue.get()
                try:
                    payload = json.dumps(event, separators=(",", ":")).encode("utf-8")
                    await asyncio.get_running_loop().sock_sendto(sock, payload, self.socket_path)
                    self.sent += 1
                except OSError:
                    self.dropped_send_error += 1
                finally:
                    self.queue.task_done()
        finally:
            sock.close()


def extract_http_usage(flow: Any) -> Optional[dict[str, Any]]:
    host, path = _flow_host_path(flow)
    if not _in_scope(host, path):
        return None

    response = getattr(flow, "response", None)
    if response is None:
        return None
    content_type = _header(response, "content-type")
    body = getattr(response, "text", None)
    if body is None:
        raw = getattr(response, "content", None)
        if raw is None:
            return None
        body = raw.decode("utf-8", errors="replace")

    if "text/event-stream" in content_type.lower():
        completed = _extract_sse_completed(body)
        if completed is None:
            return None
        return event_from_response(completed, "sse", host, path)

    if "json" not in content_type.lower() and not body.lstrip().startswith("{"):
        return None
    try:
        payload = json.loads(body)
    except json.JSONDecodeError:
        return None
    return event_from_response(payload, "https-json", host, path)


def extract_websocket_usage(flow: Any) -> Optional[dict[str, Any]]:
    host, path = _flow_host_path(flow)
    if not _in_scope(host, path):
        return None

    messages = getattr(getattr(flow, "websocket", None), "messages", None)
    if not messages:
        return None
    message = messages[-1]
    from_client = getattr(message, "from_client", False)
    if from_client:
        return None
    text = getattr(message, "text", None)
    if text is None:
        content = getattr(message, "content", None)
        if isinstance(content, bytes):
            text = content.decode("utf-8", errors="replace")
        elif isinstance(content, str):
            text = content
    if not text:
        return None
    try:
        payload = json.loads(text)
    except json.JSONDecodeError:
        return None
    if payload.get("type") == "codex.rate_limits":
        return rate_limits_event_from_payload(payload, "websocket", host, path)
    if payload.get("type") != "response.completed":
        return None
    response = payload.get("response", payload)
    return event_from_response(response, "websocket", host, path)


def rate_limits_event_from_payload(payload: dict[str, Any], transport: str, host: str, path: str) -> Optional[dict[str, Any]]:
    raw_json = json.dumps(payload, separators=(",", ":"), sort_keys=True)
    rate_limits = payload.get("rate_limits")
    if not isinstance(rate_limits, dict):
        return {
            "schema": SCHEMA_VERSION,
            "event_type": "codex_rate_limits",
            "ts": _now_rfc3339(),
            "source": "mitmproxy",
            "transport": transport,
            "host": host,
            "path": path,
            "raw_json": raw_json,
        }
    primary = _rate_limit_window(rate_limits.get("primary"))
    secondary = _rate_limit_window(rate_limits.get("secondary"))
    event = {
        "schema": SCHEMA_VERSION,
        "event_type": "codex_rate_limits",
        "ts": _now_rfc3339(),
        "source": "mitmproxy",
        "transport": transport,
        "host": host,
        "path": path,
        "plan_type": payload.get("plan_type") or "",
        "allowed": bool(rate_limits.get("allowed")),
        "limit_reached": bool(rate_limits.get("limit_reached")),
        "raw_json": raw_json,
    }
    event.update(
        {
            "primary_used_percent": primary["used_percent"],
            "primary_window_minutes": primary["window_minutes"],
            "primary_reset_after_seconds": primary["reset_after_seconds"],
            "primary_reset_at": primary["reset_at"],
            "secondary_used_percent": secondary["used_percent"],
            "secondary_window_minutes": secondary["window_minutes"],
            "secondary_reset_after_seconds": secondary["reset_after_seconds"],
            "secondary_reset_at": secondary["reset_at"],
        }
    )
    return event


def event_from_response(response: dict[str, Any], transport: str, host: str, path: str) -> Optional[dict[str, Any]]:
    if response.get("type") == "response.completed" and isinstance(response.get("response"), dict):
        response = response["response"]
    usage = response.get("usage")
    if not isinstance(usage, dict):
        return None
    response_id = response.get("id")
    if not response_id:
        return None

    input_tokens = _int(usage.get("input_tokens", usage.get("prompt_tokens")))
    output_tokens = _int(usage.get("output_tokens", usage.get("completion_tokens")))
    total_tokens = _int(usage.get("total_tokens"))
    input_details = usage.get("input_tokens_details") or usage.get("prompt_tokens_details") or {}
    output_details = usage.get("output_tokens_details") or usage.get("completion_tokens_details") or {}

    return {
        "schema": SCHEMA_VERSION,
        "ts": _now_rfc3339(),
        "source": "mitmproxy",
        "transport": transport,
        "host": host,
        "path": path,
        "response_id": response_id,
        "previous_response_id": response.get("previous_response_id") or "",
        "prompt_cache_key": _string_or_empty(response.get("prompt_cache_key")),
        "model": response.get("model", ""),
        "input_tokens": input_tokens,
        "output_tokens": output_tokens,
        "total_tokens": total_tokens,
        "cached_tokens": _int(input_details.get("cached_tokens")),
        "reasoning_tokens": _int(output_details.get("reasoning_tokens")),
    }


def _rate_limit_window(value: Any) -> dict[str, int]:
    if not isinstance(value, dict):
        value = {}
    return {
        "used_percent": _int(value.get("used_percent")),
        "window_minutes": _int(value.get("window_minutes")),
        "reset_after_seconds": _int(value.get("reset_after_seconds")),
        "reset_at": _int(value.get("reset_at")),
    }


def _now_rfc3339() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def _extract_sse_completed(body: str) -> Optional[dict[str, Any]]:
    event_name = ""
    data_lines: list[str] = []
    for raw_line in body.splitlines():
        line = raw_line.rstrip("\r")
        if line == "":
            completed = _complete_sse_event(event_name, data_lines)
            if completed is not None:
                return completed
            event_name = ""
            data_lines = []
            continue
        if line.startswith("event:"):
            event_name = line[6:].strip()
        elif line.startswith("data:"):
            data_lines.append(line[5:].lstrip())
    return _complete_sse_event(event_name, data_lines)


def _complete_sse_event(event_name: str, data_lines: list[str]) -> Optional[dict[str, Any]]:
    if event_name != "response.completed" or not data_lines:
        return None
    data = "\n".join(data_lines)
    if data == "[DONE]":
        return None
    try:
        payload = json.loads(data)
    except json.JSONDecodeError:
        return None
    response = payload.get("response", payload)
    if isinstance(response, dict):
        return response
    return None


def _flow_host_path(flow: Any) -> tuple[str, str]:
    request = getattr(flow, "request", None)
    host = getattr(request, "host", "") or getattr(request, "pretty_host", "")
    path = getattr(request, "path", "")
    return host, path


def _in_scope(host: str, path: str) -> bool:
    if host == "api.openai.com":
        return path == "/v1/responses"
    return host == "chatgpt.com" and path.startswith("/backend-api/codex")


def _header(message: Any, name: str) -> str:
    headers = getattr(message, "headers", {}) or {}
    if hasattr(headers, "get"):
        return headers.get(name, headers.get(name.title(), "")) or ""
    return ""


def _int(value: Any) -> int:
    if isinstance(value, bool) or value is None:
        return 0
    try:
        number = int(value)
    except (TypeError, ValueError):
        return 0
    return max(number, 0)


def _string_or_empty(value: Any) -> str:
    if isinstance(value, str):
        return value
    return ""


def _env_int(name: str, default: int) -> int:
    try:
        value = int(os.environ.get(name, ""))
    except ValueError:
        return default
    return value if value > 0 else default


addons = [UsageMeterAddon()]

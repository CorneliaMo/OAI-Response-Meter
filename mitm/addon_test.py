import asyncio
import unittest
import json

from addon import PromptVersionTracker, ResponseSpeedTracker, UsageMeterAddon, extract_http_usage, extract_websocket_usage


class Obj:
    def __init__(self, **kwargs):
        self.__dict__.update(kwargs)


class AddonTest(unittest.TestCase):
    def test_http_preset_capture_is_scoped(self):
        addon = UsageMeterAddon()
        addon.queue = asyncio.Queue()
        flow = Obj(id="http", request=Obj(host="api.openai.com", path="/v1/responses", method="POST", timestamp_start=1000,
                   content=json.dumps({"model": "gpt-test", "instructions": "preset", "input": "private input"}).encode()))
        addon.request(flow)
        sample = addon.queue.get_nowait()
        self.assertEqual(sample["text"], "preset")
        self.assertNotIn("private input", json.dumps(sample))
        flow.request.host = "example.com"
        addon.request(flow)
        self.assertTrue(addon.queue.empty())

    def test_first_visible_ignores_tool_input_and_records_tool_metadata(self):
        tracker = ResponseSpeedTracker()
        flow = Obj(id="profile", request=Obj(host="api.openai.com", path="/v1/responses"))
        def observe(payload, at, client=False):
            return tracker.observe(flow, payload, Obj(timestamp=at, from_client=client))
        observe({"type": "response.create"}, 1000, True)
        observe({"type": "response.created", "response": {"id": "r"}}, 1001)
        observe({"type": "response.output_item.added", "item": {"id": "tool", "type": "custom_tool_call", "name": "exec"}}, 1002)
        observe({"type": "response.custom_tool_call_input.delta", "item_id": "tool", "delta": "private command"}, 1003)
        observe({"type": "response.output_item.done", "item": {"id": "tool", "type": "custom_tool_call", "name": "exec"}}, 1004)
        observe({"type": "response.output_text.delta", "delta": ""}, 1005)
        visible = observe({"type": "response.output_text.delta", "delta": "visible"}, 1006)[0]
        self.assertEqual(visible["first_visible_at"], "1970-01-01T00:16:46Z")
        final = observe({"type": "response.completed", "response": {"id": "r", "usage": {"output_tokens": 50}}}, 1010)[0]
        self.assertEqual(final["first_visible_at"], visible["first_visible_at"])
        self.assertEqual(final["tools"][0]["name"], "exec")
        self.assertEqual(final["tools"][0]["input_characters"], 15)
        self.assertEqual(final["tools"][0]["started_at"], "1970-01-01T00:16:42Z")
        self.assertEqual(final["tools"][0]["finished_at"], "1970-01-01T00:16:44Z")
        self.assertNotIn("private command", json.dumps(final))

    def test_prompt_capture_is_preset_only_chunked_and_actual_model_scoped(self):
        tracker = PromptVersionTracker()
        flow = Obj(id="prompts")
        text = "preset line\n" * 1200
        payload = {"type": "response.create", "model": "requested", "input": [
            {"role": "developer", "type": "message", "content": [{"type": "input_text", "text": text}]},
            {"role": "developer", "type": "message", "content": "dynamic permissions"},
            {"role": "user", "content": "private user content"},
        ]}
        self.assertEqual(tracker.observe(flow, payload, Obj(timestamp=1000, from_client=True)), [])
        chunks = tracker.observe(flow, {"type": "response.created", "response": {"id": "r", "model": "actual"}}, Obj(timestamp=1001, from_client=False))
        self.assertGreater(len(chunks), 1)
        self.assertEqual("".join(chunk["text"] for chunk in chunks), text)
        self.assertTrue(all(chunk["model"] == "actual" and len(chunk["text"]) <= 6000 for chunk in chunks))
        self.assertNotIn("dynamic permissions", json.dumps(chunks))
        self.assertNotIn("private user content", json.dumps(chunks))
        payload["instructions"] = "explicit preset"
        tracker.observe(flow, payload, Obj(timestamp=1002, from_client=True))
        chunks = tracker.observe(flow, {"type": "response.created", "response": {"id": "r2"}}, Obj(timestamp=1003, from_client=False))
        self.assertEqual(chunks[0]["text"], "explicit preset")
        self.assertEqual(chunks[0]["source_label"], "instructions")
        payload["instructions"] = "x" * (1024 * 1024 + 1)
        tracker.observe(flow, payload, Obj(timestamp=1004, from_client=True))
        self.assertEqual(tracker.observe(flow, {"type": "response.created", "response": {"id": "large"}}, Obj(timestamp=1005, from_client=False)), [])
        self.assertEqual(len(tracker.pending), 0)

    def test_speed_ttl_prunes_idle_flow_without_new_messages(self):
        tracker = ResponseSpeedTracker()
        flow = Obj(id="idle", request=Obj(host="api.openai.com", path="/v1/responses"))
        tracker.observe(flow, {"type": "response.created", "response": {"id": "expired"}}, Obj(timestamp=1000, from_client=False))
        tracker.prune(4599)
        self.assertIn("idle", tracker.flows)
        tracker.prune(4600)
        self.assertNotIn("idle", tracker.flows)
        self.assertEqual(tracker.observe(flow, {"type": "response.output_text.delta", "delta": "late"}, Obj(timestamp=4601, from_client=False)), [])

    def test_speed_lifecycle_keeps_deltas_distinct_from_tokens(self):
        tracker = ResponseSpeedTracker()
        flow = Obj(id="speed-flow", request=Obj(host="api.openai.com", path="/v1/responses"))

        def observe(payload, timestamp, from_client=False):
            return tracker.observe(flow, payload, Obj(timestamp=timestamp, from_client=from_client))

        self.assertEqual(observe({"type": "response.create", "model": "requested-model"}, 1000, True), [])
        created = observe({"type": "response.created", "response": {"id": "resp_speed", "model": "actual-model"}}, 1001)[0]
        self.assertEqual(created["request_at"], "1970-01-01T00:16:40Z")
        self.assertFalse(created["output_tokens_known"])
        observe({"type": "response.output_item.added", "item": {"id": "item_1"}, "sequence_number": 1}, 1001.1)
        delta = {"type": "response.output_text.delta", "item_id": "item_1", "delta": "hello", "sequence_number": 2}
        live = observe(delta, 1002.1)[0]
        self.assertEqual(live["output_characters"], 5)
        self.assertFalse(live["output_tokens_known"])
        self.assertNotIn("hello", json.dumps(live))
        self.assertEqual(observe(delta, 1003), [])
        observe({"type": "response.output_item.done", "item": {"id": "item_1"}, "sequence_number": 3}, 1003.1)
        final = observe({"type": "response.completed", "response": {"id": "resp_speed", "model": "actual-model", "usage": {"output_tokens": 50}}, "sequence_number": 4}, 1006)[0]
        self.assertTrue(final["completed"])
        self.assertTrue(final["output_tokens_known"])
        self.assertEqual(final["output_tokens"], 50)
        self.assertEqual(final["output_items"], 1)
        self.assertEqual(final["output_characters"], 5)
        self.assertEqual(observe({"type": "response.completed", "response": {"id": "resp_speed"}}, 1007), [])
        tracker.forget(flow)
        self.assertEqual(len(tracker.flows), 0)

    def test_speed_tracking_isolates_connections_and_skips_ambiguous_deltas(self):
        tracker = ResponseSpeedTracker()
        flow = Obj(id="one", request=Obj(host="api.openai.com", path="/v1/responses"))
        second = Obj(id="two", request=flow.request)
        for response_id in ("resp_one", "resp_two"):
            tracker.observe(flow, {"type": "response.created", "response": {"id": response_id}}, Obj(timestamp=1000, from_client=False))
        delta = {"type": "response.output_text.delta", "delta": "ambiguous"}
        self.assertEqual(tracker.observe(flow, delta, Obj(timestamp=1002, from_client=False)), [])
        self.assertEqual(tracker.observe(second, delta, Obj(timestamp=1002, from_client=False)), [])

    def test_addon_enqueues_speed_and_final_usage(self):
        async def run():
            addon = UsageMeterAddon()
            addon.queue = asyncio.Queue(maxsize=10)
            flow = Obj(id="integration", request=Obj(host="api.openai.com", path="/v1/responses"), websocket=Obj(messages=[]))
            for payload, timestamp, from_client in [
                ({"type": "response.create", "model": "gpt-test"}, 1000, True),
                ({"type": "response.created", "response": {"id": "resp_integrated", "model": "gpt-test"}}, 1001, False),
                ({"type": "response.completed", "response": {"id": "resp_integrated", "model": "gpt-test", "usage": {"input_tokens": 10, "output_tokens": 20, "total_tokens": 30}}}, 1003, False),
            ]:
                flow.websocket.messages.append(Obj(text=json.dumps(payload), timestamp=timestamp, from_client=from_client))
                addon.websocket_message(flow)
            events = [addon.queue.get_nowait() for _ in range(addon.queue.qsize())]
            self.assertEqual(len(events), 3)
            self.assertEqual(events[0]["event_type"], "response_speed")
            self.assertEqual(events[1]["output_tokens"], 20)
            self.assertNotIn("event_type", events[1])
            self.assertTrue(events[2]["completed"])
        asyncio.run(run())

    def test_extract_http_json_usage(self):
        flow = Obj(
            request=Obj(host="api.openai.com", path="/v1/responses"),
            response=Obj(
                headers={"content-type": "application/json"},
                text='{"id":"resp_1","previous_response_id":"resp_parent","prompt_cache_key":"session-uuid","model":"gpt-test","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}',
            ),
        )

        event = extract_http_usage(flow)

        self.assertIsNotNone(event)
        self.assertEqual(event["transport"], "https-json")
        self.assertEqual(event["response_id"], "resp_1")
        self.assertEqual(event["previous_response_id"], "resp_parent")
        self.assertEqual(event["prompt_cache_key"], "session-uuid")
        self.assertEqual(event["total_tokens"], 3)

    def test_extract_sse_completed_usage(self):
        flow = Obj(
            request=Obj(host="api.openai.com", path="/v1/responses"),
            response=Obj(
                headers={"content-type": "text/event-stream"},
                text='\n'.join(
                    [
                        "event: response.output_text.delta",
                        'data: {"delta":"ignored"}',
                        "",
                        "event: response.completed",
                        'data: {"response":{"id":"resp_2","model":"gpt-test","usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9,"prompt_tokens_details":{"cached_tokens":1},"completion_tokens_details":{"reasoning_tokens":2}}}}',
                        "",
                    ]
                ),
            ),
        )

        event = extract_http_usage(flow)

        self.assertIsNotNone(event)
        self.assertEqual(event["transport"], "sse")
        self.assertEqual(event["input_tokens"], 4)
        self.assertEqual(event["cached_tokens"], 1)
        self.assertEqual(event["cache_write_tokens"], 0)
        self.assertEqual(event["reasoning_tokens"], 2)

    def test_sse_without_content_type_or_event_label(self):
        completed = {"type": "response.completed", "response": {
            "id": "resp_http_fallback", "model": "gpt-test",
            "usage": {"input_tokens": 10, "output_tokens": 2, "total_tokens": 12,
                      "input_tokens_details": {"cached_tokens": 4, "cache_write_tokens": 3}}}}
        for host, path in (("chatgpt.com", "/backend-api/codex/responses"),
                           ("api.openai.com", "/v1/responses")):
            for headers in ({}, {"content-type": "application/octet-stream"},
                            {"content-type": "text/event-stream"}):
                for label in ("", "event: response.completed\r\n"):
                    with self.subTest(host=host, headers=headers, label=label):
                        flow = Obj(request=Obj(host=host, path=path), response=Obj(
                            headers=headers, text="\ufeff: keepalive\r\n\r\n" + label +
                            "data: " + json.dumps(completed) + "\r\n\r\ndata: [DONE]\r\n\r\n"))
                        event = extract_http_usage(flow)
                        self.assertIsNotNone(event)
                        self.assertEqual(event["transport"], "sse")
                        self.assertEqual(event["response_id"], "resp_http_fallback")
                        self.assertEqual(event["cache_write_tokens"], 3)

    def test_sse_skips_invalid_and_non_completed_frames(self):
        for frame in ("[]", "null", "invalid", '[DONE]',
                      '{"type":"response.created","response":{"id":"not_done","usage":{}}}'):
            flow = Obj(request=Obj(host="chatgpt.com", path="/backend-api/codex/responses"),
                       response=Obj(headers={}, text="data: " + frame + "\n\n"))
            self.assertIsNone(extract_http_usage(flow))

    def test_extract_cache_write_tokens_from_response_completed_payload(self):
        flow = Obj(
            request=Obj(host="api.openai.com", path="/v1/responses"),
            response=Obj(
                headers={"content-type": "application/json"},
                text='{"type":"response.completed","response":{"id":"resp_cache_write","model":"gpt-test","usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15,"input_tokens_details":{"cached_tokens":4,"cache_write_tokens":2}}}}',
            ),
        )

        event = extract_http_usage(flow)

        self.assertIsNotNone(event)
        self.assertEqual(event["response_id"], "resp_cache_write")
        self.assertEqual(event["cached_tokens"], 4)
        self.assertEqual(event["cache_write_tokens"], 2)

    def test_extract_websocket_server_usage(self):
        flow = Obj(
            request=Obj(host="chatgpt.com", path="/backend-api/codex"),
            websocket=Obj(
                messages=[
                    Obj(
                        from_client=False,
                        text='{"type":"response.completed","response":{"id":"resp_3","model":"gpt-test","usage":{"input_tokens":6,"output_tokens":7,"total_tokens":13}}}',
                    )
                ]
            ),
        )

        event = extract_websocket_usage(flow)

        self.assertIsNotNone(event)
        self.assertEqual(event["transport"], "websocket")
        self.assertEqual(event["response_id"], "resp_3")
        self.assertEqual(event["prompt_cache_key"], "")

    def test_extract_usage_with_invalid_prompt_cache_key(self):
        flow = Obj(
            request=Obj(host="api.openai.com", path="/v1/responses"),
            response=Obj(
                headers={"content-type": "application/json"},
                text='{"id":"resp_bad_prompt_cache","prompt_cache_key":{"unexpected":true},"model":"gpt-test","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}',
            ),
        )

        event = extract_http_usage(flow)

        self.assertIsNotNone(event)
        self.assertEqual(event["response_id"], "resp_bad_prompt_cache")
        self.assertEqual(event["prompt_cache_key"], "")

    def test_extract_websocket_codex_rate_limits(self):
        flow = Obj(
            request=Obj(host="chatgpt.com", path="/backend-api/codex"),
            websocket=Obj(
                messages=[
                    Obj(
                        from_client=False,
                        text='{"type":"codex.rate_limits","plan_type":"plus","rate_limits":{"allowed":true,"limit_reached":false,"primary":{"used_percent":1,"window_minutes":300,"reset_after_seconds":18000,"reset_at":1781881906},"secondary":{"used_percent":8,"window_minutes":10080,"reset_after_seconds":516852,"reset_at":1782380758}},"code_review_rate_limits":null,"additional_rate_limits":null,"credits":null,"promo":null}',
                    )
                ]
            ),
        )

        event = extract_websocket_usage(flow)

        self.assertIsNotNone(event)
        self.assertEqual(event["event_type"], "codex_rate_limits")
        self.assertEqual(event["plan_type"], "plus")
        self.assertTrue(event["allowed"])
        self.assertFalse(event["limit_reached"])
        self.assertEqual(event["five_hour_reset_at"], 1781881906)
        self.assertEqual(event["weekly_reset_at"], 1782380758)
        self.assertIn("codex.rate_limits", event["raw_json"])

    def test_extract_websocket_codex_rate_limits_without_expected_keys(self):
        flow = Obj(
            request=Obj(host="chatgpt.com", path="/backend-api/codex"),
            websocket=Obj(messages=[Obj(from_client=False, text='{"type":"codex.rate_limits"}')]),
        )

        event = extract_websocket_usage(flow)

        self.assertIsNotNone(event)
        self.assertEqual(event["event_type"], "codex_rate_limits")
        self.assertIn("raw_json", event)
        self.assertNotIn("five_hour_reset_at", event)

    def test_extract_websocket_weekly_limit_without_five_hour_limit(self):
        flow = Obj(
            request=Obj(host="chatgpt.com", path="/backend-api/codex"),
            websocket=Obj(
                messages=[
                    Obj(
                        from_client=False,
                        text='{"type":"codex.rate_limits","rate_limits":{"allowed":true,"primary":{"used_percent":100,"window_minutes":10080,"reset_after_seconds":60,"reset_at":1782380758}}}',
                    )
                ]
            ),
        )

        event = extract_websocket_usage(flow)

        self.assertIsNotNone(event)
        self.assertEqual(event["five_hour_window_minutes"], 0)
        self.assertEqual(event["weekly_used_percent"], 100)
        self.assertEqual(event["weekly_window_minutes"], 10080)

    def test_ignores_client_websocket_messages(self):
        flow = Obj(
            request=Obj(host="chatgpt.com", path="/backend-api/codex"),
            websocket=Obj(messages=[Obj(from_client=True, text='{"type":"response.completed"}')]),
        )

        self.assertIsNone(extract_websocket_usage(flow))

    def test_ignores_out_of_scope_hosts(self):
        flow = Obj(
            request=Obj(host="chatgpt.com", path="/not-codex"),
            response=Obj(
                headers={"content-type": "application/json"},
                text='{"id":"resp_1","usage":{"total_tokens":1}}',
            ),
        )

        self.assertIsNone(extract_http_usage(flow))

    def test_ignores_other_api_openai_paths(self):
        flow = Obj(
            request=Obj(host="api.openai.com", path="/v1/models"),
            response=Obj(
                headers={"content-type": "application/json"},
                text='{"id":"resp_1","usage":{"total_tokens":1}}',
            ),
        )

        self.assertIsNone(extract_http_usage(flow))

    def test_queue_full_drops_current_event(self):
        async def run():
            addon = UsageMeterAddon()
            addon.queue = asyncio.Queue(maxsize=1)
            addon._enqueue({"response_id": "resp_1"})
            addon._enqueue({"response_id": "resp_2"})
            self.assertEqual(addon.dropped_queue_full, 1)
            self.assertEqual(addon.queue.qsize(), 1)

        asyncio.run(run())


if __name__ == "__main__":
    unittest.main()

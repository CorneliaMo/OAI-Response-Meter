import asyncio
import unittest

from addon import UsageMeterAddon, extract_http_usage, extract_websocket_usage


class Obj:
    def __init__(self, **kwargs):
        self.__dict__.update(kwargs)


class AddonTest(unittest.TestCase):
    def test_extract_http_json_usage(self):
        flow = Obj(
            request=Obj(host="api.openai.com", path="/v1/responses"),
            response=Obj(
                headers={"content-type": "application/json"},
                text='{"id":"resp_1","model":"gpt-test","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}',
            ),
        )

        event = extract_http_usage(flow)

        self.assertIsNotNone(event)
        self.assertEqual(event["transport"], "https-json")
        self.assertEqual(event["response_id"], "resp_1")
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
        self.assertEqual(event["reasoning_tokens"], 2)

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

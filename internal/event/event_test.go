package event

import "testing"

func TestDecodeValidUsage(t *testing.T) {
	raw := []byte(`{
		"schema": 1,
		"ts": "2026-06-20T12:00:00Z",
		"source": "mitmproxy",
		"transport": "websocket",
		"host": "chatgpt.com",
		"path": "/backend-api/codex",
		"response_id": "resp_123",
		"model": "gpt-test",
		"input_tokens": 10,
		"output_tokens": 20,
		"total_tokens": 30,
		"cached_tokens": 4,
		"reasoning_tokens": 5
	}`)

	got, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.ResponseID != "resp_123" || got.TotalTokens != 30 {
		t.Fatalf("Decode() = %+v", got)
	}
}

func TestDecodeRejectsInvalidUsage(t *testing.T) {
	tests := map[string]string{
		"bad json":           `{`,
		"wrong schema":       `{"schema":2,"ts":"2026-06-20T12:00:00Z","transport":"sse","host":"api.openai.com","response_id":"resp_123"}`,
		"missing timestamp":  `{"schema":1,"transport":"sse","host":"api.openai.com","response_id":"resp_123"}`,
		"bad timestamp":      `{"schema":1,"ts":"bad","transport":"sse","host":"api.openai.com","response_id":"resp_123"}`,
		"missing response id": `{"schema":1,"ts":"2026-06-20T12:00:00Z","transport":"sse","host":"api.openai.com"}`,
		"negative tokens":    `{"schema":1,"ts":"2026-06-20T12:00:00Z","transport":"sse","host":"api.openai.com","response_id":"resp_123","input_tokens":-1}`,
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode([]byte(raw)); err == nil {
				t.Fatal("Decode() error = nil")
			}
		})
	}
}

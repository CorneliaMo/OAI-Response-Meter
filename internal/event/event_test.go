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
		"previous_response_id": "resp_parent",
		"chain_root_response_id": "resp_root",
		"prompt_cache_key": "session-uuid",
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
	if got.ResponseID != "resp_123" || got.PreviousResponseID != "resp_parent" || got.ChainRootResponseID != "resp_root" || got.PromptCacheKey != "session-uuid" || got.TotalTokens != 30 {
		t.Fatalf("Decode() = %+v", got)
	}
}

func TestDecodeDatagramRateLimits(t *testing.T) {
	raw := []byte(`{
		"schema": 1,
		"event_type": "codex_rate_limits",
		"ts": "2026-06-20T12:00:00Z",
		"source": "mitmproxy",
		"transport": "websocket",
		"host": "chatgpt.com",
		"path": "/backend-api/codex",
		"plan_type": "plus",
		"allowed": true,
		"limit_reached": false,
		"primary_used_percent": 1,
		"primary_window_minutes": 300,
		"primary_reset_after_seconds": 18000,
		"primary_reset_at": 1781881906,
		"secondary_used_percent": 8,
		"secondary_window_minutes": 10080,
		"secondary_reset_after_seconds": 516852,
		"secondary_reset_at": 1782380758,
		"raw_json": "{\"type\":\"codex.rate_limits\"}"
	}`)

	got, err := DecodeDatagram(raw)
	if err != nil {
		t.Fatalf("DecodeDatagram() error = %v", err)
	}
	if got.Kind != KindRateLimits || got.RateLimits.PlanType != "plus" || got.RateLimits.PrimaryResetAt != 1781881906 {
		t.Fatalf("DecodeDatagram() = %+v", got)
	}
}

func TestDecodeDatagramRejectsRateLimitsWithoutResetAt(t *testing.T) {
	raw := []byte(`{
		"schema": 1,
		"event_type": "codex_rate_limits",
		"ts": "2026-06-20T12:00:00Z",
		"source": "mitmproxy",
		"transport": "websocket",
		"host": "chatgpt.com",
		"path": "/backend-api/codex",
		"raw_json": "{\"type\":\"codex.rate_limits\"}"
	}`)
	if _, err := DecodeDatagram(raw); err == nil {
		t.Fatal("DecodeDatagram() error = nil")
	}
}

func TestDecodeRejectsInvalidUsage(t *testing.T) {
	tests := map[string]string{
		"bad json":            `{`,
		"wrong schema":        `{"schema":2,"ts":"2026-06-20T12:00:00Z","transport":"sse","host":"api.openai.com","response_id":"resp_123"}`,
		"missing timestamp":   `{"schema":1,"transport":"sse","host":"api.openai.com","response_id":"resp_123"}`,
		"bad timestamp":       `{"schema":1,"ts":"bad","transport":"sse","host":"api.openai.com","response_id":"resp_123"}`,
		"missing response id": `{"schema":1,"ts":"2026-06-20T12:00:00Z","transport":"sse","host":"api.openai.com"}`,
		"negative tokens":     `{"schema":1,"ts":"2026-06-20T12:00:00Z","transport":"sse","host":"api.openai.com","response_id":"resp_123","input_tokens":-1}`,
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode([]byte(raw)); err == nil {
				t.Fatal("Decode() error = nil")
			}
		})
	}
}

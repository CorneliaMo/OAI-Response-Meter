package event

import (
	"encoding/json"
	"testing"
)

func testSpeed() Speed {
	return Speed{Schema: 1, EventType: SpeedEventType, Timestamp: "2026-10-03T01:00:01Z", Transport: "websocket", Host: "chatgpt.com", ResponseID: "r1", CreatedAt: "2026-10-03T01:00:00Z", UpdatedAt: "2026-10-03T01:00:01Z", OutputTokens: 12, OutputTokensKnown: true}
}

func TestSpeedDatagram(t *testing.T) {
	s := testSpeed()
	data, _ := json.Marshal(s)
	var wire map[string]any
	json.Unmarshal(data, &wire)
	wire["content"] = "must not persist"
	data, _ = json.Marshal(wire)
	got, err := DecodeDatagram(data)
	if err != nil || got.Kind != KindSpeed || got.Speed != s {
		t.Fatalf("decode = %+v, %v", got, err)
	}
	line, err := got.Speed.MarshalJSONLine()
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(line, &wire)
	var metadata map[string]any
	json.Unmarshal(line, &metadata)
	if _, ok := metadata["content"]; ok {
		t.Fatal("content retained")
	}
}

func TestSpeedValidation(t *testing.T) {
	for name, mutate := range map[string]func(*Speed){
		"schema":     func(s *Speed) { s.Schema = 2 },
		"event type": func(s *Speed) { s.EventType = "usage" },
		"ts":         func(s *Speed) { s.Timestamp = "bad" },
		"created":    func(s *Speed) { s.CreatedAt = "" },
		"updated":    func(s *Speed) { s.UpdatedAt = "bad" },
		"request":    func(s *Speed) { s.RequestAt = "bad" },
		"backward":   func(s *Speed) { s.UpdatedAt = "2026-10-03T00:00:00Z" },
		"tokens":     func(s *Speed) { s.OutputTokens = -1 },
		"characters": func(s *Speed) { s.OutputCharacters = -1 },
		"items":      func(s *Speed) { s.OutputItems = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			s := testSpeed()
			mutate(&s)
			if s.Validate() == nil {
				t.Fatal("accepted invalid speed")
			}
		})
	}
	s := testSpeed()
	s.UpdatedAt = s.CreatedAt
	if err := s.Validate(); err != nil {
		t.Fatalf("zero duration: %v", err)
	}
	s.RequestAt = "2026-10-03T00:59:59.123456789Z"
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	data, _ := s.MarshalJSONLine()
	var wire map[string]any
	json.Unmarshal(data, &wire)
	wire["output_tokens"] = 1.5
	data, _ = json.Marshal(wire)
	if _, err := DecodeDatagram(data); err == nil {
		t.Fatal("accepted fractional count")
	}
}

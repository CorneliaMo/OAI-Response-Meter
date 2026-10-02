package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cornelia/oai-response-meter/internal/event"
)

func TestSpeedSnapshotsOrderingAndJSONL(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	s, err := Open(ctx, filepath.Join(dir, "events.db"), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	initial := event.Speed{Schema: 1, EventType: event.SpeedEventType, Timestamp: "2026-10-03T01:00:00Z", Transport: "websocket", Host: "chatgpt.com", ResponseID: "r1", Model: "gpt-5-2026-10-01", CreatedAt: "2026-10-03T01:00:00Z", UpdatedAt: "2026-10-03T01:00:00.000000001Z", OutputCharacters: 1}
	newer := initial
	newer.UpdatedAt = "2026-10-03T09:00:00.000000002+08:00"
	newer.OutputCharacters = 2
	final := newer
	final.Completed = true
	final.OutputTokensKnown = true
	final.OutputTokens = 20
	provisional := newer
	provisional.UpdatedAt = "2026-10-03T01:00:01Z"
	provisional.OutputCharacters = 999
	olderFinal := final
	olderFinal.UpdatedAt = initial.UpdatedAt
	olderFinal.OutputTokens = 999
	laterFinal := final
	laterFinal.UpdatedAt = provisional.UpdatedAt
	laterFinal.OutputTokens = 888
	result, err := s.WriteSpeedBatch(ctx, []event.Speed{initial, newer, initial, final, final, provisional, olderFinal, laterFinal})
	if err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 3 || result.Duplicates != 5 {
		t.Fatalf("result=%+v", result)
	}
	var tokens, chars int64
	var completed bool
	var model, updated string
	if err := s.db.QueryRow(`select output_tokens,output_characters,completed,model,updated_at from response_speed_events where response_id='r1'`).Scan(&tokens, &chars, &completed, &model, &updated); err != nil {
		t.Fatal(err)
	}
	if tokens != 20 || chars != 2 || !completed || model != initial.Model || updated != final.UpdatedAt {
		t.Fatalf("stored %d %d %t %s %s", tokens, chars, completed, model, updated)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines=%d", len(lines))
	}
	for _, line := range lines {
		var wire map[string]any
		if err := json.Unmarshal([]byte(line), &wire); err != nil {
			t.Fatal(err)
		}
		if wire["event_type"] != event.SpeedEventType || wire["content"] != nil {
			t.Fatalf("metadata=%v", wire)
		}
	}
	invalid := initial
	invalid.ResponseID = "bad"
	invalid.OutputItems = -1
	if _, err := s.WriteSpeedBatch(ctx, []event.Speed{invalid}); err == nil {
		t.Fatal("accepted negative count")
	}
}

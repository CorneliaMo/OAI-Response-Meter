package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cornelia/oai-response-meter/internal/event"
)

func TestPruneActiveSpeedsTTL(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, filepath.Join(dir, "events.db"), filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		id    string
		age   time.Duration
		final bool
	}{
		{"expired", time.Hour, false}, {"recent", 59 * time.Minute, false}, {"final", 2 * time.Hour, true},
	} {
		created := now.Add(-tc.age)
		item := event.Speed{Schema: 1, EventType: event.SpeedEventType, Timestamp: created.Format(time.RFC3339Nano), Transport: "websocket", Host: "chatgpt.com", ResponseID: tc.id, CreatedAt: created.Format(time.RFC3339Nano), UpdatedAt: created.Add(time.Second).Format(time.RFC3339Nano), Completed: tc.final}
		if _, err := s.WriteSpeedBatch(ctx, []event.Speed{item}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PruneActiveSpeeds(ctx, now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`select count(*) from response_speed_events`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := s.db.QueryRow(`select count(*) from response_speed_events where response_id='expired'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired=%d err=%v", count, err)
	}
}

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

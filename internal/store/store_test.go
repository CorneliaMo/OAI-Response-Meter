package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cornelia/oai-response-meter/internal/event"
	_ "modernc.org/sqlite"
)

func TestWriteBatchStoresUsageAndJSONL(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "usage.db")
	jsonlPath := filepath.Join(dir, "usage.jsonl")

	store, err := Open(ctx, dbPath, jsonlPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	usage := sampleUsage("resp_1")
	result, err := store.WriteBatch(ctx, []event.Usage{usage})
	if err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	if result.Inserted != 1 || result.Duplicates != 0 {
		t.Fatalf("WriteBatch() result = %+v", result)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	var total int64
	if err := db.QueryRowContext(ctx, `select total_tokens from usage_events where response_id = ?`, "resp_1").Scan(&total); err != nil {
		t.Fatalf("query usage error = %v", err)
	}
	if total != 30 {
		t.Fatalf("total_tokens = %d, want 30", total)
	}

	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := strings.Count(string(data), "\n"); got != 1 {
		t.Fatalf("jsonl line count = %d, want 1; data=%q", got, data)
	}
	if !strings.Contains(string(data), `"response_id":"resp_1"`) {
		t.Fatalf("jsonl missing response id: %s", data)
	}
}

func TestWriteBatchDeduplicatesByResponseID(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, filepath.Join(dir, "usage.db"), filepath.Join(dir, "usage.jsonl"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	result, err := store.WriteBatch(ctx, []event.Usage{sampleUsage("resp_1"), sampleUsage("resp_1")})
	if err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	if result.Inserted != 1 || result.Duplicates != 1 {
		t.Fatalf("WriteBatch() result = %+v", result)
	}
}

func sampleUsage(responseID string) event.Usage {
	return event.Usage{
		Schema:          event.SchemaVersion,
		Timestamp:       "2026-06-20T12:00:00Z",
		Source:          "mitmproxy",
		Transport:       "websocket",
		Host:            "chatgpt.com",
		Path:            "/backend-api/codex",
		ResponseID:      responseID,
		Model:           "gpt-test",
		InputTokens:     10,
		OutputTokens:    20,
		TotalTokens:     30,
		CachedTokens:    4,
		ReasoningTokens: 5,
	}
}

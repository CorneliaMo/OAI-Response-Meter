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
	var root string
	var promptCacheKey string
	if err := db.QueryRowContext(ctx, `select total_tokens, chain_root_response_id, prompt_cache_key from usage_events where response_id = ?`, "resp_1").Scan(&total, &root, &promptCacheKey); err != nil {
		t.Fatalf("query usage error = %v", err)
	}
	if total != 30 {
		t.Fatalf("total_tokens = %d, want 30", total)
	}
	if root != "resp_1" {
		t.Fatalf("chain_root_response_id = %q, want resp_1", root)
	}
	if promptCacheKey != "session-uuid" {
		t.Fatalf("prompt_cache_key = %q, want session-uuid", promptCacheKey)
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

func TestWriteBatchComputesChainRootFromParent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, filepath.Join(dir, "usage.db"), filepath.Join(dir, "usage.jsonl"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	parent := sampleUsage("resp_parent")
	child := sampleUsage("resp_child")
	child.PreviousResponseID = "resp_parent"
	if _, err := store.WriteBatch(ctx, []event.Usage{parent, child}); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "usage.db"))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	var previous, root string
	if err := db.QueryRowContext(ctx, `select previous_response_id, chain_root_response_id from usage_events where response_id = ?`, "resp_child").Scan(&previous, &root); err != nil {
		t.Fatalf("query child error = %v", err)
	}
	if previous != "resp_parent" || root != "resp_parent" {
		t.Fatalf("child previous/root = %q/%q, want resp_parent/resp_parent", previous, root)
	}
}

func TestWriteBatchRepairsOutOfOrderChainRoots(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "usage.db")
	store, err := Open(ctx, dbPath, filepath.Join(dir, "usage.jsonl"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	child := sampleUsage("resp_child")
	child.PreviousResponseID = "resp_parent"
	parent := sampleUsage("resp_parent")
	parent.PreviousResponseID = "resp_root"
	root := sampleUsage("resp_root")
	if _, err := store.WriteBatch(ctx, []event.Usage{child, parent, root}); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `select response_id, chain_root_response_id from usage_events`)
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	defer rows.Close()
	roots := map[string]string{}
	for rows.Next() {
		var responseID, rootID string
		if err := rows.Scan(&responseID, &rootID); err != nil {
			t.Fatalf("Scan() error = %v", err)
		}
		roots[responseID] = rootID
	}
	for _, responseID := range []string{"resp_root", "resp_parent", "resp_child"} {
		if roots[responseID] != "resp_root" {
			t.Fatalf("%s root = %q, want resp_root; all=%v", responseID, roots[responseID], roots)
		}
	}
}

func TestOpenMigratesExistingSchemaWithChainRoot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "usage.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `
create table usage_events (
  id integer primary key autoincrement,
  ts text not null,
  source text not null,
  transport text not null,
  host text not null,
  path text not null,
  response_id text not null unique,
  model text,
  input_tokens integer not null default 0,
  output_tokens integer not null default 0,
  total_tokens integer not null default 0,
  cached_tokens integer not null default 0,
  reasoning_tokens integer not null default 0,
  created_at text not null default current_timestamp
)`); err != nil {
		t.Fatalf("create old schema error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `
insert into usage_events (
  ts, source, transport, host, path, response_id, model,
  input_tokens, output_tokens, total_tokens, cached_tokens, reasoning_tokens
) values (
  '2026-06-20T12:00:00Z', 'mitmproxy', 'https-json', 'api.openai.com',
  '/v1/responses', 'resp_old', 'gpt-test', 1, 2, 3, 0, 0
)`); err != nil {
		t.Fatalf("insert old row error = %v", err)
	}
	db.Close()

	store, err := Open(ctx, dbPath, filepath.Join(dir, "usage.jsonl"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()
	var previous, root, promptCacheKey string
	if err := db.QueryRowContext(ctx, `select previous_response_id, chain_root_response_id, prompt_cache_key from usage_events where response_id = ?`, "resp_old").Scan(&previous, &root, &promptCacheKey); err != nil {
		t.Fatalf("query migrated row error = %v", err)
	}
	if previous != "" || root != "resp_old" || promptCacheKey != "" {
		t.Fatalf("previous/root/prompt_cache_key = %q/%q/%q, want empty/resp_old/empty", previous, root, promptCacheKey)
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

func TestWriteRateLimitBatchStoresRawEventAndResetWindows(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "usage.db")
	jsonlPath := filepath.Join(dir, "usage.jsonl")
	store, err := Open(ctx, dbPath, jsonlPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	result, err := store.WriteRateLimitBatch(ctx, []event.RateLimits{sampleRateLimits()})
	if err != nil {
		t.Fatalf("WriteRateLimitBatch() error = %v", err)
	}
	if result.Inserted != 1 {
		t.Fatalf("WriteRateLimitBatch() result = %+v", result)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()
	var plan string
	var primaryResetAt, secondaryResetAt int64
	var raw string
	if err := db.QueryRowContext(ctx, `select plan_type, primary_reset_at, secondary_reset_at, raw_json from codex_rate_limit_events`).Scan(&plan, &primaryResetAt, &secondaryResetAt, &raw); err != nil {
		t.Fatalf("query rate limit event error = %v", err)
	}
	if plan != "plus" || primaryResetAt != 1781881906 || secondaryResetAt != 1782380758 || !strings.Contains(raw, "codex.rate_limits") {
		t.Fatalf("stored rate limit event = plan:%q primary:%d secondary:%d raw:%q", plan, primaryResetAt, secondaryResetAt, raw)
	}

	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), `"event_type":"codex_rate_limits"`) {
		t.Fatalf("jsonl missing rate limits event: %s", data)
	}
}

func sampleUsage(responseID string) event.Usage {
	return event.Usage{
		Schema:             event.SchemaVersion,
		Timestamp:          "2026-06-20T12:00:00Z",
		Source:             "mitmproxy",
		Transport:          "websocket",
		Host:               "chatgpt.com",
		Path:               "/backend-api/codex",
		ResponseID:         responseID,
		PreviousResponseID: "",
		PromptCacheKey:     "session-uuid",
		Model:              "gpt-test",
		InputTokens:        10,
		OutputTokens:       20,
		TotalTokens:        30,
		CachedTokens:       4,
		ReasoningTokens:    5,
	}
}

func sampleRateLimits() event.RateLimits {
	return event.RateLimits{
		Schema:                     event.SchemaVersion,
		EventType:                  event.RateLimitsEventType,
		Timestamp:                  "2026-06-20T12:00:00Z",
		Source:                     "mitmproxy",
		Transport:                  "websocket",
		Host:                       "chatgpt.com",
		Path:                       "/backend-api/codex",
		PlanType:                   "plus",
		Allowed:                    true,
		LimitReached:               false,
		PrimaryUsedPercent:         1,
		PrimaryWindowMinutes:       300,
		PrimaryResetAfterSeconds:   18000,
		PrimaryResetAt:             1781881906,
		SecondaryUsedPercent:       8,
		SecondaryWindowMinutes:     10080,
		SecondaryResetAfterSeconds: 516852,
		SecondaryResetAt:           1782380758,
		RawJSON:                    `{"type":"codex.rate_limits"}`,
	}
}

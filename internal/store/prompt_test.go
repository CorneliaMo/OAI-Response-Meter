package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"github.com/cornelia/oai-response-meter/internal/event"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func promptChunk(text string) event.PromptVersion {
	return event.PromptVersion{Schema: 1, EventType: event.PromptEventType, Timestamp: "2026-10-03T00:00:00.123456789Z", Model: "gpt-5-2026-10-01", SourceLabel: "instructions", Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(text))), ChunkCount: 1, Text: text}
}
func TestPromptReassemblyPersistenceAndPrivacy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "events.db")
	jsonl := filepath.Join(dir, "events.jsonl")
	s, err := Open(ctx, dbPath, jsonl)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := promptChunk("private正文\ntext")
	a, b := p, p
	a.ChunkCount = 2
	a.Text = "private正文"
	b.ChunkCount = 2
	b.ChunkIndex = 1
	b.Text = "\ntext"
	if result, err := s.WritePromptBatch(ctx, []event.PromptVersion{b, b}); err != nil || result.Inserted != 0 {
		t.Fatalf("incomplete %+v %v", result, err)
	}
	var count int
	s.db.QueryRow(`select count(*) from prompt_versions`).Scan(&count)
	if count != 0 {
		t.Fatal("persisted incomplete prompt")
	}
	if result, err := s.WritePromptBatch(ctx, []event.PromptVersion{a, b, a}); err != nil || result.Inserted != 1 || result.Duplicates != 1 {
		t.Fatalf("complete %+v %v", result, err)
	}
	// Canonical aliases and equivalent timestamps describe the same observation.
	p.Model = "gpt-5"
	p.Timestamp = "2026-10-03T08:00:00.123456789+08:00"
	if result, err := s.WritePromptBatch(ctx, []event.PromptVersion{p}); err != nil || result.Duplicates != 1 {
		t.Fatalf("duplicate %+v %v", result, err)
	}
	p.Timestamp = "2026-10-03T00:00:01Z"
	if _, err := s.WritePromptBatch(ctx, []event.PromptVersion{p}); err != nil {
		t.Fatal(err)
	}
	var model, text, first, last string
	var observations, chars int
	err = s.db.QueryRow(`select model,text,first_seen,last_seen,observations,characters from prompt_versions`).Scan(&model, &text, &first, &last, &observations, &chars)
	if err != nil || model != "gpt-5" || text != p.Text || observations != 2 || chars != 14 || last != p.Timestamp {
		t.Fatalf("stored model=%s observations=%d chars=%d err=%v", model, observations, chars, err)
	}
	p.Model = "gpt-5-mini"
	if _, err := s.WritePromptBatch(ctx, []event.PromptVersion{p}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(jsonl)
	if err != nil || len(data) != 0 {
		t.Fatalf("prompt leaked into JSONL bytes=%d err=%v", len(data), err)
	}
	s.Close()
	s, err = Open(ctx, dbPath, jsonl)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if result, err := s.WritePromptBatch(ctx, []event.PromptVersion{p}); err != nil || result.Duplicates != 1 {
		t.Fatalf("reopened %+v %v", result, err)
	}
}

func TestPromptInvalidBoundsEvictionAndSegregation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, filepath.Join(dir, "db"), filepath.Join(dir, "jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := promptChunk("secret")
	p.Text = "wrong"
	if _, err = s.WritePromptBatch(ctx, []event.PromptVersion{p}); err == nil {
		t.Fatal("accepted hash mismatch")
	}
	p = promptChunk("ab")
	p.ChunkCount = 2
	p.Text = "a"
	if _, err = s.WritePromptBatch(ctx, []event.PromptVersion{p}); err != nil {
		t.Fatal(err)
	}
	other := p
	other.Model = "gpt-5-mini"
	other.ChunkIndex = 1
	other.Text = "b"
	if _, err = s.WritePromptBatch(ctx, []event.PromptVersion{other}); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`select count(*) from prompt_versions`).Scan(&n)
	if n != 0 {
		t.Fatal("combined models")
	}
	conflict := p
	conflict.Text = "z"
	if _, err = s.WritePromptBatch(ctx, []event.PromptVersion{conflict}); err == nil {
		t.Fatal("accepted conflict")
	}
	for i := 0; i < 140; i++ {
		c := p
		c.Hash = fmt.Sprintf("%064x", i)
		if _, err = s.WritePromptBatch(ctx, []event.PromptVersion{c}); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.assemblies) != 128 {
		t.Fatalf("assemblies=%d", len(s.assemblies))
	}
	for _, a := range s.assemblies {
		a.received = time.Now().Add(-11 * time.Minute)
	}
	if err = s.PruneActiveSpeeds(ctx, time.Now()); err != nil || len(s.assemblies) != 0 {
		t.Fatal("TTL not enforced")
	}
	p = promptChunk("")
	p.ChunkCount = 256
	p.Text = strings.Repeat("界", 6000)
	for i := 0; i < 256; i++ {
		p.ChunkIndex = i
		_, err = s.WritePromptBatch(ctx, []event.PromptVersion{p})
		if err != nil {
			break
		}
	}
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("byte bound err=%v", err)
	}
	if len(s.assemblies) != 0 {
		t.Fatal("oversized assembly retained")
	}
}

func TestSpeedLegacySchemaUpgrade(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(speedTableSQL); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`insert into response_speed_events values('old','','','','','','','','2026-10-03T00:00:00Z','2026-10-03T00:00:01Z',1,0,1,0,0)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = InitSpeedSchema(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	var visible, tools string
	if err = db.QueryRow(`select first_visible_at,tools_json from response_speed_events where response_id='old'`).Scan(&visible, &tools); err != nil || visible != "" || tools != "" {
		t.Fatalf("legacy visible=%q tools=%q err=%v", visible, tools, err)
	}
}

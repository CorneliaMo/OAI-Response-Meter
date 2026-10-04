package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/cornelia/oai-response-meter/internal/event"
	"github.com/cornelia/oai-response-meter/internal/store"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPromptEndpointsAndSelectedAllMetadata(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db")
	sink, err := store.Open(ctx, dbPath, filepath.Join(dir, "jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	var hash string
	for i := 0; i < 105; i++ {
		text := fmt.Sprintf("private正文 %d", i)
		hash = fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
		p := event.PromptVersion{Schema: 1, EventType: event.PromptEventType, Timestamp: time.Date(2026, 10, 3, 0, 0, i, 0, time.UTC).Format(time.RFC3339Nano), Model: "gpt-5-2026-10-01", SourceLabel: "developer", Hash: hash, ChunkCount: 1, Text: text}
		if _, err := sink.WritePromptBatch(ctx, []event.PromptVersion{p}); err != nil {
			t.Fatal(err)
		}
		if i == 104 {
			p.Model = "gpt-5-mini"
			if _, err := sink.WritePromptBatch(ctx, []event.PromptVersion{p}); err != nil {
				t.Fatal(err)
			}
		}
	}
	handler, db, err := newHandler(Config{DBPath: dbPath}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	request := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	rec := request("/api/prompts?model=gpt-5-2026-10-01&from=bad")
	var resp struct {
		Models   []string         `json:"models"`
		Versions []PromptMetadata `json:"versions"`
	}
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Models) != 2 || len(resp.Versions) != 105 || strings.Contains(rec.Body.String(), "private正文") || strings.Contains(rec.Body.String(), `"text"`) {
		t.Fatalf("metadata count=%d", len(resp.Versions))
	}
	rec = request("/api/prompts")
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != 200 || len(resp.Versions) != 100 {
		t.Fatalf("global count=%d status=%d", len(resp.Versions), rec.Code)
	}
	rec = request("/api/prompt?model=gpt-5-2026-10-01&hash=" + hash)
	var detail PromptDetail
	if rec.Code != 200 {
		t.Fatalf("detail status=%d", rec.Code)
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Text != "private正文 104" || detail.Model != "gpt-5" || detail.Observations != 1 || detail.SourceLabel != "developer" {
		t.Fatalf("detail metadata=%+v", detail.PromptMetadata)
	}
	if rec = request("/api/prompt?model=missing&hash=" + hash); rec.Code != 404 {
		t.Fatalf("missing=%d", rec.Code)
	}
	if rec = request("/api/prompts?model=missing"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"versions":[]`) {
		t.Fatalf("missing list=%d", rec.Code)
	}
}

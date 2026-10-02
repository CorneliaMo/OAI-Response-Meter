package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/cornelia/oai-response-meter/internal/event"
	"github.com/cornelia/oai-response-meter/internal/store"
)

func TestSpeedsMeansTimezoneAndActive(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "events.db")
	sink, err := store.Open(ctx, dbPath, filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	now := time.Date(2026, 10, 3, 16, 5, 0, 0, time.UTC)
	makeSpeed := func(id, model string, created time.Time, seconds int, tokens int64, known, completed bool) event.Speed {
		return event.Speed{Schema: 1, EventType: event.SpeedEventType, Timestamp: now.Format(time.RFC3339Nano), Transport: "websocket", Host: "chatgpt.com", ResponseID: id, Model: model, CreatedAt: created.Format(time.RFC3339Nano), UpdatedAt: created.Add(time.Duration(seconds) * time.Second).Format(time.RFC3339Nano), Completed: completed, OutputTokens: tokens, OutputTokensKnown: known, OutputCharacters: 120, OutputItems: 2}
	}
	created := time.Date(2026, 10, 3, 16, 0, 3, 0, time.UTC)
	a := makeSpeed("a", "gpt-5-2026-10-01", created, 2, 20, true, true)
	a.RequestAt = created.Add(-time.Second).Format(time.RFC3339Nano)
	b := makeSpeed("b", "gpt-5", created.Add(time.Second), 10, 200, true, true)
	events := []event.Speed{a, b, makeSpeed("prefill", "gpt-5", created, 5, 0, true, true), makeSpeed("unknown", "gpt-5", created, 2, 100, false, true), makeSpeed("zero", "gpt-5", created, 0, 100, true, true), makeSpeed("stale", "gpt-5", now.Add(-11*time.Minute), 1, 1, true, false), makeSpeed("live", "gpt-5-2026-10-01", now.Add(-time.Minute), 30, 60, true, false), makeSpeed("unknown-live", "gpt-5", now.Add(-time.Minute), 0, 0, false, false)}
	if _, err := sink.WriteSpeedBatch(ctx, events); err != nil {
		t.Fatal(err)
	}
	handler, db, err := newHandler(Config{DBPath: dbPath}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	request := func(path string) SpeedsResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 200 {
			t.Fatalf("status=%d %s", rec.Code, rec.Body.String())
		}
		var resp SpeedsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := request("/api/speeds?from=2026-10-04&to=2026-10-04&tz=Asia%2FShanghai")
	if resp.CompletedRequests != 2 || resp.AvgTokensPerSecond == nil || *resp.AvgTokensPerSecond != 15 || len(resp.Points) != 1 {
		t.Fatalf("resp=%+v", resp)
	}
	p := resp.Points[0]
	if p.Time != "2026-10-04T00:00:00+08:00" || p.Model != "gpt-5" || p.Requests != 2 || p.AvgTokensPerSecond != 15 || p.AvgDurationMS != 6000 || p.AvgRequestDurationMS == nil || *p.AvgRequestDurationMS != 3000 {
		t.Fatalf("point=%+v", p)
	}
	if len(resp.Active) != 2 || resp.Active[0].ResponseID != "live" || resp.Active[0].TokensPerSecond == nil || *resp.Active[0].TokensPerSecond != 2 || resp.Active[0].CharactersPerSecond != 4 || resp.Active[0].RequestDurationMS != nil || resp.Active[1].TokensPerSecond != nil {
		t.Fatalf("active=%+v", resp.Active)
	}
	outside := request("/api/speeds?from=2026-10-03&to=2026-10-03&tz=Asia%2FShanghai")
	if outside.CompletedRequests != 0 || len(outside.Points) != 0 || outside.AvgTokensPerSecond != nil || len(outside.Active) != 2 {
		t.Fatalf("outside=%+v", outside)
	}
	for i := 0; i < 105; i++ {
		events = append(events, makeSpeed(fmt.Sprintf("extra-%03d", i), "gpt-5", now.Add(-time.Second), 1, 1, true, false))
	}
	if _, err := sink.WriteSpeedBatch(ctx, events); err != nil {
		t.Fatal(err)
	}
	if got := request("/api/speeds?range=day"); len(got.Active) != 100 {
		t.Fatalf("active limit=%d", len(got.Active))
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/speeds?from=bad", nil))
	if rec.Code != 400 {
		t.Fatalf("invalid range=%d", rec.Code)
	}
}

func TestSpeedsEmptyLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`create table usage_events (ts text,source text,transport text,host text,path text)`); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	handler, db, err := newHandler(Config{DBPath: path}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/speeds", nil))
	if rec.Code != 200 {
		t.Fatalf("status=%d %s", rec.Code, rec.Body.String())
	}
	var resp SpeedsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Points == nil || resp.Active == nil || resp.CompletedRequests != 0 || resp.AvgTokensPerSecond != nil {
		t.Fatalf("empty=%+v", resp)
	}
}

func TestSpeedsRecentAndMultipleModelFilters(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sink, err := store.Open(ctx, filepath.Join(dir, "events.db"), filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	items := []event.Speed{}
	for i := 0; i < 30; i++ {
		model := "gpt-5"
		if i%2 == 0 {
			model = "gpt-5-mini-2026-03-17"
		}
		created := now.Add(-time.Duration(i+1) * time.Minute)
		items = append(items, event.Speed{Schema: 1, EventType: event.SpeedEventType, Timestamp: created.Format(time.RFC3339Nano), Transport: "websocket", Host: "chatgpt.com", ResponseID: fmt.Sprintf("r-%02d", i), Model: model, CreatedAt: created.Format(time.RFC3339Nano), UpdatedAt: created.Add(time.Second).Format(time.RFC3339Nano), Completed: true, OutputTokens: int64(i + 1), OutputTokensKnown: true})
	}
	if _, err := sink.WriteSpeedBatch(ctx, items); err != nil {
		t.Fatal(err)
	}
	handler, db, err := newHandler(Config{DBPath: filepath.Join(dir, "events.db")}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	query := func(path string) SpeedsResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 200 {
			t.Fatalf("status=%d %s", rec.Code, rec.Body.String())
		}
		var response SpeedsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	all := query("/api/speeds?range=day&model=gpt-5&model=gpt-5-mini")
	if len(all.Recent) != 20 || all.Recent[0].ResponseID != "r-00" || all.Recent[19].ResponseID != "r-19" || all.CompletedRequests != 30 || len(all.Models) != 2 {
		t.Fatalf("all=%+v", all)
	}
	mini := query("/api/speeds?range=day&model=gpt-5-mini")
	if len(mini.Recent) != 15 || mini.CompletedRequests != 15 || len(mini.Models) != 2 {
		t.Fatalf("mini=%+v", mini)
	}
	for _, item := range mini.Recent {
		if item.Model != "gpt-5-mini" {
			t.Fatalf("wrong model=%s", item.Model)
		}
	}
	empty := query("/api/speeds?range=day&model=missing")
	if len(empty.Recent) != 0 || len(empty.Points) != 0 || len(empty.Models) != 2 {
		t.Fatalf("empty=%+v", empty)
	}
	outside := query("/api/speeds?from=2026-10-02&to=2026-10-02&tz=UTC")
	if len(outside.Recent) != 0 {
		t.Fatalf("outside=%+v", outside)
	}
}

func TestSpeedsDoesNotFabricateUsageTiming(t *testing.T) {
	handler := testHandler(t)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/speeds?range=year", nil))
	var resp SpeedsResponse
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Points) != 0 || len(resp.Active) != 0 || resp.CompletedRequests != 0 || resp.AvgTokensPerSecond != nil {
		t.Fatalf("fabricated=%+v", resp)
	}
}

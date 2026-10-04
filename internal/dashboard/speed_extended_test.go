package dashboard

import (
	"context"
	"encoding/json"
	"github.com/cornelia/oai-response-meter/internal/event"
	"github.com/cornelia/oai-response-meter/internal/store"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestSpeedFirstVisibleAndFinalizedToolAggregation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "db")
	sink, err := store.Open(ctx, path, filepath.Join(dir, "jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	created := now.Add(-time.Minute)
	makeSpeed := func(id, model string) event.Speed {
		return event.Speed{Schema: 1, EventType: event.SpeedEventType, Timestamp: now.Format(time.RFC3339Nano), Transport: "websocket", Host: "chatgpt.com", ResponseID: id, Model: model, CreatedAt: created.Format(time.RFC3339Nano), UpdatedAt: created.Add(10 * time.Second).Format(time.RFC3339Nano), Completed: true, OutputTokensKnown: true, OutputTokens: 20, RequestAt: created.Add(-time.Second).Format(time.RFC3339Nano)}
	}
	a := makeSpeed("a", "gpt-5-2026-10-01")
	a.FirstVisibleAt = created.Add(250 * time.Millisecond).Format(time.RFC3339Nano)
	a.Tools = []event.Tool{{ItemID: "c1", Name: "shell", Type: "function_call", StartedAt: created.Add(time.Second).Format(time.RFC3339Nano), FinishedAt: created.Add(3 * time.Second).Format(time.RFC3339Nano), InputCharacters: 30}, {ItemID: "c2", Name: "shell", Type: "function_call", StartedAt: created.Add(4 * time.Second).Format(time.RFC3339Nano), InputCharacters: 10}}
	b := makeSpeed("b", "gpt-5")
	b.Tools = []event.Tool{{ItemID: "c3", Name: "shell", Type: "function_call", StartedAt: created.Format(time.RFC3339Nano), FinishedAt: created.Add(time.Second).Format(time.RFC3339Nano), InputCharacters: 20}}
	zero := makeSpeed("zero", "gpt-5")
	zero.OutputTokens = 0
	zero.FirstVisibleAt = created.Add(time.Second).Format(time.RFC3339Nano)
	other := makeSpeed("other", "gpt-5-mini")
	other.Tools = a.Tools
	live := makeSpeed("live", "gpt-5")
	live.Completed = false
	live.Tools = a.Tools
	live.FirstVisibleAt = a.FirstVisibleAt
	outside := makeSpeed("outside", "gpt-5")
	outside.CreatedAt = now.Add(-48 * time.Hour).Format(time.RFC3339Nano)
	outside.RequestAt = ""
	outside.Tools = nil
	if _, err = sink.WriteSpeedBatch(ctx, []event.Speed{a, b, zero, other, live, outside}); err != nil {
		t.Fatal(err)
	}
	if result, err := sink.WriteSpeedBatch(ctx, []event.Speed{a, b}); err != nil || result.Duplicates != 2 {
		t.Fatalf("final snapshot dedup=%+v err=%v", result, err)
	}
	handler, db, err := newHandler(Config{DBPath: path}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	query := func(url string) SpeedsResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
		if rec.Code != 200 {
			t.Fatalf("status=%d %s", rec.Code, rec.Body.String())
		}
		var resp SpeedsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := query("/api/speeds?range=day&model=gpt-5-2026-10-01")
	if resp.CompletedRequests != 2 || len(resp.Recent) != 3 || len(resp.Points) != 1 || resp.AvgFirstVisibleLatencyMS == nil || *resp.AvgFirstVisibleLatencyMS != 1625 || resp.Points[0].AvgFirstVisibleLatencyMS == nil || *resp.Points[0].AvgFirstVisibleLatencyMS != 1250 {
		t.Fatalf("response=%+v", resp)
	}
	if len(resp.Tools) != 1 {
		t.Fatalf("tools=%+v", resp.Tools)
	}
	tool := resp.Tools[0]
	if tool.Model != "gpt-5" || tool.Calls != 3 || tool.CompletedCalls != 2 || tool.InputCharacters != 60 || tool.AvgInputDurationMS == nil || *tool.AvgInputDurationMS != 1500 {
		t.Fatalf("tool=%+v", tool)
	}
	for _, recent := range resp.Recent {
		if recent.ResponseID == "b" && recent.FirstVisibleLatencyMS != nil {
			t.Fatal("created substituted for first-visible")
		}
	}
	if len(resp.Active) != 1 || resp.Active[0].FirstVisibleLatencyMS == nil || *resp.Active[0].FirstVisibleLatencyMS != 1250 {
		t.Fatalf("active=%+v", resp.Active)
	}
	mini := query("/api/speeds?range=day&model=gpt-5-mini")
	if mini.AvgFirstVisibleLatencyMS != nil || len(mini.Tools) != 1 || mini.Tools[0].Calls != 2 {
		t.Fatalf("mini=%+v", mini)
	}
	empty := query("/api/speeds?from=2026-10-02&to=2026-10-02&tz=UTC")
	if len(empty.Tools) != 0 || empty.AvgFirstVisibleLatencyMS != nil {
		t.Fatalf("outside=%+v", empty)
	}
}

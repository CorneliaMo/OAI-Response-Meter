package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/cornelia/oai-response-meter/internal/event"
	"github.com/cornelia/oai-response-meter/internal/pricing"
	"github.com/cornelia/oai-response-meter/internal/store"
)

func TestSummaryEndpoint(t *testing.T) {
	handler := testHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/summary?range=week", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp SummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if resp.Requests != 3 || resp.TotalTokens != 110 {
		t.Fatalf("summary = %+v", resp)
	}
	if resp.CacheRatio != float64(11)/float64(55) {
		t.Fatalf("cache_ratio = %f", resp.CacheRatio)
	}
	if resp.ReasoningRatio != float64(22)/float64(110) {
		t.Fatalf("reasoning_ratio = %f", resp.ReasoningRatio)
	}
	if resp.LatestEventTime != "2026-06-21T11:00:00Z" {
		t.Fatalf("latest_event_time = %q", resp.LatestEventTime)
	}
}

func TestEmptyDatabaseReturnsZeroSummaryAndEmptyCollections(t *testing.T) {
	handler := emptyTestHandler(t)

	for _, path := range []string{
		"/api/summary?range=day",
		"/api/timeseries?range=day&bucket=hour",
		"/api/models?range=day",
		"/api/chains?range=day",
		"/api/events?range=day",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestTimeseriesEndpoint(t *testing.T) {
	handler := testHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/timeseries?range=week&bucket=day", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp TimeseriesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(resp.Points) != 2 {
		t.Fatalf("points len = %d, want 2; points=%+v", len(resp.Points), resp.Points)
	}
	if resp.Points[1].Requests != 2 || resp.Points[1].TotalTokens != 70 {
		t.Fatalf("points[1] = %+v", resp.Points[1])
	}
}

func TestChainsAndEventsEndpoints(t *testing.T) {
	handler := testHandler(t)

	chainReq := httptest.NewRequest(http.MethodGet, "/api/chains?range=week&limit=10", nil)
	chainRec := httptest.NewRecorder()
	handler.ServeHTTP(chainRec, chainReq)
	if chainRec.Code != http.StatusOK {
		t.Fatalf("chains status = %d body=%s", chainRec.Code, chainRec.Body.String())
	}
	var chains ChainsResponse
	if err := json.Unmarshal(chainRec.Body.Bytes(), &chains); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(chains.Items) != 2 {
		t.Fatalf("chain count = %d, want 2", len(chains.Items))
	}
	if chains.Items[0].ChainRootResponseID != "resp_root" || chains.Items[0].ResponseCount != 2 {
		t.Fatalf("first chain = %+v", chains.Items[0])
	}

	eventReq := httptest.NewRequest(http.MethodGet, "/api/events?range=week&chain_root_response_id=resp_root&limit=5", nil)
	eventRec := httptest.NewRecorder()
	handler.ServeHTTP(eventRec, eventReq)
	if eventRec.Code != http.StatusOK {
		t.Fatalf("events status = %d body=%s", eventRec.Code, eventRec.Body.String())
	}
	var events EventsResponse
	if err := json.Unmarshal(eventRec.Body.Bytes(), &events); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(events.Items) != 2 {
		t.Fatalf("event count = %d, want 2", len(events.Items))
	}
	if events.Items[0].ResponseID != "resp_child" {
		t.Fatalf("first event = %+v", events.Items[0])
	}
}

func TestModelsEndpointAndValidation(t *testing.T) {
	handler := testHandler(t)

	modelReq := httptest.NewRequest(http.MethodGet, "/api/models?range=week", nil)
	modelRec := httptest.NewRecorder()
	handler.ServeHTTP(modelRec, modelReq)
	if modelRec.Code != http.StatusOK {
		t.Fatalf("models status = %d body=%s", modelRec.Code, modelRec.Body.String())
	}
	var models ModelsResponse
	if err := json.Unmarshal(modelRec.Body.Bytes(), &models); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(models.Items) != 2 {
		t.Fatalf("model count = %d", len(models.Items))
	}
	if models.Items[0].Model != "gpt-4.1" || models.Items[0].TotalTokens != 70 {
		t.Fatalf("first model = %+v", models.Items[0])
	}

	badReq := httptest.NewRequest(http.MethodGet, "/api/summary?range=quarter", nil)
	badRec := httptest.NewRecorder()
	handler.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("bad status = %d body=%s", badRec.Code, badRec.Body.String())
	}
}

func TestPricingIsAggregatedPerModel(t *testing.T) {
	handler := testHandlerWithPricing(t, &pricing.Catalog{
		Currency: "USD",
		Unit:     pricing.UnitPer1MTokens,
		Models: map[string]pricing.Rate{
			"gpt-4.1": {Input: 1, CachedInput: 0.1, Output: 10},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/summary?range=week", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary status = %d body=%s", rec.Code, rec.Body.String())
	}
	var summary SummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if summary.Cost.Status != "partial" || summary.Cost.PricedTokens != 70 || summary.Cost.UnpricedTokens != 40 {
		t.Fatalf("summary cost = %+v", summary.Cost)
	}
	if summary.Cost.EstimatedCost <= 0 {
		t.Fatalf("estimated cost = %f", summary.Cost.EstimatedCost)
	}

	modelReq := httptest.NewRequest(http.MethodGet, "/api/models?range=week", nil)
	modelRec := httptest.NewRecorder()
	handler.ServeHTTP(modelRec, modelReq)
	if modelRec.Code != http.StatusOK {
		t.Fatalf("models status = %d body=%s", modelRec.Code, modelRec.Body.String())
	}
	var models ModelsResponse
	if err := json.Unmarshal(modelRec.Body.Bytes(), &models); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if models.Items[0].Cost.Status != "priced" || models.Items[1].Cost.Status != "unpriced" {
		t.Fatalf("model costs = %+v", models.Items)
	}
}

func TestPricingAppearsOnEventsAndChains(t *testing.T) {
	handler := testHandlerWithPricing(t, &pricing.Catalog{
		Currency: "USD",
		Unit:     pricing.UnitPer1MTokens,
		Models: map[string]pricing.Rate{
			"gpt-4.1": {Input: 1, CachedInput: 0.1, Output: 10},
		},
	})

	chainReq := httptest.NewRequest(http.MethodGet, "/api/chains?range=week&limit=10", nil)
	chainRec := httptest.NewRecorder()
	handler.ServeHTTP(chainRec, chainReq)
	if chainRec.Code != http.StatusOK {
		t.Fatalf("chains status = %d body=%s", chainRec.Code, chainRec.Body.String())
	}
	var chains ChainsResponse
	if err := json.Unmarshal(chainRec.Body.Bytes(), &chains); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if chains.Items[0].ChainRootResponseID != "resp_root" || chains.Items[0].Cost.Status != "priced" {
		t.Fatalf("first chain = %+v", chains.Items[0])
	}

	eventReq := httptest.NewRequest(http.MethodGet, "/api/events?range=week&limit=5", nil)
	eventRec := httptest.NewRecorder()
	handler.ServeHTTP(eventRec, eventReq)
	if eventRec.Code != http.StatusOK {
		t.Fatalf("events status = %d body=%s", eventRec.Code, eventRec.Body.String())
	}
	var events EventsResponse
	if err := json.Unmarshal(eventRec.Body.Bytes(), &events); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if events.Items[0].Cost.Status != "priced" || events.Items[1].Cost.Status != "unpriced" {
		t.Fatalf("event costs = %+v", events.Items)
	}
}

func TestUnknownAPIPathReturnsJSONNotFound(t *testing.T) {
	handler := testHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/missing", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
}

func TestStaticIndexServed(t *testing.T) {
	handler := testHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
}

func testHandler(t *testing.T) http.Handler {
	return testHandlerWithPricing(t, nil)
}

func testHandlerWithPricing(t *testing.T, catalog *pricing.Catalog) http.Handler {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "usage.db")
	jsonlPath := filepath.Join(dir, "usage.jsonl")
	sink, err := store.Open(context.Background(), dbPath, jsonlPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	events := []event.Usage{
		testUsage("resp_root", "", "gpt-4.1", "https-json", "2026-06-20T09:00:00Z", 40),
		testUsage("resp_child", "resp_root", "gpt-4.1", "websocket", "2026-06-21T11:00:00Z", 30),
		testUsage("resp_other", "", "gpt-4o-mini", "https-json", "2026-06-21T08:30:00Z", 40),
	}
	if _, err := sink.WriteBatch(context.Background(), events); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}

	handler, db, err := newHandler(Config{DBPath: dbPath, Pricing: catalog}, func() time.Time {
		return time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("newHandler() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return handler
}

func emptyTestHandler(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "usage.db")
	jsonlPath := filepath.Join(dir, "usage.jsonl")
	sink, err := store.Open(context.Background(), dbPath, jsonlPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	handler, db, err := newHandler(Config{DBPath: dbPath}, func() time.Time {
		return time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("newHandler() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return handler
}

func testUsage(responseID, previousResponseID, model, transport, ts string, total int64) event.Usage {
	return event.Usage{
		Schema:             event.SchemaVersion,
		Timestamp:          ts,
		Source:             "mitmproxy",
		Transport:          transport,
		Host:               "api.openai.com",
		Path:               "/v1/responses",
		ResponseID:         responseID,
		PreviousResponseID: previousResponseID,
		Model:              model,
		InputTokens:        total / 2,
		OutputTokens:       total / 2,
		TotalTokens:        total,
		CachedTokens:       total / 10,
		ReasoningTokens:    total / 5,
	}
}

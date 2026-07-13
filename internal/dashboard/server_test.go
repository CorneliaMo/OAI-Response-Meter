package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
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
		"/api/heatmap?range=day",
		"/api/rate-limits?range=day",
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

func TestSummaryAndTimeseriesIncludeCacheWriteTokens(t *testing.T) {
	usages := []event.Usage{
		{
			Schema:           event.SchemaVersion,
			Timestamp:        "2026-06-21T08:00:00Z",
			Source:           "mitmproxy",
			Transport:        "https-json",
			Host:             "api.openai.com",
			Path:             "/v1/responses",
			ResponseID:       "resp_cache_write_a",
			Model:            "gpt-test",
			InputTokens:      100,
			OutputTokens:     50,
			TotalTokens:      150,
			CachedTokens:     20,
			CacheWriteTokens: 10,
		},
		{
			Schema:           event.SchemaVersion,
			Timestamp:        "2026-06-21T09:00:00Z",
			Source:           "mitmproxy",
			Transport:        "https-json",
			Host:             "api.openai.com",
			Path:             "/v1/responses",
			ResponseID:       "resp_cache_write_b",
			Model:            "gpt-test",
			InputTokens:      80,
			OutputTokens:     20,
			TotalTokens:      100,
			CachedTokens:     15,
			CacheWriteTokens: 5,
		},
	}
	handler := testHandlerWithAllEvents(t, nil, usages, nil)

	summary := requestSummary(t, handler, "/api/summary?range=day")
	if summary.CacheWriteTokens != 15 {
		t.Fatalf("summary cache_write_tokens = %d, want 15", summary.CacheWriteTokens)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/timeseries?range=day&bucket=day", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("timeseries status = %d body=%s", rec.Code, rec.Body.String())
	}
	var timeseries TimeseriesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &timeseries); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(timeseries.Points) != 1 || timeseries.Points[0].CacheWriteTokens != 15 {
		t.Fatalf("timeseries points = %+v", timeseries.Points)
	}
}

func TestDatedModelSnapshotsAreGroupedAndFilteredByBaseModel(t *testing.T) {
	catalog := &pricing.Catalog{
		Currency: "USD",
		Unit:     pricing.UnitPer1MTokens,
		Models: map[string]pricing.Rate{
			"gpt-5.4-mini": {Input: 1, CachedInput: 0.1, Output: 2},
		},
	}
	usages := []event.Usage{
		testUsage("resp_model_base", "", "gpt-5.4-mini", "https-json", "2026-06-21T08:00:00Z", 100),
		testUsage("resp_model_snapshot", "", "gpt-5.4-mini-2026-03-17", "https-json", "2026-06-21T09:00:00Z", 100),
	}
	handler := testHandlerWithAllEvents(t, catalog, usages, nil)

	models := requestModels(t, handler, "/api/models?range=day")
	if len(models.Items) != 1 || models.Items[0].Model != "gpt-5.4-mini" || models.Items[0].Requests != 2 {
		t.Fatalf("models = %+v", models.Items)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/events?range=day&model=gpt-5.4-mini", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("events status = %d body=%s", rec.Code, rec.Body.String())
	}
	var events EventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &events); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(events.Items) != 2 || events.Items[1].Model != "gpt-5.4-mini" {
		t.Fatalf("events = %+v", events.Items)
	}
}

func TestRangeUsesRequestedTimezoneBoundaries(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}
	window, err := parseRange("day", "", "", time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC), loc)
	if err != nil {
		t.Fatalf("parseRange() error = %v", err)
	}
	if got, want := window.cutoff.Format(time.RFC3339), "2026-06-20T16:00:00Z"; got != want {
		t.Fatalf("day cutoff = %s, want %s", got, want)
	}
	window, err = parseRange("week", "", "", time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC), loc)
	if err != nil {
		t.Fatalf("parseRange() error = %v", err)
	}
	if got, want := window.cutoff.Format(time.RFC3339), "2026-06-14T16:00:00Z"; got != want {
		t.Fatalf("week cutoff = %s, want %s", got, want)
	}
}

func TestRangeSupportsCustomDateBounds(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}
	window, err := parseRange("week", "2026-06-19", "2026-06-20", time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC), loc)
	if err != nil {
		t.Fatalf("parseRange() error = %v", err)
	}
	if window.name != "custom" {
		t.Fatalf("range name = %q", window.name)
	}
	if got, want := window.cutoff.Format(time.RFC3339), "2026-06-18T16:00:00Z"; got != want {
		t.Fatalf("cutoff = %s, want %s", got, want)
	}
	if window.end == nil {
		t.Fatal("expected end bound")
	}
	if got, want := window.end.Format(time.RFC3339), "2026-06-20T16:00:00Z"; got != want {
		t.Fatalf("end = %s, want %s", got, want)
	}
}

func TestRangeRejectsInvalidCustomDateBounds(t *testing.T) {
	loc := time.UTC
	if _, err := parseRange("day", "2026-06-01", "", time.Now().UTC(), loc); err == nil {
		t.Fatal("expected error for missing to")
	}
	if _, err := parseRange("day", "2026-06-02", "2026-06-01", time.Now().UTC(), loc); err == nil {
		t.Fatal("expected error for reversed bounds")
	}
	if _, err := parseRange("day", "20260602", "2026-06-03", time.Now().UTC(), loc); err == nil {
		t.Fatal("expected error for invalid date format")
	}
}

func TestSummaryAndTimeseriesUseRequestedTimezone(t *testing.T) {
	handler := testHandlerWithAllEvents(t, nil, []event.Usage{
		testUsage("resp_before_local_day", "", "gpt-4.1", "https-json", "2026-06-20T15:30:00Z", 10),
		testUsage("resp_local_midnight", "", "gpt-4.1", "https-json", "2026-06-20T16:30:00Z", 20),
		testUsage("resp_local_morning", "", "gpt-4.1", "https-json", "2026-06-21T01:00:00Z", 30),
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/summary?range=day&tz=Asia%2FShanghai", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary status = %d body=%s", rec.Code, rec.Body.String())
	}
	var summary SummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if summary.Requests != 2 || summary.TotalTokens != 50 {
		t.Fatalf("summary = %+v", summary)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/timeseries?range=day&bucket=day&tz=Asia%2FShanghai", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("timeseries status = %d body=%s", rec.Code, rec.Body.String())
	}
	var timeseries TimeseriesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &timeseries); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(timeseries.Points) != 1 {
		t.Fatalf("points = %+v", timeseries.Points)
	}
	if timeseries.Points[0].Time != "2026-06-21T00:00:00+08:00" || timeseries.Points[0].TotalTokens != 50 {
		t.Fatalf("point = %+v", timeseries.Points[0])
	}
}

func TestCustomDateWindowAppliesAcrossEndpoints(t *testing.T) {
	handler := testHandler(t)

	for _, path := range []string{
		"/api/summary?range=week&from=2026-06-21&to=2026-06-21",
		"/api/timeseries?range=week&bucket=day&from=2026-06-21&to=2026-06-21",
		"/api/models?range=week&from=2026-06-21&to=2026-06-21",
		"/api/chains?range=week&from=2026-06-21&to=2026-06-21",
		"/api/events?range=week&from=2026-06-21&to=2026-06-21",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}

	summaryRec := httptest.NewRecorder()
	handler.ServeHTTP(summaryRec, httptest.NewRequest(http.MethodGet, "/api/summary?range=week&from=2026-06-21&to=2026-06-21", nil))
	var summary SummaryResponse
	if err := json.Unmarshal(summaryRec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if summary.Requests != 2 || summary.TotalTokens != 70 {
		t.Fatalf("summary = %+v", summary)
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

func TestEventsFiltersAndSort(t *testing.T) {
	usages := []event.Usage{
		testUsage("resp_root", "", "gpt-4.1", "https-json", "2026-06-20T09:00:00Z", 40),
		testUsage("resp_child", "resp_root", "gpt-4.1", "websocket", "2026-06-21T11:00:00Z", 30),
		testUsage("resp_other", "", "gpt-4o-mini", "https-json", "2026-06-21T08:30:00Z", 50),
	}
	usages[0].PromptCacheKey = "alpha"
	usages[1].PromptCacheKey = "beta"
	usages[2].PromptCacheKey = "alpha"
	handler := testHandlerWithAllEvents(t, nil, usages, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/events?range=week&model=gpt-4.1&transport=websocket&prompt_cache_key=beta&sort=total_desc", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("events status = %d body=%s", rec.Code, rec.Body.String())
	}
	var events EventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &events); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(events.Items) != 1 || events.Items[0].ResponseID != "resp_child" {
		t.Fatalf("events = %+v", events.Items)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/events?range=week&sort=total_desc", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("events status = %d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &events); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(events.Items) < 2 || events.Items[0].TotalTokens < events.Items[1].TotalTokens {
		t.Fatalf("unexpected sort order: %+v", events.Items)
	}

	badReq := httptest.NewRequest(http.MethodGet, "/api/events?range=week&sort=weird", nil)
	badRec := httptest.NewRecorder()
	handler.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("bad status = %d body=%s", badRec.Code, badRec.Body.String())
	}
}

func TestHeatmapEndpointReturns365DaysAndHighlightsRange(t *testing.T) {
	handler := testHandlerWithAllEvents(t, nil, []event.Usage{
		testUsage("resp_old", "", "gpt-4.1", "https-json", "2025-07-03T10:00:00Z", 10),
		testUsage("resp_today", "", "gpt-4.1", "https-json", "2026-06-21T09:00:00Z", 20),
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/heatmap?range=week", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp HeatmapResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(resp.Days) != 365 {
		t.Fatalf("days len = %d", len(resp.Days))
	}
	if resp.Days[0].Date != "2025-06-22" || resp.Days[len(resp.Days)-1].Date != "2026-06-21" {
		t.Fatalf("unexpected date range: first=%s last=%s", resp.Days[0].Date, resp.Days[len(resp.Days)-1].Date)
	}
	var highlighted int
	var today HeatmapDay
	for _, day := range resp.Days {
		if day.InRange {
			highlighted++
		}
		if day.Date == "2026-06-21" {
			today = day
		}
	}
	if highlighted != 7 {
		t.Fatalf("highlighted days = %d", highlighted)
	}
	if today.Requests != 1 || today.TotalTokens != 20 || !today.InRange {
		t.Fatalf("today = %+v", today)
	}
}

func TestRateLimitsEndpoint(t *testing.T) {
	rateLimits := []event.RateLimits{
		testRateLimit("2026-06-21T08:00:00Z", "plus", true, false, 40, 300, 1_781_881_906, 20, 10080, 1_782_380_758),
		testRateLimit("2026-06-21T08:30:00Z", "plus", true, false, 55, 300, 1_781_883_706, 25, 10080, 1_782_382_558),
		testRateLimit("2026-06-21T11:00:00Z", "plus", false, true, 90, 300, 1_782_039_906, 45, 10080, 1_782_391_558),
	}
	handler := testHandlerWithAllEvents(t, nil, nil, rateLimits)

	req := httptest.NewRequest(http.MethodGet, "/api/rate-limits?range=day&limit=1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp RateLimitsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if resp.Bucket != "event" || resp.Limit != 1 || len(resp.Items) != 1 || len(resp.Points) != 3 {
		t.Fatalf("response = %+v", resp)
	}
	if !resp.Items[0].LimitReached || resp.Items[0].FiveHourResetAt != "2026-06-21T11:05:06Z" {
		t.Fatalf("first item = %+v", resp.Items[0])
	}
	if resp.Points[1].Time != "2026-06-21T08:30:00Z" || resp.Points[1].FiveHourUsedPercent == nil || *resp.Points[1].FiveHourUsedPercent != 55 || resp.Points[1].WeeklyUsedPercent == nil || *resp.Points[1].WeeklyUsedPercent != 25 {
		t.Fatalf("middle point = %+v", resp.Points)
	}
	if resp.Points[2].FiveHourUsedPercent == nil || *resp.Points[2].FiveHourUsedPercent != 90 || resp.Points[2].WeeklyUsedPercent == nil || *resp.Points[2].WeeklyUsedPercent != 45 {
		t.Fatalf("points = %+v", resp.Points)
	}
}

func TestRateLimitsEndpointLeavesFiveHourUnknownWhenOnlyWeeklyWindowExists(t *testing.T) {
	rateLimits := []event.RateLimits{
		testRateLimit("2026-06-21T08:00:00Z", "plus", true, false, 0, 0, 0, 100, 10080, 1_782_380_758),
	}
	handler := testHandlerWithAllEvents(t, nil, nil, rateLimits)

	resp := requestRateLimits(t, handler)
	if len(resp.Points) != 1 || resp.Points[0].FiveHourUsedPercent != nil {
		t.Fatalf("five-hour point = %+v, want unknown", resp.Points)
	}
	if resp.Points[0].WeeklyUsedPercent == nil || *resp.Points[0].WeeklyUsedPercent != 100 {
		t.Fatalf("weekly point = %+v, want 100%%", resp.Points)
	}
	if len(resp.Items) != 1 || resp.Items[0].FiveHourUsedPercent != nil {
		t.Fatalf("five-hour item = %+v, want unknown", resp.Items)
	}
	if resp.Items[0].WeeklyUsedPercent == nil || *resp.Items[0].WeeklyUsedPercent != 100 {
		t.Fatalf("weekly item = %+v, want 100%%", resp.Items)
	}
}

func TestRateLimitWindowEstimatesUseScopeResetWindow(t *testing.T) {
	catalog := &pricing.Catalog{
		Currency: "USD",
		Unit:     pricing.UnitPer1MTokens,
		Models: map[string]pricing.Rate{
			"gpt-test": {Input: 1, CachedInput: 0, Output: 1},
		},
	}
	usages := []event.Usage{
		testUsage("estimate_1", "", "gpt-test", "https-json", "2026-06-21T08:10:00Z", 100_000),
		testUsage("estimate_2", "", "gpt-test", "https-json", "2026-06-21T08:40:00Z", 100_000),
	}
	rateLimits := []event.RateLimits{
		testRateLimit("2026-06-21T08:00:00Z", "plus", true, false, 10, 300, 1_782_000_000, 50, 10080, 1_782_500_000),
		testRateLimit("2026-06-21T08:30:00Z", "plus", true, false, 20, 300, 1_782_000_000, 60, 10080, 1_782_500_000),
		testRateLimit("2026-06-21T09:00:00Z", "plus", true, false, 30, 300, 1_782_100_000, 70, 10080, 1_782_500_000),
	}
	handler := testHandlerWithAllEvents(t, catalog, usages, rateLimits)

	req := httptest.NewRequest(http.MethodGet, "/api/rate-limits?range=day", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp RateLimitsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	var secondary *RateLimitWindowEstimate
	for i := range resp.Estimates {
		if resp.Estimates[i].Scope == "weekly" && resp.Estimates[i].ResetAt == 1_782_500_000 {
			secondary = &resp.Estimates[i]
			break
		}
	}
	if secondary == nil {
		t.Fatalf("secondary estimate missing: %+v", resp.Estimates)
	}
	if secondary.Events != 3 || secondary.Pairs != 2 || secondary.Observations != 3 {
		t.Fatalf("secondary estimate used wrong sample set: %+v", *secondary)
	}
	if !secondary.Feasible || secondary.Status != "estimated" || secondary.BestLimit <= 0 {
		t.Fatalf("secondary estimate not feasible: %+v", *secondary)
	}
	if secondary.TotalVisibleCost <= 0 {
		t.Fatalf("secondary visible cost = %f", secondary.TotalVisibleCost)
	}
}

func TestRateLimitWindowEstimatesUseCacheWriteCost(t *testing.T) {
	cacheWriteRate := 1.25
	catalog := &pricing.Catalog{
		Currency: "USD",
		Unit:     pricing.UnitPer1MTokens,
		Models: map[string]pricing.Rate{
			"gpt-test": {Input: 1, CachedInput: 0, CacheWriteInput: &cacheWriteRate, Output: 1},
		},
	}
	usages := []event.Usage{
		{
			Schema:           event.SchemaVersion,
			Timestamp:        "2026-06-21T08:10:00Z",
			Source:           "mitmproxy",
			Transport:        "https-json",
			Host:             "api.openai.com",
			Path:             "/v1/responses",
			ResponseID:       "estimate_cache_write_1",
			Model:            "gpt-test",
			InputTokens:      100_000,
			OutputTokens:     0,
			TotalTokens:      100_000,
			CacheWriteTokens: 100_000,
		},
		{
			Schema:           event.SchemaVersion,
			Timestamp:        "2026-06-21T08:40:00Z",
			Source:           "mitmproxy",
			Transport:        "https-json",
			Host:             "api.openai.com",
			Path:             "/v1/responses",
			ResponseID:       "estimate_cache_write_2",
			Model:            "gpt-test",
			InputTokens:      100_000,
			OutputTokens:     0,
			TotalTokens:      100_000,
			CacheWriteTokens: 100_000,
		},
	}
	rateLimits := []event.RateLimits{
		testRateLimit("2026-06-21T08:00:00Z", "plus", true, false, 10, 300, 1_782_000_000, 50, 10080, 1_782_500_000),
		testRateLimit("2026-06-21T08:30:00Z", "plus", true, false, 20, 300, 1_782_000_000, 60, 10080, 1_782_500_000),
		testRateLimit("2026-06-21T09:00:00Z", "plus", true, false, 30, 300, 1_782_100_000, 70, 10080, 1_782_500_000),
	}
	handler := testHandlerWithAllEvents(t, catalog, usages, rateLimits)

	resp := requestRateLimits(t, handler)
	secondary := findEstimate(t, resp.Estimates, "weekly", 1_782_500_000)
	if math.Abs(secondary.TotalVisibleCost-0.25) > 0.000001 {
		t.Fatalf("secondary total_visible_cost = %f, want 0.25", secondary.TotalVisibleCost)
	}
}

func TestRateLimitWindowEstimateCachePersistsExpiredWindow(t *testing.T) {
	catalog := &pricing.Catalog{
		Currency: "USD",
		Unit:     pricing.UnitPer1MTokens,
		Models: map[string]pricing.Rate{
			"gpt-test": {Input: 1, CachedInput: 0, Output: 1},
		},
	}
	resetAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC).Unix()
	usages := []event.Usage{
		testUsage("cache_1", "", "gpt-test", "https-json", "2026-06-21T08:10:00Z", 100_000),
		testUsage("cache_2", "", "gpt-test", "https-json", "2026-06-21T08:40:00Z", 100_000),
	}
	rateLimits := []event.RateLimits{
		testRateLimit("2026-06-21T08:00:00Z", "plus", true, false, 10, 300, resetAt, 50, 10080, resetAt),
		testRateLimit("2026-06-21T08:30:00Z", "plus", true, false, 20, 300, resetAt, 60, 10080, resetAt),
		testRateLimit("2026-06-21T09:00:00Z", "plus", true, false, 30, 300, resetAt, 70, 10080, resetAt),
	}
	handler, db := testHandlerWithAllEventsAndNow(t, catalog, usages, rateLimits, func() time.Time {
		return time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	})

	first := requestRateLimits(t, handler)
	firstPrimary := findEstimate(t, first.Estimates, "five_hour", resetAt)
	if firstPrimary.BestLimit <= 0 {
		t.Fatalf("first primary estimate = %+v", firstPrimary)
	}
	var cached int
	if err := db.QueryRowContext(context.Background(), `select count(*) from rate_limit_window_estimate_cache where reset_at = ?`, resetAt).Scan(&cached); err != nil {
		t.Fatalf("query cache count error = %v", err)
	}
	if cached != 2 {
		t.Fatalf("cached rows = %d, want 2", cached)
	}
	if _, err := db.ExecContext(context.Background(), `delete from usage_events`); err != nil {
		t.Fatalf("delete usage_events error = %v", err)
	}

	second := requestRateLimits(t, handler)
	secondPrimary := findEstimate(t, second.Estimates, "five_hour", resetAt)
	if secondPrimary.BestLimit != firstPrimary.BestLimit || secondPrimary.Observations != firstPrimary.Observations {
		t.Fatalf("cache was not reused: first=%+v second=%+v", firstPrimary, secondPrimary)
	}
}

func TestRateLimitWindowEstimateCacheSkipsActiveWindow(t *testing.T) {
	catalog := &pricing.Catalog{
		Currency: "USD",
		Unit:     pricing.UnitPer1MTokens,
		Models: map[string]pricing.Rate{
			"gpt-test": {Input: 1, CachedInput: 0, Output: 1},
		},
	}
	resetAt := time.Date(2026, 6, 21, 11, 30, 0, 0, time.UTC).Unix()
	handler, db := testHandlerWithAllEventsAndNow(t, catalog, []event.Usage{
		testUsage("active_cache_1", "", "gpt-test", "https-json", "2026-06-21T08:10:00Z", 100_000),
	}, []event.RateLimits{
		testRateLimit("2026-06-21T08:00:00Z", "plus", true, false, 10, 300, resetAt, 50, 10080, resetAt),
		testRateLimit("2026-06-21T08:30:00Z", "plus", true, false, 20, 300, resetAt, 60, 10080, resetAt),
	}, func() time.Time {
		return time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	})

	_ = requestRateLimits(t, handler)
	var cached int
	if err := db.QueryRowContext(context.Background(), `select count(*) from rate_limit_window_estimate_cache where reset_at = ?`, resetAt).Scan(&cached); err != nil {
		t.Fatalf("query cache count error = %v", err)
	}
	if cached != 0 {
		t.Fatalf("cached rows = %d, want 0", cached)
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

func TestTieredPricingUsesPerEventCostsInAggregates(t *testing.T) {
	catalog := &pricing.Catalog{
		Currency: "USD",
		Unit:     pricing.UnitPer1MTokens,
		Models: map[string]pricing.Rate{
			"gpt-tiered": {
				Input:       2,
				CachedInput: 0.2,
				Output:      8,
				Tiers: []pricing.Tier{
					{MinInputTokens: 272001, Input: 5, CachedInput: 0.5, Output: 20},
				},
			},
		},
	}
	usages := []event.Usage{
		{
			Schema:              event.SchemaVersion,
			Timestamp:           "2026-06-21T08:00:00Z",
			Source:              "mitmproxy",
			Transport:           "https-json",
			Host:                "api.openai.com",
			Path:                "/v1/responses",
			ResponseID:          "resp_a",
			ChainRootResponseID: "resp_a",
			Model:               "gpt-tiered",
			InputTokens:         200_000,
			OutputTokens:        100_000,
			TotalTokens:         300_000,
		},
		{
			Schema:              event.SchemaVersion,
			Timestamp:           "2026-06-21T09:00:00Z",
			Source:              "mitmproxy",
			Transport:           "https-json",
			Host:                "api.openai.com",
			Path:                "/v1/responses",
			ResponseID:          "resp_b",
			ChainRootResponseID: "resp_b",
			Model:               "gpt-tiered",
			InputTokens:         200_000,
			OutputTokens:        100_000,
			TotalTokens:         300_000,
		},
		{
			Schema:              event.SchemaVersion,
			Timestamp:           "2026-06-21T10:00:00Z",
			Source:              "mitmproxy",
			Transport:           "https-json",
			Host:                "api.openai.com",
			Path:                "/v1/responses",
			ResponseID:          "resp_c",
			ChainRootResponseID: "resp_c",
			Model:               "gpt-tiered",
			InputTokens:         272_001,
			OutputTokens:        100_000,
			TotalTokens:         372_001,
		},
	}
	handler := testHandlerWithAllEvents(t, catalog, usages, nil)

	summary := requestSummary(t, handler, "/api/summary?range=day")
	wantSummaryCost := 2*(200_000.0/1_000_000*2+100_000.0/1_000_000*8) + 272_001.0/1_000_000*5 + 100_000.0/1_000_000*20
	if math.Abs(summary.Cost.EstimatedCost-wantSummaryCost) > 0.000001 {
		t.Fatalf("summary cost = %f, want %f", summary.Cost.EstimatedCost, wantSummaryCost)
	}

	models := requestModels(t, handler, "/api/models?range=day")
	if len(models.Items) != 1 {
		t.Fatalf("models = %+v", models.Items)
	}
	if math.Abs(models.Items[0].Cost.EstimatedCost-wantSummaryCost) > 0.000001 {
		t.Fatalf("model cost = %f, want %f", models.Items[0].Cost.EstimatedCost, wantSummaryCost)
	}

	chains := requestChains(t, handler, "/api/chains?range=day&limit=10")
	if len(chains.Items) != 3 {
		t.Fatalf("chains = %+v", chains.Items)
	}
	if math.Abs(chains.Items[0].Cost.EstimatedCost-(272_001.0/1_000_000*5+100_000.0/1_000_000*20)) > 0.000001 {
		t.Fatalf("tiered chain cost = %+v", chains.Items[0])
	}
	if math.Abs(chains.Items[1].Cost.EstimatedCost-(200_000.0/1_000_000*2+100_000.0/1_000_000*8)) > 0.000001 {
		t.Fatalf("base chain cost = %+v", chains.Items[1])
	}
	if math.Abs(chains.Items[2].Cost.EstimatedCost-(200_000.0/1_000_000*2+100_000.0/1_000_000*8)) > 0.000001 {
		t.Fatalf("base chain cost = %+v", chains.Items[2])
	}
}

func TestRateLimitEstimatePriceSignatureIncludesTiers(t *testing.T) {
	cacheWriteBase := 2.5
	cacheWriteTier := 6.25
	baseCatalog := &pricing.Catalog{
		Currency: "USD",
		Unit:     pricing.UnitPer1MTokens,
		Models: map[string]pricing.Rate{
			"gpt-test": {Input: 2, CachedInput: 0.2, CacheWriteInput: &cacheWriteBase, Output: 8},
		},
	}
	tieredCatalog := &pricing.Catalog{
		Currency: "USD",
		Unit:     pricing.UnitPer1MTokens,
		Models: map[string]pricing.Rate{
			"gpt-test": {
				Input:           2,
				CachedInput:     0.2,
				CacheWriteInput: &cacheWriteBase,
				Output:          8,
				Tiers: []pricing.Tier{
					{MinInputTokens: 272001, Input: 5, CachedInput: 0.5, CacheWriteInput: &cacheWriteTier, Output: 20},
				},
			},
		},
	}

	if rateLimitEstimatePriceSignature(baseCatalog) == rateLimitEstimatePriceSignature(tieredCatalog) {
		t.Fatal("expected distinct signatures when pricing tiers change")
	}
}

func TestRateLimitEstimatePriceSignatureIncludesCacheWriteRate(t *testing.T) {
	cacheWriteA := 2.5
	cacheWriteB := 3.0
	catalogA := &pricing.Catalog{
		Currency: "USD",
		Unit:     pricing.UnitPer1MTokens,
		Models: map[string]pricing.Rate{
			"gpt-test": {Input: 2, CachedInput: 0.2, CacheWriteInput: &cacheWriteA, Output: 8},
		},
	}
	catalogB := &pricing.Catalog{
		Currency: "USD",
		Unit:     pricing.UnitPer1MTokens,
		Models: map[string]pricing.Rate{
			"gpt-test": {Input: 2, CachedInput: 0.2, CacheWriteInput: &cacheWriteB, Output: 8},
		},
	}
	if rateLimitEstimatePriceSignature(catalogA) == rateLimitEstimatePriceSignature(catalogB) {
		t.Fatal("expected distinct signatures when cache write pricing changes")
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

func requestRateLimits(t *testing.T, handler http.Handler) RateLimitsResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/rate-limits?range=day", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp RateLimitsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return resp
}

func requestSummary(t *testing.T, handler http.Handler, path string) SummaryResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp SummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return resp
}

func requestModels(t *testing.T, handler http.Handler, path string) ModelsResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp ModelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return resp
}

func requestChains(t *testing.T, handler http.Handler, path string) ChainsResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp ChainsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return resp
}

func findEstimate(t *testing.T, estimates []RateLimitWindowEstimate, scope string, resetAt int64) RateLimitWindowEstimate {
	t.Helper()
	for _, estimate := range estimates {
		if estimate.Scope == scope && estimate.ResetAt == resetAt {
			return estimate
		}
	}
	t.Fatalf("%s estimate reset_at=%d missing: %+v", scope, resetAt, estimates)
	return RateLimitWindowEstimate{}
}

func testHandler(t *testing.T) http.Handler {
	return testHandlerWithPricing(t, nil)
}

func testHandlerWithPricing(t *testing.T, catalog *pricing.Catalog) http.Handler {
	t.Helper()
	usages := []event.Usage{
		testUsage("resp_root", "", "gpt-4.1", "https-json", "2026-06-20T09:00:00Z", 40),
		testUsage("resp_child", "resp_root", "gpt-4.1", "websocket", "2026-06-21T11:00:00Z", 30),
		testUsage("resp_other", "", "gpt-4o-mini", "https-json", "2026-06-21T08:30:00Z", 40),
	}
	return testHandlerWithAllEvents(t, catalog, usages, nil)
}

func testHandlerWithAllEvents(t *testing.T, catalog *pricing.Catalog, usages []event.Usage, rateLimits []event.RateLimits) http.Handler {
	t.Helper()
	handler, _ := testHandlerWithAllEventsAndNow(t, catalog, usages, rateLimits, func() time.Time {
		return time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	})
	return handler
}

func testHandlerWithAllEventsAndNow(t *testing.T, catalog *pricing.Catalog, usages []event.Usage, rateLimits []event.RateLimits, now func() time.Time) (http.Handler, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "usage.db")
	jsonlPath := filepath.Join(dir, "usage.jsonl")
	sink, err := store.Open(context.Background(), dbPath, jsonlPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	if _, err := sink.WriteBatch(context.Background(), usages); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	if _, err := sink.WriteRateLimitBatch(context.Background(), rateLimits); err != nil {
		t.Fatalf("WriteRateLimitBatch() error = %v", err)
	}

	handler, db, err := newHandler(Config{DBPath: dbPath, Pricing: catalog}, now)
	if err != nil {
		t.Fatalf("newHandler() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return handler, db
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

func testRateLimit(ts, plan string, allowed, reached bool, fiveHourUsed, fiveHourWindow, fiveHourResetAt, weeklyUsed, weeklyWindow, weeklyResetAt int64) event.RateLimits {
	return event.RateLimits{
		Schema:                    event.SchemaVersion,
		EventType:                 event.RateLimitsEventType,
		Timestamp:                 ts,
		Source:                    "mitmproxy",
		Transport:                 "https-json",
		Host:                      "api.openai.com",
		Path:                      "/v1/responses",
		PlanType:                  plan,
		Allowed:                   allowed,
		LimitReached:              reached,
		FiveHourUsedPercent:       fiveHourUsed,
		FiveHourWindowMinutes:     fiveHourWindow,
		FiveHourResetAfterSeconds: 30,
		FiveHourResetAt:           fiveHourResetAt,
		WeeklyUsedPercent:         weeklyUsed,
		WeeklyWindowMinutes:       weeklyWindow,
		WeeklyResetAfterSeconds:   60,
		WeeklyResetAt:             weeklyResetAt,
		RawJSON:                   `{"type":"codex.rate_limits"}`,
	}
}

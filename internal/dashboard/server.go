package dashboard

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cornelia/oai-response-meter/internal/pricing"
	_ "modernc.org/sqlite"
)

//go:embed static
var embeddedStatic embed.FS

type Config struct {
	Addr    string
	DBPath  string
	Pricing *pricing.Catalog
}

type Server struct {
	addr       string
	db         *sql.DB
	httpServer *http.Server
	listener   net.Listener
}

type SummaryResponse struct {
	Range           string       `json:"range"`
	Requests        int64        `json:"requests"`
	TotalTokens     int64        `json:"total_tokens"`
	InputTokens     int64        `json:"input_tokens"`
	OutputTokens    int64        `json:"output_tokens"`
	CachedTokens    int64        `json:"cached_tokens"`
	ReasoningTokens int64        `json:"reasoning_tokens"`
	CacheRatio      float64      `json:"cache_ratio"`
	ReasoningRatio  float64      `json:"reasoning_ratio"`
	LatestEventTime string       `json:"latest_event_time"`
	Cost            pricing.Cost `json:"cost"`
}

type TimeseriesResponse struct {
	Range  string            `json:"range"`
	Bucket string            `json:"bucket"`
	Points []TimeseriesPoint `json:"points"`
}

type TimeseriesPoint struct {
	Time            string       `json:"time"`
	Requests        int64        `json:"requests"`
	TotalTokens     int64        `json:"total_tokens"`
	InputTokens     int64        `json:"input_tokens"`
	OutputTokens    int64        `json:"output_tokens"`
	CachedTokens    int64        `json:"cached_tokens"`
	ReasoningTokens int64        `json:"reasoning_tokens"`
	Cost            pricing.Cost `json:"cost"`
}

type ModelsResponse struct {
	Items []ModelItem `json:"items"`
}

type ModelItem struct {
	Model           string       `json:"model"`
	Requests        int64        `json:"requests"`
	TotalTokens     int64        `json:"total_tokens"`
	InputTokens     int64        `json:"input_tokens"`
	OutputTokens    int64        `json:"output_tokens"`
	CachedTokens    int64        `json:"cached_tokens"`
	ReasoningTokens int64        `json:"reasoning_tokens"`
	Cost            pricing.Cost `json:"cost"`
}

type ChainsResponse struct {
	Items []ChainItem `json:"items"`
}

type ChainItem struct {
	ChainRootResponseID string       `json:"chain_root_response_id"`
	ResponseCount       int64        `json:"response_count"`
	StartedAt           string       `json:"started_at"`
	EndedAt             string       `json:"ended_at"`
	Models              []string     `json:"models"`
	Transports          []string     `json:"transports"`
	TotalTokens         int64        `json:"total_tokens"`
	InputTokens         int64        `json:"input_tokens"`
	OutputTokens        int64        `json:"output_tokens"`
	CachedTokens        int64        `json:"cached_tokens"`
	ReasoningTokens     int64        `json:"reasoning_tokens"`
	Cost                pricing.Cost `json:"cost"`
}

type EventsResponse struct {
	Items  []EventItem `json:"items"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
}

type EventItem struct {
	Timestamp           string       `json:"ts"`
	Transport           string       `json:"transport"`
	Host                string       `json:"host"`
	Path                string       `json:"path"`
	ResponseID          string       `json:"response_id"`
	PreviousResponseID  string       `json:"previous_response_id"`
	ChainRootResponseID string       `json:"chain_root_response_id"`
	PromptCacheKey      string       `json:"prompt_cache_key"`
	Model               string       `json:"model"`
	InputTokens         int64        `json:"input_tokens"`
	OutputTokens        int64        `json:"output_tokens"`
	TotalTokens         int64        `json:"total_tokens"`
	CachedTokens        int64        `json:"cached_tokens"`
	ReasoningTokens     int64        `json:"reasoning_tokens"`
	Cost                pricing.Cost `json:"cost"`
}

type HeatmapResponse struct {
	Range string       `json:"range"`
	Days  []HeatmapDay `json:"days"`
}

type HeatmapDay struct {
	Date            string       `json:"date"`
	Requests        int64        `json:"requests"`
	TotalTokens     int64        `json:"total_tokens"`
	InputTokens     int64        `json:"input_tokens"`
	OutputTokens    int64        `json:"output_tokens"`
	CachedTokens    int64        `json:"cached_tokens"`
	ReasoningTokens int64        `json:"reasoning_tokens"`
	Cost            pricing.Cost `json:"cost"`
	InRange         bool         `json:"in_range"`
}

type RateLimitsResponse struct {
	Range     string                    `json:"range"`
	Bucket    string                    `json:"bucket"`
	Items     []RateLimitItem           `json:"items"`
	Points    []RateLimitPoint          `json:"points"`
	Estimates []RateLimitWindowEstimate `json:"estimates"`
	Limit     int                       `json:"limit"`
	Offset    int                       `json:"offset"`
}

type RateLimitItem struct {
	Timestamp                  string `json:"ts"`
	Transport                  string `json:"transport"`
	Host                       string `json:"host"`
	Path                       string `json:"path"`
	PlanType                   string `json:"plan_type"`
	Allowed                    bool   `json:"allowed"`
	LimitReached               bool   `json:"limit_reached"`
	PrimaryUsedPercent         int64  `json:"primary_used_percent"`
	PrimaryWindowMinutes       int64  `json:"primary_window_minutes"`
	PrimaryResetAfterSeconds   int64  `json:"primary_reset_after_seconds"`
	PrimaryResetAt             string `json:"primary_reset_at"`
	SecondaryUsedPercent       int64  `json:"secondary_used_percent"`
	SecondaryWindowMinutes     int64  `json:"secondary_window_minutes"`
	SecondaryResetAfterSeconds int64  `json:"secondary_reset_after_seconds"`
	SecondaryResetAt           string `json:"secondary_reset_at"`
	RawJSON                    string `json:"raw_json"`
}

type RateLimitPoint struct {
	Time                 string `json:"time"`
	PrimaryUsedPercent   int64  `json:"primary_used_percent"`
	SecondaryUsedPercent int64  `json:"secondary_used_percent"`
	Events               int64  `json:"events"`
}

type RateLimitWindowEstimate struct {
	Scope              string  `json:"scope"`
	ResetAt            int64   `json:"reset_at"`
	ResetAtTime        string  `json:"reset_at_time"`
	WindowMinutes      int64   `json:"window_minutes"`
	Events             int     `json:"events"`
	Pairs              int     `json:"pairs"`
	SkippedPairs       int     `json:"skipped_pairs"`
	Observations       int     `json:"observations"`
	TotalVisibleCost   float64 `json:"total_visible_cost"`
	Feasible           bool    `json:"feasible"`
	MinimumMargin      float64 `json:"minimum_margin"`
	LimitLow           float64 `json:"limit_low"`
	LimitHigh          float64 `json:"limit_high"`
	BestLimit          float64 `json:"best_limit"`
	BestInitialUsed    float64 `json:"best_initial_used"`
	BestInitialPercent float64 `json:"best_initial_percent"`
	BestScore          float64 `json:"best_score"`
	Status             string  `json:"status"`
	Message            string  `json:"message"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type EventFilters struct {
	ChainRootResponseID string
	Model               string
	Transport           string
	PromptCacheKey      string
	Sort                string
}

type usageEventRow struct {
	Timestamp           string
	Transport           string
	ChainRootResponseID string
	Model               string
	InputTokens         int64
	OutputTokens        int64
	TotalTokens         int64
	CachedTokens        int64
	ReasoningTokens     int64
}

func Start(ctx context.Context, config Config) (*Server, error) {
	handler, db, err := newHandler(config, time.Now)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", config.Addr)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("listen dashboard: %w", err)
	}

	server := &Server{
		addr:     listener.Addr().String(),
		db:       db,
		listener: listener,
	}
	server.httpServer = &http.Server{
		Addr:              config.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Close(shutdownCtx)
	}()
	go func() {
		err := server.httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			_ = server.Close(context.Background())
		}
	}()

	return server, nil
}

func (s *Server) Close(ctx context.Context) error {
	var err error
	if s.httpServer != nil {
		err = errors.Join(err, s.httpServer.Shutdown(ctx))
	}
	if s.db != nil {
		err = errors.Join(err, s.db.Close())
		s.db = nil
	}
	return err
}

func (s *Server) URL() string {
	return "http://" + s.addr
}

func NewHandler(config Config) (http.Handler, *sql.DB, error) {
	return newHandler(config, time.Now)
}

func newHandler(config Config, now func() time.Time) (http.Handler, *sql.DB, error) {
	if strings.TrimSpace(config.DBPath) == "" {
		return nil, nil, errors.New("dashboard db path is required")
	}
	db, err := sql.Open("sqlite", config.DBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := initRateLimitEstimateCache(context.Background(), db); err != nil {
		db.Close()
		return nil, nil, err
	}

	staticFS, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("static fs: %w", err)
	}

	server := apiServer{
		db:       db,
		now:      now,
		staticFS: staticFS,
		pricing:  config.Pricing,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/summary", server.handleSummary)
	mux.HandleFunc("/api/timeseries", server.handleTimeseries)
	mux.HandleFunc("/api/models", server.handleModels)
	mux.HandleFunc("/api/chains", server.handleChains)
	mux.HandleFunc("/api/events", server.handleEvents)
	mux.HandleFunc("/api/heatmap", server.handleHeatmap)
	mux.HandleFunc("/api/rate-limits", server.handleRateLimits)
	mux.HandleFunc("/api/", server.handleAPINotFound)
	mux.HandleFunc("/", server.handleStatic)
	return mux, db, nil
}

func initRateLimitEstimateCache(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `create table if not exists rate_limit_window_estimate_cache (
  scope text not null,
  reset_at integer not null,
  price_signature text not null,
  pairs integer not null default 0,
  skipped_pairs integer not null default 0,
  observations integer not null default 0,
  total_visible_cost real not null default 0,
  feasible integer not null default 0,
  minimum_margin real not null default 0,
  limit_low real not null default 0,
  limit_high real not null default 0,
  best_limit real not null default 0,
  best_initial_used real not null default 0,
  best_initial_percent real not null default 0,
  best_score real not null default 0,
  status text not null default '',
  message text not null default '',
  created_at text not null default current_timestamp,
  updated_at text not null default current_timestamp,
  primary key (scope, reset_at, price_signature)
)`)
	if err != nil {
		return fmt.Errorf("init rate limit estimate cache: %w", err)
	}
	return nil
}

type apiServer struct {
	db       *sql.DB
	now      func() time.Time
	staticFS fs.FS
	pricing  *pricing.Catalog
}

func (s apiServer) handleSummary(w http.ResponseWriter, r *http.Request) {
	window, err := parseQueryWindow(r, s.now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := querySummary(r.Context(), s.db, window, s.pricing)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s apiServer) handleTimeseries(w http.ResponseWriter, r *http.Request) {
	window, err := parseQueryWindow(r, s.now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	bucket, err := parseBucket(r.URL.Query().Get("bucket"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := queryTimeseries(r.Context(), s.db, window, bucket, s.pricing)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s apiServer) handleModels(w http.ResponseWriter, r *http.Request) {
	window, err := parseQueryWindow(r, s.now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := queryModels(r.Context(), s.db, window, s.pricing)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s apiServer) handleChains(w http.ResponseWriter, r *http.Request) {
	window, err := parseQueryWindow(r, s.now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"), 50)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := queryChains(r.Context(), s.db, window, limit, s.pricing)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s apiServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	window, err := parseQueryWindow(r, s.now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"), 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	offset, err := parseOffset(r.URL.Query().Get("offset"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	filters, err := parseEventFilters(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := queryEvents(r.Context(), s.db, window, limit, offset, filters, s.pricing)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s apiServer) handleHeatmap(w http.ResponseWriter, r *http.Request) {
	window, err := parseQueryWindow(r, s.now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := queryHeatmap(r.Context(), s.db, window, s.now().UTC(), s.pricing)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s apiServer) handleRateLimits(w http.ResponseWriter, r *http.Request) {
	window, err := parseQueryWindow(r, s.now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"), 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	offset, err := parseOffset(r.URL.Query().Get("offset"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := queryRateLimits(r.Context(), s.db, window, limit, offset, s.pricing, s.now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s apiServer) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, fmt.Errorf("unknown api path %q", r.URL.Path))
}

func (s apiServer) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := "index.html"
	if cleaned := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/"); cleaned != "" && cleaned != "." {
		name = cleaned
	}
	data, err := fs.ReadFile(s.staticFS, name)
	if err != nil {
		data, err = fs.ReadFile(s.staticFS, "index.html")
		if err != nil {
			http.Error(w, "dashboard asset missing", http.StatusInternalServerError)
			return
		}
		name = "index.html"
	}
	w.Header().Set("Content-Type", contentType(name))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(data)
}

type queryWindow struct {
	name      string
	cutoff    time.Time
	end       *time.Time
	location  *time.Location
	startDay  time.Time
	endDay    time.Time
	fromLabel string
	toLabel   string
}

func requestLocation(r *http.Request) *time.Location {
	value := strings.TrimSpace(r.URL.Query().Get("tz"))
	if value == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(value)
	if err != nil {
		return time.UTC
	}
	return loc
}

func parseQueryWindow(r *http.Request, now time.Time) (queryWindow, error) {
	query := r.URL.Query()
	return parseRange(query.Get("range"), query.Get("from"), query.Get("to"), now, requestLocation(r))
}

func parseRange(value, fromValue, toValue string, now time.Time, loc *time.Location) (queryWindow, error) {
	if loc == nil {
		loc = time.UTC
	}
	localNow := now.In(loc)
	localToday := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)

	fromValue = strings.TrimSpace(fromValue)
	toValue = strings.TrimSpace(toValue)
	if (fromValue == "") != (toValue == "") {
		return queryWindow{}, errors.New("from and to must be supplied together")
	}
	if fromValue != "" {
		fromDay, err := parseLocalDate(fromValue, loc)
		if err != nil {
			return queryWindow{}, fmt.Errorf("invalid from %q", fromValue)
		}
		toDay, err := parseLocalDate(toValue, loc)
		if err != nil {
			return queryWindow{}, fmt.Errorf("invalid to %q", toValue)
		}
		if fromDay.After(toDay) {
			return queryWindow{}, errors.New("from must be on or before to")
		}
		endDay := toDay.AddDate(0, 0, 1)
		endUTC := endDay.UTC()
		return queryWindow{
			name:      "custom",
			cutoff:    fromDay.UTC(),
			end:       &endUTC,
			location:  loc,
			startDay:  fromDay,
			endDay:    endDay,
			fromLabel: fromValue,
			toLabel:   toValue,
		}, nil
	}

	window := queryWindow{
		name:     defaultRangeName(value),
		location: loc,
		endDay:   localToday.AddDate(0, 0, 1),
	}
	switch value {
	case "", "day":
		window.cutoff = localToday.UTC()
		window.startDay = localToday
	case "week":
		weekday := int(localToday.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		window.startDay = localToday.AddDate(0, 0, 1-weekday)
		window.cutoff = window.startDay.UTC()
	case "month":
		window.startDay = time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, loc)
		window.cutoff = window.startDay.UTC()
	case "year":
		window.startDay = time.Date(localNow.Year(), 1, 1, 0, 0, 0, 0, loc)
		window.cutoff = window.startDay.UTC()
	default:
		return queryWindow{}, fmt.Errorf("invalid range %q", value)
	}
	return window, nil
}

func defaultRangeName(value string) string {
	if strings.TrimSpace(value) == "" {
		return "day"
	}
	return value
}

func parseLocalDate(value string, loc *time.Location) (time.Time, error) {
	day, err := time.ParseInLocation("2006-01-02", value, loc)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc), nil
}

func parseBucket(value string) (string, error) {
	switch value {
	case "", "hour":
		return "hour", nil
	case "day":
		return "day", nil
	case "month":
		return "month", nil
	default:
		return "", fmt.Errorf("invalid bucket %q", value)
	}
}

func parseLimit(value string, fallback int) (int, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return 0, fmt.Errorf("invalid limit %q", value)
	}
	if limit > 500 {
		limit = 500
	}
	return limit, nil
}

func parseOffset(value string) (int, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(value)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid offset %q", value)
	}
	return offset, nil
}

func parseEventFilters(r *http.Request) (EventFilters, error) {
	sortValue := strings.TrimSpace(r.URL.Query().Get("sort"))
	if sortValue == "" {
		sortValue = "ts_desc"
	}
	if _, ok := eventSortSQL(sortValue); !ok {
		return EventFilters{}, fmt.Errorf("invalid sort %q", sortValue)
	}
	return EventFilters{
		ChainRootResponseID: strings.TrimSpace(r.URL.Query().Get("chain_root_response_id")),
		Model:               strings.TrimSpace(r.URL.Query().Get("model")),
		Transport:           strings.TrimSpace(r.URL.Query().Get("transport")),
		PromptCacheKey:      strings.TrimSpace(r.URL.Query().Get("prompt_cache_key")),
		Sort:                sortValue,
	}, nil
}

func querySummary(ctx context.Context, db *sql.DB, window queryWindow, catalog *pricing.Catalog) (SummaryResponse, error) {
	resp := SummaryResponse{Range: window.name}
	rows, err := queryUsageEventsInWindow(ctx, db, window)
	if err != nil {
		return SummaryResponse{}, err
	}

	for _, row := range rows {
		resp.Requests++
		resp.TotalTokens += row.TotalTokens
		resp.InputTokens += row.InputTokens
		resp.OutputTokens += row.OutputTokens
		resp.CachedTokens += row.CachedTokens
		resp.ReasoningTokens += row.ReasoningTokens
		if row.Timestamp > resp.LatestEventTime {
			resp.LatestEventTime = row.Timestamp
		}
		resp.Cost = pricing.Add(resp.Cost, estimateCost(catalog, row.Model, row.InputTokens, row.OutputTokens, row.CachedTokens, row.TotalTokens))
	}
	if resp.InputTokens > 0 {
		resp.CacheRatio = float64(resp.CachedTokens) / float64(resp.InputTokens)
	}
	if resp.TotalTokens > 0 {
		resp.ReasoningRatio = float64(resp.ReasoningTokens) / float64(resp.TotalTokens)
	}
	return resp, nil
}

func queryTimeseries(ctx context.Context, db *sql.DB, window queryWindow, bucket string, catalog *pricing.Catalog) (TimeseriesResponse, error) {
	query := `
select
  ts,
  coalesce(nullif(model, ''), '(unknown)'),
  total_tokens,
  input_tokens,
  output_tokens,
  cached_tokens,
  reasoning_tokens
from usage_events
where ts >= ?
`
	args := []any{window.cutoff.Format(time.RFC3339)}
	appendUpperBound(&query, &args, window)
	query += `
order by ts asc
`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return TimeseriesResponse{}, fmt.Errorf("query timeseries: %w", err)
	}
	defer rows.Close()

	resp := TimeseriesResponse{Range: window.name, Bucket: bucket}
	points := map[string]*TimeseriesPoint{}
	for rows.Next() {
		var ts string
		var model string
		var total, input, output, cached, reasoning int64
		if err := rows.Scan(&ts, &model, &total, &input, &output, &cached, &reasoning); err != nil {
			return TimeseriesResponse{}, fmt.Errorf("scan timeseries: %w", err)
		}
		eventTime, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			return TimeseriesResponse{}, fmt.Errorf("parse timeseries timestamp %q: %w", ts, err)
		}
		bucketTime := bucketStart(eventTime, window.location, bucket).Format(time.RFC3339)
		item := points[bucketTime]
		if item == nil {
			item = &TimeseriesPoint{Time: bucketTime}
			points[bucketTime] = item
		}
		item.Requests++
		item.TotalTokens += total
		item.InputTokens += input
		item.OutputTokens += output
		item.CachedTokens += cached
		item.ReasoningTokens += reasoning
		item.Cost = pricing.Add(item.Cost, estimateCost(catalog, model, input, output, cached, total))
	}
	if err := rows.Err(); err != nil {
		return TimeseriesResponse{}, fmt.Errorf("iterate timeseries: %w", err)
	}
	for _, item := range points {
		resp.Points = append(resp.Points, *item)
	}
	slices.SortFunc(resp.Points, func(a, b TimeseriesPoint) int {
		return strings.Compare(a.Time, b.Time)
	})
	return resp, nil
}

func bucketStart(ts time.Time, loc *time.Location, bucket string) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := ts.In(loc)
	switch bucket {
	case "hour":
		return time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, loc)
	case "month":
		return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	default:
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	}
}

func queryModels(ctx context.Context, db *sql.DB, window queryWindow, catalog *pricing.Catalog) (ModelsResponse, error) {
	rows, err := queryUsageEventsInWindow(ctx, db, window)
	if err != nil {
		return ModelsResponse{}, err
	}

	items := map[string]*ModelItem{}
	for _, row := range rows {
		item := items[row.Model]
		if item == nil {
			item = &ModelItem{Model: row.Model}
			items[row.Model] = item
		}
		item.Requests++
		item.TotalTokens += row.TotalTokens
		item.InputTokens += row.InputTokens
		item.OutputTokens += row.OutputTokens
		item.CachedTokens += row.CachedTokens
		item.ReasoningTokens += row.ReasoningTokens
		item.Cost = pricing.Add(item.Cost, estimateCost(catalog, row.Model, row.InputTokens, row.OutputTokens, row.CachedTokens, row.TotalTokens))
	}
	resp := ModelsResponse{Items: make([]ModelItem, 0, len(items))}
	for _, item := range items {
		resp.Items = append(resp.Items, *item)
	}
	slices.SortFunc(resp.Items, func(a, b ModelItem) int {
		if a.TotalTokens != b.TotalTokens {
			if a.TotalTokens > b.TotalTokens {
				return -1
			}
			return 1
		}
		if a.Requests != b.Requests {
			if a.Requests > b.Requests {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Model, b.Model)
	})
	return resp, nil
}

func queryChains(ctx context.Context, db *sql.DB, window queryWindow, limit int, catalog *pricing.Catalog) (ChainsResponse, error) {
	rows, err := queryUsageEventsInWindow(ctx, db, window)
	if err != nil {
		return ChainsResponse{}, err
	}

	type chainAggregate struct {
		item       *ChainItem
		models     map[string]struct{}
		transports map[string]struct{}
	}
	chains := map[string]*chainAggregate{}
	for _, row := range rows {
		chain := chains[row.ChainRootResponseID]
		if chain == nil {
			chain = &chainAggregate{
				item: &ChainItem{
					ChainRootResponseID: row.ChainRootResponseID,
					StartedAt:           row.Timestamp,
					EndedAt:             row.Timestamp,
				},
				models:     map[string]struct{}{},
				transports: map[string]struct{}{},
			}
			chains[row.ChainRootResponseID] = chain
		}
		item := chain.item
		item.ResponseCount++
		item.TotalTokens += row.TotalTokens
		item.InputTokens += row.InputTokens
		item.OutputTokens += row.OutputTokens
		item.CachedTokens += row.CachedTokens
		item.ReasoningTokens += row.ReasoningTokens
		item.Cost = pricing.Add(item.Cost, estimateCost(catalog, row.Model, row.InputTokens, row.OutputTokens, row.CachedTokens, row.TotalTokens))
		if row.Timestamp < item.StartedAt {
			item.StartedAt = row.Timestamp
		}
		if row.Timestamp > item.EndedAt {
			item.EndedAt = row.Timestamp
		}
		if row.Model != "" && row.Model != "(unknown)" {
			chain.models[row.Model] = struct{}{}
		}
		if row.Transport != "" {
			chain.transports[row.Transport] = struct{}{}
		}
	}
	resp := ChainsResponse{}
	for _, chain := range chains {
		for model := range chain.models {
			chain.item.Models = append(chain.item.Models, model)
		}
		for transport := range chain.transports {
			chain.item.Transports = append(chain.item.Transports, transport)
		}
		slices.Sort(chain.item.Models)
		slices.Sort(chain.item.Transports)
		resp.Items = append(resp.Items, *chain.item)
	}
	slices.SortFunc(resp.Items, func(a, b ChainItem) int {
		return strings.Compare(b.EndedAt, a.EndedAt)
	})
	if len(resp.Items) > limit {
		resp.Items = resp.Items[:limit]
	}
	return resp, nil
}

func queryEvents(ctx context.Context, db *sql.DB, window queryWindow, limit, offset int, filters EventFilters, catalog *pricing.Catalog) (EventsResponse, error) {
	query := `
select
  ts,
  transport,
  host,
  path,
  response_id,
  previous_response_id,
  chain_root_response_id,
  prompt_cache_key,
  coalesce(model, ''),
  input_tokens,
  output_tokens,
  total_tokens,
  cached_tokens,
  reasoning_tokens
from usage_events
where ts >= ?
`
	args := []any{window.cutoff.Format(time.RFC3339)}
	appendUpperBound(&query, &args, window)
	if filters.ChainRootResponseID != "" {
		query += " and chain_root_response_id = ?"
		args = append(args, filters.ChainRootResponseID)
	}
	if filters.Model != "" {
		query += " and coalesce(nullif(model, ''), '(unknown)') = ?"
		args = append(args, filters.Model)
	}
	if filters.Transport != "" {
		query += " and transport = ?"
		args = append(args, filters.Transport)
	}
	if filters.PromptCacheKey != "" {
		query += " and prompt_cache_key = ?"
		args = append(args, filters.PromptCacheKey)
	}
	orderBy, _ := eventSortSQL(filters.Sort)
	query += " order by " + orderBy + " limit ? offset ?"
	args = append(args, limit, offset)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return EventsResponse{}, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	resp := EventsResponse{Limit: limit, Offset: offset}
	for rows.Next() {
		var item EventItem
		if err := rows.Scan(
			&item.Timestamp,
			&item.Transport,
			&item.Host,
			&item.Path,
			&item.ResponseID,
			&item.PreviousResponseID,
			&item.ChainRootResponseID,
			&item.PromptCacheKey,
			&item.Model,
			&item.InputTokens,
			&item.OutputTokens,
			&item.TotalTokens,
			&item.CachedTokens,
			&item.ReasoningTokens,
		); err != nil {
			return EventsResponse{}, fmt.Errorf("scan events: %w", err)
		}
		item.Cost = estimateCost(catalog, item.Model, item.InputTokens, item.OutputTokens, item.CachedTokens, item.TotalTokens)
		resp.Items = append(resp.Items, item)
	}
	if err := rows.Err(); err != nil {
		return EventsResponse{}, fmt.Errorf("iterate events: %w", err)
	}
	return resp, nil
}

func queryHeatmap(ctx context.Context, db *sql.DB, window queryWindow, now time.Time, catalog *pricing.Catalog) (HeatmapResponse, error) {
	localNow := now.In(window.location)
	localToday := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, window.location)
	heatmapStart := localToday.AddDate(0, 0, -364)
	heatmapEnd := localToday.AddDate(0, 0, 1)
	query := `
select
  ts,
  coalesce(nullif(model, ''), '(unknown)'),
  total_tokens,
  input_tokens,
  output_tokens,
  cached_tokens,
  reasoning_tokens
from usage_events
where ts >= ? and ts < ?
order by ts asc
`
	rows, err := db.QueryContext(ctx, query, heatmapStart.UTC().Format(time.RFC3339), heatmapEnd.UTC().Format(time.RFC3339))
	if err != nil {
		return HeatmapResponse{}, fmt.Errorf("query heatmap: %w", err)
	}
	defer rows.Close()

	byDate := make(map[string]*HeatmapDay, 365)
	for rows.Next() {
		var ts string
		var model string
		var total, input, output, cached, reasoning int64
		if err := rows.Scan(&ts, &model, &total, &input, &output, &cached, &reasoning); err != nil {
			return HeatmapResponse{}, fmt.Errorf("scan heatmap: %w", err)
		}
		eventTime, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			return HeatmapResponse{}, fmt.Errorf("parse heatmap timestamp %q: %w", ts, err)
		}
		localDay := time.Date(eventTime.In(window.location).Year(), eventTime.In(window.location).Month(), eventTime.In(window.location).Day(), 0, 0, 0, 0, window.location)
		key := localDay.Format("2006-01-02")
		day := byDate[key]
		if day == nil {
			day = &HeatmapDay{Date: key}
			byDate[key] = day
		}
		day.Requests++
		day.TotalTokens += total
		day.InputTokens += input
		day.OutputTokens += output
		day.CachedTokens += cached
		day.ReasoningTokens += reasoning
		day.Cost = pricing.Add(day.Cost, estimateCost(catalog, model, input, output, cached, total))
	}
	if err := rows.Err(); err != nil {
		return HeatmapResponse{}, fmt.Errorf("iterate heatmap: %w", err)
	}

	resp := HeatmapResponse{Range: window.name}
	for day := heatmapStart; !day.After(localToday); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		item := byDate[key]
		if item == nil {
			item = &HeatmapDay{Date: key}
		}
		item.InRange = !day.Before(window.startDay) && day.Before(window.endDay)
		resp.Days = append(resp.Days, *item)
	}
	return resp, nil
}

func queryRateLimits(ctx context.Context, db *sql.DB, window queryWindow, limit, offset int, catalog *pricing.Catalog, now time.Time) (RateLimitsResponse, error) {
	pointsQuery := `
select
  ts,
  primary_used_percent,
  secondary_used_percent
from codex_rate_limit_events
where ts >= ?
`
	pointsArgs := []any{window.cutoff.Format(time.RFC3339)}
	appendUpperBound(&pointsQuery, &pointsArgs, window)
	pointsQuery += `
order by ts asc
`
	pointRows, err := db.QueryContext(ctx, pointsQuery, pointsArgs...)
	if err != nil {
		return RateLimitsResponse{}, fmt.Errorf("query rate limit points: %w", err)
	}
	defer pointRows.Close()

	resp := RateLimitsResponse{Range: window.name, Bucket: "event", Limit: limit, Offset: offset}
	for pointRows.Next() {
		var ts string
		var primaryUsed, secondaryUsed int64
		if err := pointRows.Scan(&ts, &primaryUsed, &secondaryUsed); err != nil {
			return RateLimitsResponse{}, fmt.Errorf("scan rate limit point: %w", err)
		}
		resp.Points = append(resp.Points, RateLimitPoint{
			Time:                 ts,
			PrimaryUsedPercent:   primaryUsed,
			SecondaryUsedPercent: secondaryUsed,
			Events:               1,
		})
	}
	if err := pointRows.Err(); err != nil {
		return RateLimitsResponse{}, fmt.Errorf("iterate rate limit points: %w", err)
	}

	itemsQuery := `
select
  ts,
  transport,
  host,
  path,
  plan_type,
  allowed,
  limit_reached,
  primary_used_percent,
  primary_window_minutes,
  primary_reset_after_seconds,
  primary_reset_at,
  secondary_used_percent,
  secondary_window_minutes,
  secondary_reset_after_seconds,
  secondary_reset_at,
  raw_json
from codex_rate_limit_events
where ts >= ?
`
	itemArgs := []any{window.cutoff.Format(time.RFC3339)}
	appendUpperBound(&itemsQuery, &itemArgs, window)
	itemsQuery += `
order by ts desc, id desc
limit ? offset ?
`
	itemArgs = append(itemArgs, limit, offset)
	itemRows, err := db.QueryContext(ctx, itemsQuery, itemArgs...)
	if err != nil {
		return RateLimitsResponse{}, fmt.Errorf("query rate limit items: %w", err)
	}
	defer itemRows.Close()

	for itemRows.Next() {
		var item RateLimitItem
		var allowedInt, limitReachedInt int64
		var primaryResetAt, secondaryResetAt int64
		if err := itemRows.Scan(
			&item.Timestamp,
			&item.Transport,
			&item.Host,
			&item.Path,
			&item.PlanType,
			&allowedInt,
			&limitReachedInt,
			&item.PrimaryUsedPercent,
			&item.PrimaryWindowMinutes,
			&item.PrimaryResetAfterSeconds,
			&primaryResetAt,
			&item.SecondaryUsedPercent,
			&item.SecondaryWindowMinutes,
			&item.SecondaryResetAfterSeconds,
			&secondaryResetAt,
			&item.RawJSON,
		); err != nil {
			return RateLimitsResponse{}, fmt.Errorf("scan rate limit item: %w", err)
		}
		item.Allowed = allowedInt != 0
		item.LimitReached = limitReachedInt != 0
		item.PrimaryResetAt = unixTimeString(primaryResetAt)
		item.SecondaryResetAt = unixTimeString(secondaryResetAt)
		resp.Items = append(resp.Items, item)
	}
	if err := itemRows.Err(); err != nil {
		return RateLimitsResponse{}, fmt.Errorf("iterate rate limit items: %w", err)
	}
	estimates, err := queryRateLimitWindowEstimates(ctx, db, window, catalog, now)
	if err != nil {
		return RateLimitsResponse{}, err
	}
	resp.Estimates = estimates
	return resp, nil
}

type rateLimitWindowRef struct {
	scope         string
	resetAt       int64
	windowMinutes int64
	events        int
}

type rateLimitEstimateEvent struct {
	id                   int64
	ts                   string
	source               string
	transport            string
	host                 string
	path                 string
	primaryUsedPercent   int
	secondaryUsedPercent int
}

type windowObservation struct {
	cumulativeCost float64
	usedPercent    int
}

type windowEstimateResult struct {
	observations       int
	totalVisibleCost   float64
	feasible           bool
	minimumMargin      float64
	limitLow           float64
	limitHigh          float64
	bestLimit          float64
	bestInitialUsed    float64
	bestInitialPercent float64
	bestScore          float64
	status             string
	message            string
}

func queryRateLimitWindowEstimates(ctx context.Context, db *sql.DB, window queryWindow, catalog *pricing.Catalog, now time.Time) ([]RateLimitWindowEstimate, error) {
	refs, err := queryRateLimitWindowRefs(ctx, db, window)
	if err != nil {
		return nil, err
	}
	priceSignature := rateLimitEstimatePriceSignature(catalog)
	estimates := make([]RateLimitWindowEstimate, 0, len(refs))
	for _, ref := range refs {
		result, pairs, skipped, ok, err := readRateLimitEstimateCache(ctx, db, ref, priceSignature)
		if err != nil {
			return nil, err
		}
		if !ok {
			result, pairs, skipped, err = estimateRateLimitWindow(ctx, db, ref, catalog)
			if err != nil {
				return nil, err
			}
			if shouldCacheRateLimitWindow(ref, now) {
				if err := writeRateLimitEstimateCache(ctx, db, ref, priceSignature, result, pairs, skipped); err != nil {
					return nil, err
				}
			}
		}
		estimate := RateLimitWindowEstimate{
			Scope:              ref.scope,
			ResetAt:            ref.resetAt,
			ResetAtTime:        unixTimeString(ref.resetAt),
			WindowMinutes:      ref.windowMinutes,
			Events:             ref.events,
			Pairs:              pairs,
			SkippedPairs:       skipped,
			Observations:       result.observations,
			TotalVisibleCost:   apiFloat(result.totalVisibleCost),
			Feasible:           result.feasible,
			MinimumMargin:      apiFloat(result.minimumMargin),
			LimitLow:           apiFloat(result.limitLow),
			LimitHigh:          apiFloat(result.limitHigh),
			BestLimit:          apiFloat(result.bestLimit),
			BestInitialUsed:    apiFloat(result.bestInitialUsed),
			BestInitialPercent: apiFloat(result.bestInitialPercent),
			BestScore:          apiFloat(result.bestScore),
			Status:             result.status,
			Message:            result.message,
		}
		estimates = append(estimates, estimate)
	}
	return estimates, nil
}

func readRateLimitEstimateCache(ctx context.Context, db *sql.DB, ref rateLimitWindowRef, priceSignature string) (windowEstimateResult, int, int, bool, error) {
	var result windowEstimateResult
	var pairs, skipped, feasibleInt int
	err := db.QueryRowContext(ctx, `
select
  pairs,
  skipped_pairs,
  observations,
  total_visible_cost,
  feasible,
  minimum_margin,
  limit_low,
  limit_high,
  best_limit,
  best_initial_used,
  best_initial_percent,
  best_score,
  status,
  message
from rate_limit_window_estimate_cache
where scope = ? and reset_at = ? and price_signature = ?
`, ref.scope, ref.resetAt, priceSignature).Scan(
		&pairs,
		&skipped,
		&result.observations,
		&result.totalVisibleCost,
		&feasibleInt,
		&result.minimumMargin,
		&result.limitLow,
		&result.limitHigh,
		&result.bestLimit,
		&result.bestInitialUsed,
		&result.bestInitialPercent,
		&result.bestScore,
		&result.status,
		&result.message,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return windowEstimateResult{}, 0, 0, false, nil
	}
	if err != nil {
		return windowEstimateResult{}, 0, 0, false, fmt.Errorf("read rate limit estimate cache: %w", err)
	}
	result.feasible = feasibleInt != 0
	return result, pairs, skipped, true, nil
}

func writeRateLimitEstimateCache(ctx context.Context, db *sql.DB, ref rateLimitWindowRef, priceSignature string, result windowEstimateResult, pairs, skipped int) error {
	feasibleInt := 0
	if result.feasible {
		feasibleInt = 1
	}
	_, err := db.ExecContext(ctx, `
insert into rate_limit_window_estimate_cache (
  scope,
  reset_at,
  price_signature,
  pairs,
  skipped_pairs,
  observations,
  total_visible_cost,
  feasible,
  minimum_margin,
  limit_low,
  limit_high,
  best_limit,
  best_initial_used,
  best_initial_percent,
  best_score,
  status,
  message,
  updated_at
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, current_timestamp)
on conflict(scope, reset_at, price_signature) do update set
  pairs = excluded.pairs,
  skipped_pairs = excluded.skipped_pairs,
  observations = excluded.observations,
  total_visible_cost = excluded.total_visible_cost,
  feasible = excluded.feasible,
  minimum_margin = excluded.minimum_margin,
  limit_low = excluded.limit_low,
  limit_high = excluded.limit_high,
  best_limit = excluded.best_limit,
  best_initial_used = excluded.best_initial_used,
  best_initial_percent = excluded.best_initial_percent,
  best_score = excluded.best_score,
  status = excluded.status,
  message = excluded.message,
  updated_at = current_timestamp
`,
		ref.scope,
		ref.resetAt,
		priceSignature,
		pairs,
		skipped,
		result.observations,
		apiFloat(result.totalVisibleCost),
		feasibleInt,
		apiFloat(result.minimumMargin),
		apiFloat(result.limitLow),
		apiFloat(result.limitHigh),
		apiFloat(result.bestLimit),
		apiFloat(result.bestInitialUsed),
		apiFloat(result.bestInitialPercent),
		apiFloat(result.bestScore),
		result.status,
		result.message,
	)
	if err != nil {
		return fmt.Errorf("write rate limit estimate cache: %w", err)
	}
	return nil
}

func shouldCacheRateLimitWindow(ref rateLimitWindowRef, now time.Time) bool {
	if ref.resetAt <= 0 {
		return false
	}
	return time.Unix(ref.resetAt, 0).UTC().Add(time.Hour).Before(now) || time.Unix(ref.resetAt, 0).UTC().Add(time.Hour).Equal(now)
}

func rateLimitEstimatePriceSignature(catalog *pricing.Catalog) string {
	if catalog == nil {
		return "pricing-disabled"
	}
	models := make([]string, 0, len(catalog.Models))
	for model := range catalog.Models {
		models = append(models, model)
	}
	slices.Sort(models)
	var builder strings.Builder
	fmt.Fprintf(&builder, "currency=%s\nunit=%s\n", catalog.Currency, catalog.Unit)
	for _, model := range models {
		rate := catalog.Models[model]
		fmt.Fprintf(&builder, "%s|%.12g|%.12g|%.12g\n", model, rate.Input, rate.CachedInput, rate.Output)
		for _, tier := range rate.Tiers {
			fmt.Fprintf(&builder, "tier|%d|%.12g|%.12g|%.12g\n", tier.MinInputTokens, tier.Input, tier.CachedInput, tier.Output)
		}
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return fmt.Sprintf("%x", sum[:])
}

func apiFloat(value float64) float64 {
	if !isFiniteFloat(value) {
		return 0
	}
	return value
}

func queryRateLimitWindowRefs(ctx context.Context, db *sql.DB, window queryWindow) ([]rateLimitWindowRef, error) {
	query := `
select scope, reset_at, max(window_minutes), count(*)
from (
  select 'primary' as scope, primary_reset_at as reset_at, primary_window_minutes as window_minutes, ts
  from codex_rate_limit_events
  where ts >= ? and primary_reset_at > 0
  union all
  select 'secondary' as scope, secondary_reset_at as reset_at, secondary_window_minutes as window_minutes, ts
  from codex_rate_limit_events
  where ts >= ? and secondary_reset_at > 0
) windows
where ts >= ?
`
	args := []any{window.cutoff.Format(time.RFC3339), window.cutoff.Format(time.RFC3339), window.cutoff.Format(time.RFC3339)}
	if window.end != nil {
		query += " and ts < ?"
		args = append(args, window.end.Format(time.RFC3339))
	}
	query += `
group by scope, reset_at
order by reset_at desc, scope asc
`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query rate limit estimate windows: %w", err)
	}
	defer rows.Close()

	var refs []rateLimitWindowRef
	for rows.Next() {
		var ref rateLimitWindowRef
		if err := rows.Scan(&ref.scope, &ref.resetAt, &ref.windowMinutes, &ref.events); err != nil {
			return nil, fmt.Errorf("scan rate limit estimate window: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rate limit estimate windows: %w", err)
	}
	return refs, nil
}

func estimateRateLimitWindow(ctx context.Context, db *sql.DB, ref rateLimitWindowRef, catalog *pricing.Catalog) (windowEstimateResult, int, int, error) {
	if catalog == nil {
		return windowEstimateResult{status: "pricing_disabled", message: "prices.json is not loaded."}, 0, 0, nil
	}
	events, err := queryRateLimitWindowEvents(ctx, db, ref)
	if err != nil {
		return windowEstimateResult{}, 0, 0, err
	}
	if len(events) < 2 {
		return windowEstimateResult{observations: len(events), status: "insufficient_data", message: "Not enough rate limit events in this reset window."}, 0, 0, nil
	}

	observations := []windowObservation{{cumulativeCost: 0, usedPercent: rateLimitPercentForScope(events[0], ref.scope)}}
	var cumulativeCost float64
	var skipped int
	pairs := len(events) - 1
	for i := 1; i < len(events); i++ {
		before := events[i-1]
		after := events[i]
		cost, priced, err := usageCostBetweenRateLimitEvents(ctx, db, before, after, catalog)
		if err != nil {
			return windowEstimateResult{}, 0, 0, err
		}
		if !priced {
			skipped++
			continue
		}
		cumulativeCost += cost
		observations = append(observations, windowObservation{
			cumulativeCost: cumulativeCost,
			usedPercent:    rateLimitPercentForScope(after, ref.scope),
		})
	}

	result := estimateWindowLimit(observations, cumulativeCost)
	if skipped > 0 && result.status == "estimated" {
		result.status = "partial"
		result.message = fmt.Sprintf("%d pair(s) skipped because usage cost could not be priced.", skipped)
	} else if skipped > 0 && result.message == "" {
		result.message = fmt.Sprintf("%d pair(s) skipped because usage cost could not be priced.", skipped)
	}
	return result, pairs, skipped, nil
}

func queryRateLimitWindowEvents(ctx context.Context, db *sql.DB, ref rateLimitWindowRef) ([]rateLimitEstimateEvent, error) {
	resetColumn := "primary_reset_at"
	if ref.scope == "secondary" {
		resetColumn = "secondary_reset_at"
	}
	query := fmt.Sprintf(`
select
  id,
  ts,
  source,
  transport,
  host,
  path,
  primary_used_percent,
  secondary_used_percent
from codex_rate_limit_events
where %s = ? and %s > 0
order by julianday(ts) asc, id asc
`, resetColumn, resetColumn)
	rows, err := db.QueryContext(ctx, query, ref.resetAt)
	if err != nil {
		return nil, fmt.Errorf("query %s rate limit estimate events: %w", ref.scope, err)
	}
	defer rows.Close()

	var events []rateLimitEstimateEvent
	for rows.Next() {
		var event rateLimitEstimateEvent
		if err := rows.Scan(
			&event.id,
			&event.ts,
			&event.source,
			&event.transport,
			&event.host,
			&event.path,
			&event.primaryUsedPercent,
			&event.secondaryUsedPercent,
		); err != nil {
			return nil, fmt.Errorf("scan %s rate limit estimate event: %w", ref.scope, err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s rate limit estimate events: %w", ref.scope, err)
	}
	return events, nil
}

func usageCostBetweenRateLimitEvents(ctx context.Context, db *sql.DB, before, after rateLimitEstimateEvent, catalog *pricing.Catalog) (float64, bool, error) {
	rows, err := db.QueryContext(ctx, `
select
  coalesce(model, ''),
  input_tokens,
  output_tokens,
  cached_tokens,
  total_tokens
from usage_events
where julianday(ts) > julianday(?)
  and julianday(ts) < julianday(?)
  and source = ?
  and transport = ?
  and host = ?
  and path = ?
order by julianday(ts) asc, id asc
`, before.ts, after.ts, before.source, before.transport, before.host, before.path)
	if err != nil {
		return 0, false, fmt.Errorf("query usage cost between rate limit events: %w", err)
	}
	defer rows.Close()

	var total float64
	for rows.Next() {
		var model string
		var input, output, cached, totalTokens int64
		if err := rows.Scan(&model, &input, &output, &cached, &totalTokens); err != nil {
			return 0, false, fmt.Errorf("scan usage cost between rate limit events: %w", err)
		}
		cost := estimateCost(catalog, model, input, output, cached, totalTokens)
		if cost.Status != "priced" {
			return 0, false, nil
		}
		total += cost.EstimatedCost
	}
	if err := rows.Err(); err != nil {
		return 0, false, fmt.Errorf("iterate usage cost between rate limit events: %w", err)
	}
	return total, true, nil
}

func rateLimitPercentForScope(event rateLimitEstimateEvent, scope string) int {
	if scope == "secondary" {
		return event.secondaryUsedPercent
	}
	return event.primaryUsedPercent
}

func estimateWindowLimit(observations []windowObservation, totalVisibleCost float64) windowEstimateResult {
	if len(observations) < 2 {
		return windowEstimateResult{
			observations:     len(observations),
			totalVisibleCost: totalVisibleCost,
			status:           "insufficient_data",
			message:          "Not enough priced observations.",
		}
	}

	margin, ok := findMinimumPercentMargin(observations, true, 10.0, 1e-4)
	if !ok {
		return windowEstimateResult{
			observations:     len(observations),
			totalVisibleCost: totalVisibleCost,
			status:           "infeasible",
			message:          "No feasible limit found within the maximum percent margin.",
		}
	}
	low, high, ok := feasibleLimitInterval(observations, margin, true)
	if !ok {
		return windowEstimateResult{
			observations:     len(observations),
			totalVisibleCost: totalVisibleCost,
			minimumMargin:    margin,
			status:           "infeasible",
			message:          "No feasible limit found for the selected margin.",
		}
	}
	result := windowEstimateResult{
		observations:     len(observations),
		totalVisibleCost: totalVisibleCost,
		feasible:         true,
		minimumMargin:    margin,
		limitLow:         low,
		limitHigh:        high,
		status:           "estimated",
	}
	if math.IsInf(high, 1) {
		result.status = "unbounded"
		result.message = "Feasible interval is unbounded above; more percentage changes are needed."
		return result
	}
	if low < 1e-12 {
		low = 1e-12
	}
	bestLimit, bestInitialUsed, bestScore, ok := bestLimitInInterval(observations, low, high, margin, true)
	if !ok {
		result.status = "infeasible"
		result.message = "Could not score the feasible interval."
		return result
	}
	result.bestLimit = bestLimit
	result.bestInitialUsed = bestInitialUsed
	result.bestScore = bestScore
	if bestLimit > 0 {
		result.bestInitialPercent = 100 * bestInitialUsed / bestLimit
	}
	return result
}

func fuzzyPercentInterval(percent int, margin float64) (float64, float64) {
	low := math.Max(0, float64(percent)-margin)
	high := math.Min(100, float64(percent)+margin)
	return low / 100, high / 100
}

func feasibleBInterval(observations []windowObservation, limit, margin float64, requireNonnegativeInitial bool) (float64, float64, bool) {
	bLow := math.Inf(-1)
	if requireNonnegativeInitial {
		bLow = 0
	}
	bHigh := math.Inf(1)
	for _, obs := range observations {
		lowRatio, highRatio := fuzzyPercentInterval(obs.usedPercent, margin)
		bLow = math.Max(bLow, limit*lowRatio-obs.cumulativeCost)
		bHigh = math.Min(bHigh, limit*highRatio-obs.cumulativeCost)
	}
	return bLow, bHigh, bLow <= bHigh
}

func feasibleLimitInterval(observations []windowObservation, margin float64, requireNonnegativeInitial bool) (float64, float64, bool) {
	if len(observations) == 0 {
		return 0, 0, false
	}
	type constraint struct {
		a float64
		d float64
	}
	lowerConstraints := make([]constraint, 0, len(observations)+1)
	upperConstraints := make([]constraint, 0, len(observations))
	for _, obs := range observations {
		lowRatio, highRatio := fuzzyPercentInterval(obs.usedPercent, margin)
		lowerConstraints = append(lowerConstraints, constraint{a: lowRatio, d: -obs.cumulativeCost})
		upperConstraints = append(upperConstraints, constraint{a: highRatio, d: -obs.cumulativeCost})
	}
	if requireNonnegativeInitial {
		lowerConstraints = append(lowerConstraints, constraint{a: 0, d: 0})
	}

	limitLow := 0.0
	limitHigh := math.Inf(1)
	for _, lower := range lowerConstraints {
		for _, upper := range upperConstraints {
			coef := lower.a - upper.a
			rhs := upper.d - lower.d
			if math.Abs(coef) < 1e-18 {
				if rhs < 0 {
					return 0, 0, false
				}
				continue
			}
			bound := rhs / coef
			if coef > 0 {
				limitHigh = math.Min(limitHigh, bound)
			} else {
				limitLow = math.Max(limitLow, bound)
			}
		}
	}
	limitLow = math.Max(limitLow, 0)
	return limitLow, limitHigh, limitLow <= limitHigh
}

func findMinimumPercentMargin(observations []windowObservation, requireNonnegativeInitial bool, maxMargin, precision float64) (float64, bool) {
	if _, _, ok := feasibleLimitInterval(observations, maxMargin, requireNonnegativeInitial); !ok {
		return 0, false
	}
	left := 0.0
	right := maxMargin
	for right-left > precision {
		mid := (left + right) / 2
		if _, _, ok := feasibleLimitInterval(observations, mid, requireNonnegativeInitial); ok {
			right = mid
		} else {
			left = mid
		}
	}
	return right, true
}

func scoreLimit(observations []windowObservation, limit, margin float64, requireNonnegativeInitial bool) (float64, float64, bool) {
	bLow, bHigh, ok := feasibleBInterval(observations, limit, margin, requireNonnegativeInitial)
	if !ok {
		return 0, 0, false
	}
	var targetSum float64
	for _, obs := range observations {
		targetSum += limit*float64(obs.usedPercent)/100 - obs.cumulativeCost
	}
	bStar := targetSum / float64(len(observations))
	bStar = math.Min(math.Max(bStar, bLow), bHigh)

	var squaredError float64
	for _, obs := range observations {
		truePercent := 100 * (bStar + obs.cumulativeCost) / limit
		diff := truePercent - float64(obs.usedPercent)
		squaredError += diff * diff
	}
	return squaredError / float64(len(observations)), bStar, true
}

func bestLimitInInterval(observations []windowObservation, low, high, margin float64, requireNonnegativeInitial bool) (float64, float64, float64, bool) {
	if !isFiniteFloat(low) || !isFiniteFloat(high) {
		return 0, 0, 0, false
	}
	if high <= low {
		score, b, ok := scoreLimit(observations, low, margin, requireNonnegativeInitial)
		return low, b, score, ok
	}
	phi := (1 + math.Sqrt(5)) / 2
	c := high - (high-low)/phi
	d := low + (high-low)/phi
	scoreC, bC, okC := scoreLimit(observations, c, margin, requireNonnegativeInitial)
	scoreD, bD, okD := scoreLimit(observations, d, margin, requireNonnegativeInitial)
	if !okC {
		scoreC = math.Inf(1)
	}
	if !okD {
		scoreD = math.Inf(1)
	}
	for range 200 {
		if scoreC < scoreD {
			high = d
			d = c
			scoreD = scoreC
			bD = bC
			c = high - (high-low)/phi
			scoreC, bC, okC = scoreLimit(observations, c, margin, requireNonnegativeInitial)
			if !okC {
				scoreC = math.Inf(1)
			}
		} else {
			low = c
			c = d
			scoreC = scoreD
			bC = bD
			d = low + (high-low)/phi
			scoreD, bD, okD = scoreLimit(observations, d, margin, requireNonnegativeInitial)
			if !okD {
				scoreD = math.Inf(1)
			}
		}
	}
	bestLimit := c
	bestInitial := bC
	bestScore := scoreC
	if scoreD < bestScore {
		bestLimit = d
		bestInitial = bD
		bestScore = scoreD
	}
	mid := (low + high) / 2
	if scoreMid, bMid, ok := scoreLimit(observations, mid, margin, requireNonnegativeInitial); ok && scoreMid < bestScore {
		bestLimit = mid
		bestInitial = bMid
		bestScore = scoreMid
	}
	return bestLimit, bestInitial, bestScore, isFiniteFloat(bestScore)
}

func isFiniteFloat(value float64) bool {
	return !math.IsInf(value, 0) && !math.IsNaN(value)
}

func unixTimeString(value int64) string {
	if value <= 0 {
		return ""
	}
	return time.Unix(value, 0).UTC().Format(time.RFC3339)
}

func eventSortSQL(value string) (string, bool) {
	switch value {
	case "", "ts_desc":
		return "ts desc, id desc", true
	case "ts_asc":
		return "ts asc, id asc", true
	case "total_desc":
		return "total_tokens desc, ts desc, id desc", true
	case "input_desc":
		return "input_tokens desc, ts desc, id desc", true
	case "output_desc":
		return "output_tokens desc, ts desc, id desc", true
	case "cached_desc":
		return "cached_tokens desc, ts desc, id desc", true
	case "reasoning_desc":
		return "reasoning_tokens desc, ts desc, id desc", true
	default:
		return "", false
	}
}

func appendUpperBound(query *string, args *[]any, window queryWindow) {
	if window.end == nil {
		return
	}
	*query += " and ts < ?"
	*args = append(*args, window.end.Format(time.RFC3339))
}

func queryUsageEventsInWindow(ctx context.Context, db *sql.DB, window queryWindow) ([]usageEventRow, error) {
	query := `
select
  ts,
  transport,
  chain_root_response_id,
  coalesce(nullif(model, ''), '(unknown)'),
  input_tokens,
  output_tokens,
  total_tokens,
  cached_tokens,
  reasoning_tokens
from usage_events
where ts >= ?
`
	args := []any{window.cutoff.Format(time.RFC3339)}
	appendUpperBound(&query, &args, window)
	query += `
order by ts asc, id asc
`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query usage events: %w", err)
	}
	defer rows.Close()

	var events []usageEventRow
	for rows.Next() {
		var row usageEventRow
		if err := rows.Scan(
			&row.Timestamp,
			&row.Transport,
			&row.ChainRootResponseID,
			&row.Model,
			&row.InputTokens,
			&row.OutputTokens,
			&row.TotalTokens,
			&row.CachedTokens,
			&row.ReasoningTokens,
		); err != nil {
			return nil, fmt.Errorf("scan usage events: %w", err)
		}
		events = append(events, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usage events: %w", err)
	}
	return events, nil
}

func estimateCost(catalog *pricing.Catalog, model string, input, output, cached, total int64) pricing.Cost {
	return catalog.Estimate(pricing.Usage{
		Model:        model,
		InputTokens:  input,
		OutputTokens: output,
		CachedTokens: cached,
		TotalTokens:  total,
	})
}

func splitDistinctList(value string) []string {
	if value == "" {
		return nil
	}
	seen := map[string]struct{}{}
	items := strings.Split(value, ",")
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	slices.Sort(out)
	return out
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, `{"error":"encode response"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorResponse{Error: err.Error()})
}

func contentType(name string) string {
	switch path.Ext(name) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	default:
		return "text/html; charset=utf-8"
	}
}

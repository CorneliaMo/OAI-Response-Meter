package dashboard

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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

type errorResponse struct {
	Error string `json:"error"`
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
	mux.HandleFunc("/api/", server.handleAPINotFound)
	mux.HandleFunc("/", server.handleStatic)
	return mux, db, nil
}

type apiServer struct {
	db       *sql.DB
	now      func() time.Time
	staticFS fs.FS
	pricing  *pricing.Catalog
}

func (s apiServer) handleSummary(w http.ResponseWriter, r *http.Request) {
	window, err := parseRange(r.URL.Query().Get("range"), s.now().UTC(), requestLocation(r))
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
	window, err := parseRange(r.URL.Query().Get("range"), s.now().UTC(), requestLocation(r))
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
	window, err := parseRange(r.URL.Query().Get("range"), s.now().UTC(), requestLocation(r))
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
	window, err := parseRange(r.URL.Query().Get("range"), s.now().UTC(), requestLocation(r))
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
	window, err := parseRange(r.URL.Query().Get("range"), s.now().UTC(), requestLocation(r))
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
	resp, err := queryEvents(r.Context(), s.db, window, limit, offset, r.URL.Query().Get("chain_root_response_id"), s.pricing)
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
	name     string
	cutoff   time.Time
	location *time.Location
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

func parseRange(value string, now time.Time, loc *time.Location) (queryWindow, error) {
	if loc == nil {
		loc = time.UTC
	}
	localNow := now.In(loc)
	localStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	switch value {
	case "", "day":
		return queryWindow{name: "day", cutoff: localStart.UTC(), location: loc}, nil
	case "week":
		weekday := int(localStart.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		return queryWindow{name: "week", cutoff: localStart.AddDate(0, 0, 1-weekday).UTC(), location: loc}, nil
	case "month":
		return queryWindow{name: "month", cutoff: time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, loc).UTC(), location: loc}, nil
	case "year":
		return queryWindow{name: "year", cutoff: time.Date(localNow.Year(), 1, 1, 0, 0, 0, 0, loc).UTC(), location: loc}, nil
	default:
		return queryWindow{}, fmt.Errorf("invalid range %q", value)
	}
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

func querySummary(ctx context.Context, db *sql.DB, window queryWindow, catalog *pricing.Catalog) (SummaryResponse, error) {
	resp := SummaryResponse{Range: window.name}
	rows, err := db.QueryContext(ctx, `
select
  coalesce(nullif(model, ''), '(unknown)'),
  count(*),
  coalesce(sum(total_tokens), 0),
  coalesce(sum(input_tokens), 0),
  coalesce(sum(output_tokens), 0),
  coalesce(sum(cached_tokens), 0),
  coalesce(sum(reasoning_tokens), 0),
  coalesce(max(ts), '')
from usage_events
where ts >= ?
group by coalesce(nullif(model, ''), '(unknown)')
`, window.cutoff.Format(time.RFC3339))
	if err != nil {
		return SummaryResponse{}, fmt.Errorf("query summary: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var model string
		var latest string
		var requests, total, input, output, cached, reasoning int64
		if err := rows.Scan(&model, &requests, &total, &input, &output, &cached, &reasoning, &latest); err != nil {
			return SummaryResponse{}, fmt.Errorf("scan summary: %w", err)
		}
		resp.Requests += requests
		resp.TotalTokens += total
		resp.InputTokens += input
		resp.OutputTokens += output
		resp.CachedTokens += cached
		resp.ReasoningTokens += reasoning
		if latest > resp.LatestEventTime {
			resp.LatestEventTime = latest
		}
		resp.Cost = pricing.Add(resp.Cost, estimateCost(catalog, model, input, output, cached, total))
	}
	if err := rows.Err(); err != nil {
		return SummaryResponse{}, fmt.Errorf("iterate summary: %w", err)
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
	rows, err := db.QueryContext(ctx, `
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
order by ts asc
`, window.cutoff.Format(time.RFC3339))
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
		if err := rows.Scan(
			&ts,
			&model,
			&total,
			&input,
			&output,
			&cached,
			&reasoning,
		); err != nil {
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
	rows, err := db.QueryContext(ctx, `
select
  coalesce(nullif(model, ''), '(unknown)'),
  count(*),
  coalesce(sum(total_tokens), 0),
  coalesce(sum(input_tokens), 0),
  coalesce(sum(output_tokens), 0),
  coalesce(sum(cached_tokens), 0),
  coalesce(sum(reasoning_tokens), 0)
from usage_events
where ts >= ?
group by coalesce(nullif(model, ''), '(unknown)')
order by total_tokens desc, count(*) desc, 1 asc
`, window.cutoff.Format(time.RFC3339))
	if err != nil {
		return ModelsResponse{}, fmt.Errorf("query models: %w", err)
	}
	defer rows.Close()

	resp := ModelsResponse{}
	for rows.Next() {
		var item ModelItem
		if err := rows.Scan(
			&item.Model,
			&item.Requests,
			&item.TotalTokens,
			&item.InputTokens,
			&item.OutputTokens,
			&item.CachedTokens,
			&item.ReasoningTokens,
		); err != nil {
			return ModelsResponse{}, fmt.Errorf("scan models: %w", err)
		}
		item.Cost = estimateCost(catalog, item.Model, item.InputTokens, item.OutputTokens, item.CachedTokens, item.TotalTokens)
		resp.Items = append(resp.Items, item)
	}
	if err := rows.Err(); err != nil {
		return ModelsResponse{}, fmt.Errorf("iterate models: %w", err)
	}
	return resp, nil
}

func queryChains(ctx context.Context, db *sql.DB, window queryWindow, limit int, catalog *pricing.Catalog) (ChainsResponse, error) {
	rows, err := db.QueryContext(ctx, `
select
  chain_root_response_id,
  coalesce(nullif(model, ''), '(unknown)'),
  count(*),
  min(ts),
  max(ts),
  coalesce(group_concat(distinct transport), ''),
  coalesce(sum(total_tokens), 0),
  coalesce(sum(input_tokens), 0),
  coalesce(sum(output_tokens), 0),
  coalesce(sum(cached_tokens), 0),
  coalesce(sum(reasoning_tokens), 0)
from usage_events
where ts >= ?
group by chain_root_response_id, coalesce(nullif(model, ''), '(unknown)')
order by max(ts) desc
`, window.cutoff.Format(time.RFC3339))
	if err != nil {
		return ChainsResponse{}, fmt.Errorf("query chains: %w", err)
	}
	defer rows.Close()

	type chainAggregate struct {
		item       *ChainItem
		models     map[string]struct{}
		transports map[string]struct{}
	}
	chains := map[string]*chainAggregate{}
	for rows.Next() {
		var chainID string
		var model string
		var transports string
		var responseCount, total, input, output, cached, reasoning int64
		var startedAt, endedAt string
		if err := rows.Scan(
			&chainID,
			&model,
			&responseCount,
			&startedAt,
			&endedAt,
			&transports,
			&total,
			&input,
			&output,
			&cached,
			&reasoning,
		); err != nil {
			return ChainsResponse{}, fmt.Errorf("scan chains: %w", err)
		}
		chain := chains[chainID]
		if chain == nil {
			chain = &chainAggregate{
				item:       &ChainItem{ChainRootResponseID: chainID, StartedAt: startedAt, EndedAt: endedAt},
				models:     map[string]struct{}{},
				transports: map[string]struct{}{},
			}
			chains[chainID] = chain
		}
		item := chain.item
		item.ResponseCount += responseCount
		item.TotalTokens += total
		item.InputTokens += input
		item.OutputTokens += output
		item.CachedTokens += cached
		item.ReasoningTokens += reasoning
		item.Cost = pricing.Add(item.Cost, estimateCost(catalog, model, input, output, cached, total))
		if startedAt < item.StartedAt {
			item.StartedAt = startedAt
		}
		if endedAt > item.EndedAt {
			item.EndedAt = endedAt
		}
		if model != "" && model != "(unknown)" {
			chain.models[model] = struct{}{}
		}
		for _, transport := range splitDistinctList(transports) {
			chain.transports[transport] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return ChainsResponse{}, fmt.Errorf("iterate chains: %w", err)
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

func queryEvents(ctx context.Context, db *sql.DB, window queryWindow, limit, offset int, chainRootResponseID string, catalog *pricing.Catalog) (EventsResponse, error) {
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
	if chainRootResponseID != "" {
		query += " and chain_root_response_id = ?"
		args = append(args, chainRootResponseID)
	}
	query += " order by ts desc, id desc limit ? offset ?"
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

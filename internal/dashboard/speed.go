package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/cornelia/oai-response-meter/internal/event"
	"github.com/cornelia/oai-response-meter/internal/pricing"
	"github.com/cornelia/oai-response-meter/internal/store"
)

func initSpeedSchema(ctx context.Context, db *sql.DB) error { return store.InitSpeedSchema(ctx, db) }

type SpeedPoint struct {
	Time                     string   `json:"time"`
	Model                    string   `json:"model"`
	Requests                 int      `json:"requests"`
	AvgTokensPerSecond       float64  `json:"avg_tokens_per_second"`
	AvgDurationMS            float64  `json:"avg_duration_ms"`
	AvgRequestDurationMS     *float64 `json:"avg_request_duration_ms"`
	AvgFirstVisibleLatencyMS *float64 `json:"avg_first_visible_latency_ms"`
}

type ActiveSpeed struct {
	ResponseID            string   `json:"response_id"`
	Model                 string   `json:"model"`
	CreatedAt             string   `json:"created_at"`
	UpdatedAt             string   `json:"updated_at"`
	DurationMS            float64  `json:"duration_ms"`
	RequestDurationMS     *float64 `json:"request_duration_ms"`
	FirstVisibleLatencyMS *float64 `json:"first_visible_latency_ms"`
	OutputTokens          int64    `json:"output_tokens"`
	OutputTokensKnown     bool     `json:"output_tokens_known"`
	OutputCharacters      int64    `json:"output_characters"`
	OutputItems           int64    `json:"output_items"`
	TokensPerSecond       *float64 `json:"tokens_per_second"`
	CharactersPerSecond   float64  `json:"characters_per_second"`
}

type SpeedsResponse struct {
	Points                   []SpeedPoint      `json:"points"`
	Active                   []ActiveSpeed     `json:"active"`
	Recent                   []ActiveSpeed     `json:"recent"`
	Models                   []string          `json:"models"`
	CompletedRequests        int               `json:"completed_requests"`
	AvgTokensPerSecond       *float64          `json:"avg_tokens_per_second"`
	AvgFirstVisibleLatencyMS *float64          `json:"avg_first_visible_latency_ms"`
	P50TokensPerSecond       *float64          `json:"p50_tokens_per_second"`
	P95TokensPerSecond       *float64          `json:"p95_tokens_per_second"`
	P50FirstVisibleLatencyMS *float64          `json:"p50_first_visible_latency_ms"`
	P95FirstVisibleLatencyMS *float64          `json:"p95_first_visible_latency_ms"`
	ModelStats               []ModelSpeedStats `json:"model_stats"`
	Tools                    []ToolAggregate   `json:"tools"`
}

type SpeedDistribution struct {
	Samples int      `json:"samples"`
	Mean    *float64 `json:"mean"`
	P50     *float64 `json:"p50"`
	P95     *float64 `json:"p95"`
}

type ModelSpeedStats struct {
	Model               string            `json:"model"`
	OutputSpeed         SpeedDistribution `json:"output_speed"`
	FirstVisibleLatency SpeedDistribution `json:"first_visible_latency"`
}

// Nearest-rank percentiles operate on individual responses, not minute means.
func speedDistribution(values []float64) SpeedDistribution {
	d := SpeedDistribution{Samples: len(values)}
	if len(values) == 0 {
		return d
	}
	sort.Float64s(values)
	var sum float64
	for _, value := range values {
		sum += value
	}
	mean := sum / float64(len(values))
	p50 := values[int(math.Ceil(float64(len(values))*0.50))-1]
	p95 := values[int(math.Ceil(float64(len(values))*0.95))-1]
	d.Mean, d.P50, d.P95 = &mean, &p50, &p95
	return d
}

type ToolAggregate struct {
	Model              string   `json:"model"`
	Name               string   `json:"name"`
	Type               string   `json:"type"`
	Calls              int64    `json:"calls"`
	CompletedCalls     int64    `json:"completed_calls"`
	InputCharacters    int64    `json:"input_characters"`
	AvgInputDurationMS *float64 `json:"avg_input_duration_ms"`
}

func (s apiServer) handleSpeeds(w http.ResponseWriter, r *http.Request) {
	now := s.now().UTC()
	window, err := parseQueryWindow(r, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := querySpeeds(r.Context(), s.db, window, now, r.URL.Query()["model"]...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func querySpeeds(ctx context.Context, db *sql.DB, window queryWindow, now time.Time, models ...string) (SpeedsResponse, error) {
	resp := SpeedsResponse{Points: []SpeedPoint{}, Active: []ActiveSpeed{}, Recent: []ActiveSpeed{}, Models: []string{}, Tools: []ToolAggregate{}, ModelStats: []ModelSpeedStats{}}
	type samples struct{ speed, latency []float64 }
	byModel := map[string]*samples{}
	var speedSamples, latencySamples []float64
	selected := map[string]bool{}
	for _, model := range models {
		selected[pricing.CanonicalModelName(model)] = true
	}
	available := map[string]bool{}
	// Include live snapshots independently of the historical range. Compare parsed
	// timestamps to retain nanosecond precision and handle arbitrary RFC3339 offsets.
	query := `select response_id,model,request_at,created_at,updated_at,completed,output_tokens,output_tokens_known,output_characters,output_items,first_visible_at,tools_json from response_speed_events where (completed = 1 and julianday(created_at) >= julianday(?)`
	// SQLite rounds timestamps to milliseconds; widen SQL bounds and apply exact
	// RFC3339Nano bounds below to avoid losing records adjacent to a boundary.
	args := []any{window.cutoff.Add(-time.Millisecond).Format(time.RFC3339Nano)}
	if window.end != nil {
		query += ` and julianday(created_at) < julianday(?)`
		args = append(args, window.end.Add(time.Millisecond).Format(time.RFC3339Nano))
	}
	query += `) or (completed = 0 and julianday(updated_at) >= julianday(?) and julianday(updated_at) <= julianday(?))`
	args = append(args, now.Add(-10*time.Minute-time.Millisecond).Format(time.RFC3339Nano), now.Add(time.Millisecond).Format(time.RFC3339Nano))
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return resp, err
	}
	defer rows.Close()
	type aggregate struct {
		point        SpeedPoint
		requestCount int
		requestSum   float64
		visibleCount int
		visibleSum   float64
	}
	groups := map[string]*aggregate{}
	var sum float64
	var visibleSum float64
	var visibleCount int
	toolGroups := map[string]*ToolAggregate{}
	toolSums := map[string]float64{}
	for rows.Next() {
		var item ActiveSpeed
		var requestAt string
		var firstVisibleAt, toolsJSON string
		var completed bool
		if err := rows.Scan(&item.ResponseID, &item.Model, &requestAt, &item.CreatedAt, &item.UpdatedAt, &completed, &item.OutputTokens, &item.OutputTokensKnown, &item.OutputCharacters, &item.OutputItems, &firstVisibleAt, &toolsJSON); err != nil {
			return resp, err
		}
		created, err := time.Parse(time.RFC3339Nano, item.CreatedAt)
		if err != nil {
			return resp, err
		}
		updated, err := time.Parse(time.RFC3339Nano, item.UpdatedAt)
		if err != nil {
			return resp, err
		}
		item.Model = pricing.CanonicalModelName(item.Model)
		if item.Model == "" {
			item.Model = "(unknown)"
		}
		inRange := !created.Before(window.cutoff) && (window.end == nil || created.Before(*window.end))
		if completed && inRange && item.OutputTokensKnown {
			available[item.Model] = true
		}
		if len(selected) > 0 && !selected[item.Model] {
			continue
		}
		duration := updated.Sub(created).Seconds()
		item.DurationMS = duration * 1000
		if requestAt != "" {
			request, err := time.Parse(time.RFC3339Nano, requestAt)
			if err != nil {
				return resp, err
			}
			ms := updated.Sub(request).Seconds() * 1000
			item.RequestDurationMS = &ms
			if firstVisibleAt != "" {
				visible, err := time.Parse(time.RFC3339Nano, firstVisibleAt)
				if err != nil {
					return resp, err
				}
				latency := visible.Sub(request).Seconds() * 1000
				item.FirstVisibleLatencyMS = &latency
			}
		}
		if duration > 0 {
			item.CharactersPerSecond = float64(item.OutputCharacters) / duration
			if item.OutputTokensKnown {
				tps := float64(item.OutputTokens) / duration
				item.TokensPerSecond = &tps
			}
		}
		if !completed {
			if created.After(now.Add(-store.ActiveSpeedTTL)) && !updated.Before(now.Add(-10*time.Minute)) && !updated.After(now) {
				resp.Active = append(resp.Active, item)
			}
			continue
		}
		if !inRange {
			continue
		}
		modelSamples := byModel[item.Model]
		if modelSamples == nil {
			modelSamples = &samples{}
			byModel[item.Model] = modelSamples
		}
		if item.FirstVisibleLatencyMS != nil {
			latencySamples = append(latencySamples, *item.FirstVisibleLatencyMS)
			modelSamples.latency = append(modelSamples.latency, *item.FirstVisibleLatencyMS)
			visibleSum += *item.FirstVisibleLatencyMS
			visibleCount++
		}
		var tools []event.Tool
		if toolsJSON != "" {
			if err := json.Unmarshal([]byte(toolsJSON), &tools); err != nil {
				return resp, err
			}
		}
		for _, tool := range tools {
			key := item.Model + "\x00" + tool.Name + "\x00" + tool.Type
			g := toolGroups[key]
			if g == nil {
				g = &ToolAggregate{Model: item.Model, Name: tool.Name, Type: tool.Type}
				toolGroups[key] = g
			}
			g.Calls++
			g.InputCharacters += tool.InputCharacters
			if tool.FinishedAt != "" {
				start, err := time.Parse(time.RFC3339Nano, tool.StartedAt)
				if err != nil {
					return resp, err
				}
				end, err := time.Parse(time.RFC3339Nano, tool.FinishedAt)
				if err != nil {
					return resp, err
				}
				g.CompletedCalls++
				toolSums[key] += end.Sub(start).Seconds() * 1000
			}
		}
		if item.OutputTokensKnown {
			resp.Recent = append(resp.Recent, item)
			sort.Slice(resp.Recent, func(i, j int) bool {
				a, _ := time.Parse(time.RFC3339Nano, resp.Recent[i].UpdatedAt)
				b, _ := time.Parse(time.RFC3339Nano, resp.Recent[j].UpdatedAt)
				if a.Equal(b) {
					return resp.Recent[i].ResponseID < resp.Recent[j].ResponseID
				}
				return a.After(b)
			})
			if len(resp.Recent) > 20 {
				resp.Recent = resp.Recent[:20]
			}
		}
		if item.TokensPerSecond == nil || item.OutputTokens == 0 {
			continue
		}
		resp.CompletedRequests++
		speedSamples = append(speedSamples, *item.TokensPerSecond)
		modelSamples.speed = append(modelSamples.speed, *item.TokensPerSecond)
		local := created.In(window.location)
		bucket := local.Truncate(time.Minute).Format(time.RFC3339)
		key := bucket + "\x00" + item.Model
		group := groups[key]
		if group == nil {
			group = &aggregate{point: SpeedPoint{Time: bucket, Model: item.Model}}
			groups[key] = group
		}
		group.point.Requests++
		if item.FirstVisibleLatencyMS != nil {
			group.visibleSum += *item.FirstVisibleLatencyMS
			group.visibleCount++
		}
		group.point.AvgTokensPerSecond += *item.TokensPerSecond
		group.point.AvgDurationMS += item.DurationMS
		if item.RequestDurationMS != nil {
			group.requestSum += *item.RequestDurationMS
			group.requestCount++
		}
		sum += *item.TokensPerSecond
	}
	if err := rows.Err(); err != nil {
		return resp, err
	}
	for model := range available {
		resp.Models = append(resp.Models, model)
	}
	sort.Strings(resp.Models)
	known := 0
	for _, group := range groups {
		point := group.point
		known += point.Requests
		point.AvgTokensPerSecond /= float64(point.Requests)
		point.AvgDurationMS /= float64(point.Requests)
		if group.requestCount > 0 {
			mean := group.requestSum / float64(group.requestCount)
			point.AvgRequestDurationMS = &mean
		}
		if group.visibleCount > 0 {
			mean := group.visibleSum / float64(group.visibleCount)
			point.AvgFirstVisibleLatencyMS = &mean
		}
		resp.Points = append(resp.Points, point)
	}
	if known > 0 {
		mean := sum / float64(known)
		resp.AvgTokensPerSecond = &mean
	}
	if visibleCount > 0 {
		mean := visibleSum / float64(visibleCount)
		resp.AvgFirstVisibleLatencyMS = &mean
	}
	speedDist, latencyDist := speedDistribution(speedSamples), speedDistribution(latencySamples)
	resp.P50TokensPerSecond, resp.P95TokensPerSecond = speedDist.P50, speedDist.P95
	resp.P50FirstVisibleLatencyMS, resp.P95FirstVisibleLatencyMS = latencyDist.P50, latencyDist.P95
	for model, values := range byModel {
		if len(values.speed) == 0 && len(values.latency) == 0 {
			continue
		}
		resp.ModelStats = append(resp.ModelStats, ModelSpeedStats{Model: model, OutputSpeed: speedDistribution(values.speed), FirstVisibleLatency: speedDistribution(values.latency)})
	}
	sort.Slice(resp.ModelStats, func(i, j int) bool { return resp.ModelStats[i].Model < resp.ModelStats[j].Model })
	for key, g := range toolGroups {
		if g.CompletedCalls > 0 {
			mean := toolSums[key] / float64(g.CompletedCalls)
			g.AvgInputDurationMS = &mean
		}
		resp.Tools = append(resp.Tools, *g)
	}
	sort.Slice(resp.Tools, func(i, j int) bool {
		a, b := resp.Tools[i], resp.Tools[j]
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Type < b.Type
	})
	sort.Slice(resp.Points, func(i, j int) bool {
		if resp.Points[i].Time == resp.Points[j].Time {
			return resp.Points[i].Model < resp.Points[j].Model
		}
		a, _ := time.Parse(time.RFC3339, resp.Points[i].Time)
		b, _ := time.Parse(time.RFC3339, resp.Points[j].Time)
		return a.Before(b)
	})
	sort.Slice(resp.Active, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, resp.Active[i].UpdatedAt)
		b, _ := time.Parse(time.RFC3339Nano, resp.Active[j].UpdatedAt)
		if a.Equal(b) {
			return resp.Active[i].ResponseID < resp.Active[j].ResponseID
		}
		return a.After(b)
	})
	if len(resp.Active) > 100 {
		resp.Active = resp.Active[:100]
	}
	return resp, nil
}

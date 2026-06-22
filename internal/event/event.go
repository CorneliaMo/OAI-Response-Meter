package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const SchemaVersion = 1
const RateLimitsEventType = "codex_rate_limits"

type DatagramKind int

const (
	KindUsage DatagramKind = iota + 1
	KindRateLimits
)

type Datagram struct {
	Kind       DatagramKind
	Usage      Usage
	RateLimits RateLimits
}

type Usage struct {
	Schema              int    `json:"schema"`
	Timestamp           string `json:"ts"`
	Source              string `json:"source"`
	Transport           string `json:"transport"`
	Host                string `json:"host"`
	Path                string `json:"path"`
	ResponseID          string `json:"response_id"`
	PreviousResponseID  string `json:"previous_response_id"`
	ChainRootResponseID string `json:"chain_root_response_id"`
	PromptCacheKey      string `json:"prompt_cache_key"`
	Model               string `json:"model"`
	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	TotalTokens         int64  `json:"total_tokens"`
	CachedTokens        int64  `json:"cached_tokens"`
	ReasoningTokens     int64  `json:"reasoning_tokens"`
}

type RateLimits struct {
	Schema                     int    `json:"schema"`
	EventType                  string `json:"event_type"`
	Timestamp                  string `json:"ts"`
	Source                     string `json:"source"`
	Transport                  string `json:"transport"`
	Host                       string `json:"host"`
	Path                       string `json:"path"`
	PlanType                   string `json:"plan_type"`
	Allowed                    bool   `json:"allowed"`
	LimitReached               bool   `json:"limit_reached"`
	PrimaryUsedPercent         int64  `json:"primary_used_percent"`
	PrimaryWindowMinutes       int64  `json:"primary_window_minutes"`
	PrimaryResetAfterSeconds   int64  `json:"primary_reset_after_seconds"`
	PrimaryResetAt             int64  `json:"primary_reset_at"`
	SecondaryUsedPercent       int64  `json:"secondary_used_percent"`
	SecondaryWindowMinutes     int64  `json:"secondary_window_minutes"`
	SecondaryResetAfterSeconds int64  `json:"secondary_reset_after_seconds"`
	SecondaryResetAt           int64  `json:"secondary_reset_at"`
	RawJSON                    string `json:"raw_json"`
}

func Decode(data []byte) (Usage, error) {
	var usage Usage
	if err := json.Unmarshal(data, &usage); err != nil {
		return Usage{}, fmt.Errorf("decode usage event: %w", err)
	}
	if err := usage.Validate(); err != nil {
		return Usage{}, err
	}
	return usage, nil
}

func DecodeDatagram(data []byte) (Datagram, error) {
	var header struct {
		EventType string `json:"event_type"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return Datagram{}, fmt.Errorf("decode datagram: %w", err)
	}
	if header.EventType == RateLimitsEventType {
		var rateLimits RateLimits
		if err := json.Unmarshal(data, &rateLimits); err != nil {
			return Datagram{}, fmt.Errorf("decode rate limits event: %w", err)
		}
		if err := rateLimits.Validate(); err != nil {
			return Datagram{}, err
		}
		return Datagram{Kind: KindRateLimits, RateLimits: rateLimits}, nil
	}
	usage, err := Decode(data)
	if err != nil {
		return Datagram{}, err
	}
	return Datagram{Kind: KindUsage, Usage: usage}, nil
}

func (u Usage) Validate() error {
	if u.Schema != SchemaVersion {
		return fmt.Errorf("unsupported schema %d", u.Schema)
	}
	if strings.TrimSpace(u.Timestamp) == "" {
		return errors.New("missing ts")
	}
	if _, err := time.Parse(time.RFC3339Nano, u.Timestamp); err != nil {
		return fmt.Errorf("invalid ts: %w", err)
	}
	if strings.TrimSpace(u.ResponseID) == "" {
		return errors.New("missing response_id")
	}
	if strings.TrimSpace(u.Transport) == "" {
		return errors.New("missing transport")
	}
	if strings.TrimSpace(u.Host) == "" {
		return errors.New("missing host")
	}
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.TotalTokens < 0 || u.CachedTokens < 0 || u.ReasoningTokens < 0 {
		return errors.New("token counts must be non-negative")
	}
	return nil
}

func (r RateLimits) Validate() error {
	if r.Schema != SchemaVersion {
		return fmt.Errorf("unsupported schema %d", r.Schema)
	}
	if r.EventType != RateLimitsEventType {
		return fmt.Errorf("unsupported event_type %q", r.EventType)
	}
	if strings.TrimSpace(r.Timestamp) == "" {
		return errors.New("missing ts")
	}
	if _, err := time.Parse(time.RFC3339Nano, r.Timestamp); err != nil {
		return fmt.Errorf("invalid ts: %w", err)
	}
	if strings.TrimSpace(r.Transport) == "" {
		return errors.New("missing transport")
	}
	if strings.TrimSpace(r.Host) == "" {
		return errors.New("missing host")
	}
	if strings.TrimSpace(r.RawJSON) == "" {
		return errors.New("missing raw_json")
	}
	if r.PrimaryResetAt <= 0 && r.SecondaryResetAt <= 0 {
		return errors.New("rate limits event missing reset_at values")
	}
	return nil
}

func (u Usage) MarshalJSONLine() ([]byte, error) {
	data, err := json.Marshal(u)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (r RateLimits) MarshalJSONLine() ([]byte, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

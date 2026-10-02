package event

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const SpeedEventType = "response_speed"

// Speed contains response timing and counts, never response content.
type Speed struct {
	Schema            int    `json:"schema"`
	EventType         string `json:"event_type"`
	Timestamp         string `json:"ts"`
	Source            string `json:"source"`
	Transport         string `json:"transport"`
	Host              string `json:"host"`
	Path              string `json:"path"`
	ResponseID        string `json:"response_id"`
	Model             string `json:"model"`
	RequestAt         string `json:"request_at,omitempty"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
	Completed         bool   `json:"completed"`
	OutputTokens      int64  `json:"output_tokens"`
	OutputTokensKnown bool   `json:"output_tokens_known"`
	OutputCharacters  int64  `json:"output_characters"`
	OutputItems       int64  `json:"output_items"`
}

func (s Speed) Validate() error {
	if s.Schema != SchemaVersion {
		return fmt.Errorf("unsupported schema %d", s.Schema)
	}
	if s.EventType != SpeedEventType {
		return fmt.Errorf("unsupported event_type %q", s.EventType)
	}
	for name, value := range map[string]string{"response_id": s.ResponseID, "transport": s.Transport, "host": s.Host} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("missing %s", name)
		}
	}
	for name, value := range map[string]string{"ts": s.Timestamp, "created_at": s.CreatedAt, "updated_at": s.UpdatedAt, "request_at": s.RequestAt} {
		if name == "request_at" && value == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			return fmt.Errorf("invalid %s: %w", name, err)
		}
	}
	created, _ := time.Parse(time.RFC3339Nano, s.CreatedAt)
	updated, _ := time.Parse(time.RFC3339Nano, s.UpdatedAt)
	if updated.Before(created) {
		return fmt.Errorf("updated_at precedes created_at")
	}
	if s.RequestAt != "" {
		request, _ := time.Parse(time.RFC3339Nano, s.RequestAt)
		if request.After(created) {
			return fmt.Errorf("request_at follows created_at")
		}
	}
	if s.OutputTokens < 0 || s.OutputCharacters < 0 || s.OutputItems < 0 {
		return fmt.Errorf("output counts must be non-negative")
	}
	return nil
}

func (s Speed) MarshalJSONLine() ([]byte, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

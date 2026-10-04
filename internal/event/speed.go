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
	FirstVisibleAt    string `json:"first_visible_at,omitempty"`
	Tools             []Tool `json:"tools,omitempty"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
	Completed         bool   `json:"completed"`
	OutputTokens      int64  `json:"output_tokens"`
	OutputTokensKnown bool   `json:"output_tokens_known"`
	OutputCharacters  int64  `json:"output_characters"`
	OutputItems       int64  `json:"output_items"`
}

type Tool struct {
	ItemID          string `json:"item_id"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	StartedAt       string `json:"started_at"`
	FinishedAt      string `json:"finished_at,omitempty"`
	InputCharacters int64  `json:"input_characters"`
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
	for name, value := range map[string]string{"ts": s.Timestamp, "created_at": s.CreatedAt, "updated_at": s.UpdatedAt, "request_at": s.RequestAt, "first_visible_at": s.FirstVisibleAt} {
		if (name == "request_at" || name == "first_visible_at") && value == "" {
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
	if s.FirstVisibleAt != "" {
		visible, _ := time.Parse(time.RFC3339Nano, s.FirstVisibleAt)
		if visible.Before(created) || visible.After(updated) {
			return fmt.Errorf("first_visible_at outside response interval")
		}
	}
	seen := map[string]bool{}
	if len(s.Tools) > 128 {
		return fmt.Errorf("too many tools")
	}
	for _, tool := range s.Tools {
		if tool.ItemID == "" || (tool.Type != "function_call" && tool.Type != "custom_tool_call") || len(tool.ItemID) > 256 || len(tool.Name) > 256 || seen[tool.ItemID] || tool.InputCharacters < 0 {
			return fmt.Errorf("invalid tool metadata")
		}
		seen[tool.ItemID] = true
		start, err := time.Parse(time.RFC3339Nano, tool.StartedAt)
		if err != nil || start.Before(created) || start.After(updated) {
			return fmt.Errorf("invalid tool started_at")
		}
		if tool.FinishedAt != "" {
			end, err := time.Parse(time.RFC3339Nano, tool.FinishedAt)
			if err != nil || end.Before(start) || end.After(updated) {
				return fmt.Errorf("invalid tool finished_at")
			}
		}
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

package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const SchemaVersion = 1

type Usage struct {
	Schema          int    `json:"schema"`
	Timestamp       string `json:"ts"`
	Source          string `json:"source"`
	Transport       string `json:"transport"`
	Host            string `json:"host"`
	Path            string `json:"path"`
	ResponseID      string `json:"response_id"`
	Model           string `json:"model"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	TotalTokens     int64  `json:"total_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
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

func (u Usage) MarshalJSONLine() ([]byte, error) {
	data, err := json.Marshal(u)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

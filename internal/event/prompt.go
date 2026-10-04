package event

import (
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const PromptEventType = "prompt_version"
const MaxPromptBytes = 1 << 20

type PromptVersion struct {
	Schema      int    `json:"schema"`
	EventType   string `json:"event_type"`
	Timestamp   string `json:"ts"`
	Model       string `json:"model"`
	SourceLabel string `json:"source_label"`
	Hash        string `json:"hash"`
	ChunkIndex  int    `json:"chunk_index"`
	ChunkCount  int    `json:"chunk_count"`
	Text        string `json:"text"`
}

func (p PromptVersion) Validate() error {
	if p.Schema != SchemaVersion || p.EventType != PromptEventType {
		return errors.New("invalid prompt schema or event type")
	}
	if _, err := time.Parse(time.RFC3339Nano, p.Timestamp); err != nil {
		return errors.New("invalid prompt timestamp")
	}
	if strings.TrimSpace(p.Model) == "" || len(p.Model) > 256 {
		return errors.New("invalid prompt model")
	}
	if p.SourceLabel != "instructions" && p.SourceLabel != "system" && p.SourceLabel != "developer" {
		return errors.New("invalid prompt source label")
	}
	if len(p.Hash) != 64 || p.Hash != strings.ToLower(p.Hash) {
		return errors.New("invalid prompt hash")
	}
	if _, err := hex.DecodeString(p.Hash); err != nil {
		return errors.New("invalid prompt hash")
	}
	if p.ChunkCount < 1 || p.ChunkCount > 256 || p.ChunkIndex < 0 || p.ChunkIndex >= p.ChunkCount {
		return errors.New("invalid prompt chunk bounds")
	}
	if !utf8.ValidString(p.Text) || utf8.RuneCountInString(p.Text) > 6000 {
		return errors.New("invalid prompt chunk text bounds")
	}
	return nil
}

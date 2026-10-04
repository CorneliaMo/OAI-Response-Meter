package event

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPromptValidationAndDecode(t *testing.T) {
	p := PromptVersion{Schema: 1, EventType: PromptEventType, Timestamp: "2026-10-03T00:00:00.123456789Z", Model: "gpt-5", SourceLabel: "instructions", Hash: fmt.Sprintf("%x", sha256.Sum256([]byte("正文"))), ChunkCount: 1, Text: "正文"}
	data, _ := json.Marshal(p)
	got, err := DecodeDatagram(data)
	if err != nil || got.Kind != KindPrompt || got.Prompt != p {
		t.Fatalf("decode kind=%v err=%v", got.Kind, err)
	}
	for name, mutate := range map[string]func(*PromptVersion){
		"schema": func(p *PromptVersion) { p.Schema = 2 }, "ts": func(p *PromptVersion) { p.Timestamp = "bad" }, "model": func(p *PromptVersion) { p.Model = "" },
		"source": func(p *PromptVersion) { p.SourceLabel = "user" }, "hash": func(p *PromptVersion) { p.Hash = strings.Repeat("G", 64) },
		"uppercase": func(p *PromptVersion) { p.Hash = strings.ToUpper(p.Hash) }, "zero": func(p *PromptVersion) { p.ChunkCount = 0 }, "count": func(p *PromptVersion) { p.ChunkCount = 257 },
		"index": func(p *PromptVersion) { p.ChunkIndex = 1 }, "negative": func(p *PromptVersion) { p.ChunkIndex = -1 }, "text": func(p *PromptVersion) { p.Text = strings.Repeat("界", 6001) }, "utf8": func(p *PromptVersion) { p.Text = "\xff" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := p
			mutate(&bad)
			if bad.Validate() == nil {
				t.Fatal("accepted invalid prompt")
			}
		})
	}
	p.Text = strings.Repeat("界", 6000)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSpeedVisibleAndToolsValidation(t *testing.T) {
	s := testSpeed()
	s.RequestAt = "2026-10-03T00:59:59Z"
	s.FirstVisibleAt = "2026-10-03T01:00:00.123456789Z"
	s.Tools = []Tool{{ItemID: "call1", Name: "shell", Type: "function_call", StartedAt: s.CreatedAt, FinishedAt: s.UpdatedAt, InputCharacters: 40}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Speed){
		"early tool": func(s *Speed) { s.Tools[0].StartedAt = s.RequestAt },
		"late tool":  func(s *Speed) { s.Tools[0].FinishedAt = "2026-10-03T01:00:02Z" },
		"tool type":  func(s *Speed) { s.Tools[0].Type = "execution" },
		"visible":    func(s *Speed) { s.FirstVisibleAt = "bad" }, "early": func(s *Speed) { s.FirstVisibleAt = s.RequestAt }, "late": func(s *Speed) { s.FirstVisibleAt = "2026-10-03T01:00:02Z" },
		"negative": func(s *Speed) { s.Tools[0].InputCharacters = -1 }, "start": func(s *Speed) { s.Tools[0].StartedAt = "bad" }, "end": func(s *Speed) { s.Tools[0].FinishedAt = s.RequestAt }, "name": func(s *Speed) { s.Tools[0].Name = strings.Repeat("x", 513) }, "duplicate": func(s *Speed) { s.Tools = append(s.Tools, s.Tools[0]) }, "count": func(s *Speed) { s.Tools = make([]Tool, 129) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := s
			bad.Tools = append([]Tool(nil), s.Tools...)
			mutate(&bad)
			if bad.Validate() == nil {
				t.Fatal("accepted invalid speed")
			}
		})
	}
}

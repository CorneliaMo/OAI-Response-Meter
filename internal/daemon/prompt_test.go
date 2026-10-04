package daemon

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/cornelia/oai-response-meter/internal/event"
	"github.com/cornelia/oai-response-meter/internal/store"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDaemonPromptChunksPersistAndLogMetadataOnly(t *testing.T) {
	for _, batchSize := range []int{1, 100} {
		t.Run(fmt.Sprint(batchSize), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			dir := t.TempDir()
			dbPath := filepath.Join(dir, "db")
			jsonlPath := filepath.Join(dir, "jsonl")
			sink, err := store.Open(ctx, dbPath, jsonlPath)
			if err != nil {
				t.Fatal(err)
			}
			defer sink.Close()
			var mu sync.Mutex
			var logs []string
			socket := filepath.Join(dir, "meter.sock")
			d, err := New(Config{SocketPath: socket, BatchSize: batchSize, FlushInterval: 20 * time.Millisecond, Verbose: true, Logf: func(format string, args ...any) {
				mu.Lock()
				defer mu.Unlock()
				logs = append(logs, fmt.Sprintf(format, args...))
			}}, sink)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- d.Run(ctx) }()
			waitForSocket(t, socket)
			text := "private正文\nsecret"
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
			p := event.PromptVersion{Schema: 1, EventType: event.PromptEventType, Timestamp: "2026-10-03T00:00:00Z", Model: "gpt-5", SourceLabel: "instructions", Hash: hash, ChunkCount: 2, ChunkIndex: 1, Text: "\nsecret"}
			data, _ := json.Marshal(p)
			sendDatagram(t, socket, data)
			sendDatagram(t, socket, data)
			p.ChunkIndex = 0
			p.Text = "private正文"
			data, _ = json.Marshal(p)
			sendDatagram(t, socket, data)
			waitFor(t, func() bool { return d.Counters().PromptWritten == 1 })
			p.SourceLabel = "private正文"
			data, _ = json.Marshal(p)
			sendDatagram(t, socket, data)
			waitFor(t, func() bool { return d.Counters().Invalid == 1 })
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var full string
			var observations int
			if err = db.QueryRow(`select text,observations from prompt_versions where model=? and hash=?`, "gpt-5", hash).Scan(&full, &observations); err != nil || full != text || observations != 1 {
				t.Fatalf("persist observations=%d err=%v", observations, err)
			}
			jsonl, err := os.ReadFile(jsonlPath)
			if err != nil || len(jsonl) != 0 {
				t.Fatalf("JSONL bytes=%d err=%v", len(jsonl), err)
			}
			mu.Lock()
			defer mu.Unlock()
			joined := strings.Join(logs, "\n")
			if strings.Contains(joined, "private正文") || strings.Contains(joined, "secret") {
				t.Fatal("prompt text leaked into log")
			}
			if !strings.Contains(joined, "write prompt batch=") || !strings.Contains(joined, "received prompt_version") {
				t.Fatal("missing metadata logs")
			}
		})
	}
}

func TestPromptLogDoesNotIncludeText(t *testing.T) {
	var logs string
	d, _ := New(Config{SocketPath: "unused", Verbose: true, Logf: func(format string, args ...any) { logs += fmt.Sprintf(format, args...) }}, &memoryStore{})
	d.logDatagram(event.Datagram{Kind: event.KindPrompt, Prompt: event.PromptVersion{Text: "private正文", Model: "private正文", SourceLabel: "private正文", Hash: strings.Repeat("a", 64), ChunkCount: 1}})
	if strings.Contains(logs, "private正文") {
		t.Fatal("raw text logged")
	}
}

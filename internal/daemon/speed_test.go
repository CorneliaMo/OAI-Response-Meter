package daemon

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cornelia/oai-response-meter/internal/store"
)

func TestDaemonSpeedDatagramsBatchAndTimer(t *testing.T) {
	for _, batchSize := range []int{1, 100} {
		t.Run(fmt.Sprintf("batch-%d", batchSize), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "events.db")
			sink, err := store.Open(context.Background(), path, filepath.Join(dir, "events.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			defer sink.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mu sync.Mutex
			var logs []string
			d, err := New(Config{SocketPath: filepath.Join(dir, "speed.sock"), BatchSize: batchSize, FlushInterval: 20 * time.Millisecond, Verbose: true, Logf: func(format string, args ...any) {
				mu.Lock()
				defer mu.Unlock()
				logs = append(logs, fmt.Sprintf(format, args...))
			}}, sink)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- d.Run(ctx) }()
			waitForSocket(t, d.config.SocketPath)
			sendDatagram(t, d.config.SocketPath, []byte(`{"schema":1,"event_type":"response_speed","ts":"2026-10-03T01:00:02Z","source":"mitmproxy","transport":"websocket","host":"chatgpt.com","path":"/responses","response_id":"speed-1","model":"gpt-5","created_at":"2026-10-03T01:00:00Z","updated_at":"2026-10-03T01:00:02Z","completed":true,"output_tokens":20,"output_tokens_known":true,"output_characters":50,"output_items":1,"content":"must not persist"}`))
			waitFor(t, func() bool { return d.Counters().SpeedWritten == 1 })
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			counters := d.Counters()
			if counters.Received != 1 || counters.Written != 0 || counters.SpeedWritten != 1 || counters.Invalid != 0 {
				t.Fatalf("counters=%+v", counters)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var tokens int
			if err := db.QueryRow(`select output_tokens from response_speed_events where response_id='speed-1'`).Scan(&tokens); err != nil || tokens != 20 {
				t.Fatalf("stored=%d err=%v", tokens, err)
			}
			mu.Lock()
			joined := strings.Join(logs, "\n")
			mu.Unlock()
			if !strings.Contains(joined, "received response_speed response_id=speed-1") || !strings.Contains(joined, "write speed batch=1 inserted=1 duplicates=0") || strings.Contains(joined, "must not persist") {
				t.Fatalf("logs=%s", joined)
			}
		})
	}
}

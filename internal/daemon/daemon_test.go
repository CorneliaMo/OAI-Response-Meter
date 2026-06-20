package daemon

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cornelia/oai-response-meter/internal/event"
	"github.com/cornelia/oai-response-meter/internal/store"
)

func TestDaemonReceivesDatagramsAndWritesStore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sink := &memoryStore{}
	socketPath := filepath.Join(t.TempDir(), "meter.sock")
	daemon, err := New(Config{
		SocketPath:    socketPath,
		BatchSize:     2,
		FlushInterval: 50 * time.Millisecond,
	}, sink)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx) }()
	waitForSocket(t, socketPath)

	sendDatagram(t, socketPath, sampleDatagram("resp_1"))
	sendDatagram(t, socketPath, sampleDatagram("resp_2"))
	waitFor(t, func() bool { return sink.count() == 2 })

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	counters := daemon.Counters()
	if counters.Received != 2 || counters.Written != 2 {
		t.Fatalf("Counters() = %+v", counters)
	}
}

func TestDaemonCountsInvalidDatagrams(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sink := &memoryStore{}
	socketPath := filepath.Join(t.TempDir(), "meter.sock")
	daemon, err := New(Config{SocketPath: socketPath, FlushInterval: 20 * time.Millisecond}, sink)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx) }()
	waitForSocket(t, socketPath)

	sendDatagram(t, socketPath, []byte(`{`))
	waitFor(t, func() bool { return daemon.Counters().Invalid == 1 })

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestDaemonFlushesOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sink := &memoryStore{}
	socketPath := filepath.Join(t.TempDir(), "meter.sock")
	daemon, err := New(Config{
		SocketPath:    socketPath,
		BatchSize:     100,
		FlushInterval: time.Hour,
	}, sink)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx) }()
	waitForSocket(t, socketPath)

	sendDatagram(t, socketPath, sampleDatagram("resp_1"))
	waitFor(t, func() bool { return daemon.Counters().Received == 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if sink.count() != 1 {
		t.Fatalf("sink count = %d, want 1", sink.count())
	}
}

type memoryStore struct {
	mu     sync.Mutex
	events []event.Usage
}

func (s *memoryStore) WriteBatch(_ context.Context, events []event.Usage) (store.WriteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
	return store.WriteResult{Inserted: len(events)}, nil
}

func (s *memoryStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func sampleDatagram(responseID string) []byte {
	return []byte(`{
		"schema": 1,
		"ts": "2026-06-20T12:00:00Z",
		"source": "mitmproxy",
		"transport": "websocket",
		"host": "chatgpt.com",
		"path": "/backend-api/codex",
		"response_id": "` + responseID + `",
		"model": "gpt-test",
		"input_tokens": 10,
		"output_tokens": 20,
		"total_tokens": 30
	}`)
}

func sendDatagram(t *testing.T, socketPath string, data []byte) {
	t.Helper()
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socketPath, Net: "unixgram"})
	if err != nil {
		t.Fatalf("DialUnix() error = %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
}

func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	waitFor(t, func() bool {
		conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socketPath, Net: "unixgram"})
		if err != nil {
			return false
		}
		conn.Close()
		return true
	})
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

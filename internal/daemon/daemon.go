package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/cornelia/oai-response-meter/internal/event"
	"github.com/cornelia/oai-response-meter/internal/store"
)

const maxDatagramSize = 64 * 1024

type Config struct {
	SocketPath    string
	BatchSize     int
	FlushInterval time.Duration
	Verbose       bool
	Logf          func(string, ...any)
}

type Counters struct {
	Received   uint64
	Written    uint64
	Duplicates uint64
	Invalid    uint64
	WriteError uint64
}

type Daemon struct {
	config Config
	store  EventStore

	mu       sync.Mutex
	counters Counters
}

type EventStore interface {
	WriteBatch(context.Context, []event.Usage) (store.WriteResult, error)
}

func New(config Config, sink EventStore) (*Daemon, error) {
	if config.SocketPath == "" {
		return nil, errors.New("socket path is required")
	}
	if config.BatchSize <= 0 {
		config.BatchSize = 100
	}
	if config.FlushInterval <= 0 {
		config.FlushInterval = 500 * time.Millisecond
	}
	if sink == nil {
		return nil, errors.New("store is required")
	}
	if config.Verbose && config.Logf == nil {
		config.Logf = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "[daemon] "+format+"\n", args...)
		}
	}
	return &Daemon{config: config, store: sink}, nil
}

func (d *Daemon) Run(ctx context.Context) error {
	if err := os.Remove(d.config.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	addr := net.UnixAddr{Name: d.config.SocketPath, Net: "unixgram"}
	conn, err := net.ListenUnixgram("unixgram", &addr)
	if err != nil {
		return fmt.Errorf("listen unixgram: %w", err)
	}
	d.logf("listening socket=%s batch_size=%d flush_interval=%s", d.config.SocketPath, d.config.BatchSize, d.config.FlushInterval)
	defer func() {
		conn.Close()
		os.Remove(d.config.SocketPath)
		d.logf("stopped socket=%s counters=%+v", d.config.SocketPath, d.Counters())
	}()

	events := make(chan event.Usage, d.config.BatchSize*2)
	errc := make(chan error, 1)
	go d.readLoop(ctx, conn, events, errc)

	ticker := time.NewTicker(d.config.FlushInterval)
	defer ticker.Stop()
	batch := make([]event.Usage, 0, d.config.BatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		pending := batch
		batch = make([]event.Usage, 0, d.config.BatchSize)
		d.write(ctx, pending)
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return nil
		case err := <-errc:
			flush()
			return err
		case usage := <-events:
			batch = append(batch, usage)
			if len(batch) >= d.config.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (d *Daemon) Counters() Counters {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.counters
}

func (d *Daemon) readLoop(ctx context.Context, conn *net.UnixConn, events chan<- event.Usage, errc chan<- error) {
	buffer := make([]byte, maxDatagramSize)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, _, err := conn.ReadFromUnix(buffer)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				select {
				case <-ctx.Done():
					return
				default:
					continue
				}
			}
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			errc <- fmt.Errorf("read unixgram: %w", err)
			return
		}
		d.add(func(c *Counters) { c.Received++ })
		usage, err := event.Decode(buffer[:n])
		if err != nil {
			d.add(func(c *Counters) { c.Invalid++ })
			d.logf("invalid datagram bytes=%d error=%v", n, err)
			continue
		}
		d.logf("received response_id=%s previous_response_id=%s transport=%s host=%s path=%s model=%s input=%d output=%d total=%d cached=%d reasoning=%d",
			usage.ResponseID,
			usage.PreviousResponseID,
			usage.Transport,
			usage.Host,
			usage.Path,
			usage.Model,
			usage.InputTokens,
			usage.OutputTokens,
			usage.TotalTokens,
			usage.CachedTokens,
			usage.ReasoningTokens,
		)
		select {
		case events <- usage:
		case <-ctx.Done():
			return
		}
	}
}

func (d *Daemon) write(ctx context.Context, batch []event.Usage) {
	result, err := d.store.WriteBatch(ctx, batch)
	if err != nil {
		d.add(func(c *Counters) { c.WriteError++ })
		d.logf("write failed batch=%d error=%v", len(batch), err)
		return
	}
	d.add(func(c *Counters) {
		c.Written += uint64(result.Inserted)
		c.Duplicates += uint64(result.Duplicates)
	})
	d.logf("write batch=%d inserted=%d duplicates=%d", len(batch), result.Inserted, result.Duplicates)
}

func (d *Daemon) add(update func(*Counters)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	update(&d.counters)
}

func (d *Daemon) logf(format string, args ...any) {
	if d.config.Verbose && d.config.Logf != nil {
		d.config.Logf(format, args...)
	}
}

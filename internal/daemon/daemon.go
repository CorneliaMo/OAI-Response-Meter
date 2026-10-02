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
	Received         uint64
	Written          uint64
	Duplicates       uint64
	RateLimitWritten uint64
	SpeedWritten     uint64
	Invalid          uint64
	WriteError       uint64
}

type Daemon struct {
	config Config
	store  EventStore

	mu       sync.Mutex
	counters Counters
}

type EventStore interface {
	WriteBatch(context.Context, []event.Usage) (store.WriteResult, error)
	WriteRateLimitBatch(context.Context, []event.RateLimits) (store.WriteResult, error)
	WriteSpeedBatch(context.Context, []event.Speed) (store.WriteResult, error)
	PruneActiveSpeeds(context.Context, time.Time) error
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

	events := make(chan event.Datagram, d.config.BatchSize*2)
	errc := make(chan error, 1)
	go d.readLoop(ctx, conn, events, errc)

	ticker := time.NewTicker(d.config.FlushInterval)
	defer ticker.Stop()
	cleanup := func() {
		if err := d.store.PruneActiveSpeeds(ctx, time.Now()); err != nil {
			d.logf("prune active response speeds failed error=%v", err)
		}
	}
	cleanup()
	cleanupTicker := time.NewTicker(time.Minute)
	defer cleanupTicker.Stop()
	usageBatch := make([]event.Usage, 0, d.config.BatchSize)
	rateLimitBatch := make([]event.RateLimits, 0, d.config.BatchSize)
	speedBatch := make([]event.Speed, 0, d.config.BatchSize)

	flush := func() {
		if len(speedBatch) > 0 {
			writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			result, err := d.store.WriteSpeedBatch(writeCtx, speedBatch)
			cancel()
			if err != nil {
				d.add(func(c *Counters) { c.WriteError++ })
				d.logf("write speed failed batch=%d error=%v", len(speedBatch), err)
			} else {
				d.add(func(c *Counters) {
					c.SpeedWritten += uint64(result.Inserted)
					c.Duplicates += uint64(result.Duplicates)
				})
				d.logf("write speed batch=%d inserted=%d duplicates=%d", len(speedBatch), result.Inserted, result.Duplicates)
			}
			speedBatch = make([]event.Speed, 0, d.config.BatchSize)
		}
		if len(usageBatch) > 0 {
			pending := usageBatch
			usageBatch = make([]event.Usage, 0, d.config.BatchSize)
			d.writeUsage(ctx, pending)
		}
		if len(rateLimitBatch) > 0 {
			pending := rateLimitBatch
			rateLimitBatch = make([]event.RateLimits, 0, d.config.BatchSize)
			d.writeRateLimits(ctx, pending)
		}
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return nil
		case err := <-errc:
			flush()
			return err
		case item := <-events:
			switch item.Kind {
			case event.KindSpeed:
				speedBatch = append(speedBatch, item.Speed)
			case event.KindUsage:
				usageBatch = append(usageBatch, item.Usage)
			case event.KindRateLimits:
				rateLimitBatch = append(rateLimitBatch, item.RateLimits)
			}
			if len(usageBatch)+len(rateLimitBatch)+len(speedBatch) >= d.config.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-cleanupTicker.C:
			cleanup()
		}
	}
}

func (d *Daemon) Counters() Counters {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.counters
}

func (d *Daemon) readLoop(ctx context.Context, conn *net.UnixConn, events chan<- event.Datagram, errc chan<- error) {
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
		item, err := event.DecodeDatagram(buffer[:n])
		if err != nil {
			d.add(func(c *Counters) { c.Invalid++ })
			d.logf("invalid datagram bytes=%d error=%v", n, err)
			continue
		}
		d.logDatagram(item)
		select {
		case events <- item:
		case <-ctx.Done():
			return
		}
	}
}

func (d *Daemon) logDatagram(item event.Datagram) {
	switch item.Kind {
	case event.KindSpeed:
		speed := item.Speed
		d.logf("received response_speed response_id=%s model=%s transport=%s host=%s path=%s created_at=%s updated_at=%s completed=%t output_tokens=%d output_tokens_known=%t output_characters=%d output_items=%d", speed.ResponseID, speed.Model, speed.Transport, speed.Host, speed.Path, speed.CreatedAt, speed.UpdatedAt, speed.Completed, speed.OutputTokens, speed.OutputTokensKnown, speed.OutputCharacters, speed.OutputItems)
	case event.KindUsage:
		usage := item.Usage
		if usage.PromptCacheKey == "" {
			d.logf("warning response_id=%s missing optional prompt_cache_key", usage.ResponseID)
		}
		d.logf("received response_id=%s previous_response_id=%s prompt_cache_key=%s transport=%s host=%s path=%s model=%s input=%d output=%d total=%d cached=%d cache_write=%d reasoning=%d",
			usage.ResponseID,
			usage.PreviousResponseID,
			usage.PromptCacheKey,
			usage.Transport,
			usage.Host,
			usage.Path,
			usage.Model,
			usage.InputTokens,
			usage.OutputTokens,
			usage.TotalTokens,
			usage.CachedTokens,
			usage.CacheWriteTokens,
			usage.ReasoningTokens,
		)
	case event.KindRateLimits:
		limits := item.RateLimits
		d.logf("received codex_rate_limits plan=%s allowed=%t limit_reached=%t five_hour_reset_at=%d weekly_reset_at=%d five_hour_used=%d weekly_used=%d",
			limits.PlanType,
			limits.Allowed,
			limits.LimitReached,
			limits.FiveHourResetAt,
			limits.WeeklyResetAt,
			limits.FiveHourUsedPercent,
			limits.WeeklyUsedPercent,
		)
	}
}

func (d *Daemon) writeUsage(ctx context.Context, batch []event.Usage) {
	result, err := d.store.WriteBatch(ctx, batch)
	if err != nil {
		d.add(func(c *Counters) { c.WriteError++ })
		d.logf("write usage failed batch=%d error=%v", len(batch), err)
		return
	}
	d.add(func(c *Counters) {
		c.Written += uint64(result.Inserted)
		c.Duplicates += uint64(result.Duplicates)
	})
	d.logf("write usage batch=%d inserted=%d duplicates=%d", len(batch), result.Inserted, result.Duplicates)
}

func (d *Daemon) writeRateLimits(ctx context.Context, batch []event.RateLimits) {
	result, err := d.store.WriteRateLimitBatch(ctx, batch)
	if err != nil {
		d.add(func(c *Counters) { c.WriteError++ })
		d.logf("write rate_limits failed batch=%d error=%v", len(batch), err)
		return
	}
	d.add(func(c *Counters) {
		c.RateLimitWritten += uint64(result.Inserted)
	})
	d.logf("write rate_limits batch=%d inserted=%d", len(batch), result.Inserted)
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

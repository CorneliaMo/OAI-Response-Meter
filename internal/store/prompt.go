package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cornelia/oai-response-meter/internal/event"
	"github.com/cornelia/oai-response-meter/internal/pricing"
)

func InitPromptSchema(ctx context.Context, db *sql.DB) error {
	for _, q := range []string{
		`create table if not exists prompt_versions (model text not null, hash text not null, text text not null, source_label text not null, first_seen text not null, last_seen text not null, observations integer not null, characters integer not null, primary key(model,hash))`,
		`create table if not exists prompt_observations (model text not null, hash text not null, ts text not null, primary key(model,hash,ts))`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

type promptAssembly struct {
	received     time.Time
	source       string
	chunks       map[int]string
	count, bytes int
}

func (s *Store) prunePromptAssemblies(now time.Time) {
	for key, a := range s.assemblies {
		if now.Sub(a.received) >= 10*time.Minute {
			delete(s.assemblies, key)
		}
	}
}

// Incomplete prompt text is bounded, transient, and never written to JSONL.
func (s *Store) WritePromptBatch(ctx context.Context, batch []event.PromptVersion) (WriteResult, error) {
	s.promptMu.Lock()
	defer s.promptMu.Unlock()
	result := WriteResult{}
	now := time.Now()
	if s.assemblies == nil {
		s.assemblies = map[string]*promptAssembly{}
	}
	s.prunePromptAssemblies(now)
	for _, p := range batch {
		if err := p.Validate(); err != nil {
			return result, err
		}
		p.Model = pricing.CanonicalModelName(p.Model)
		parsed, _ := time.Parse(time.RFC3339Nano, p.Timestamp)
		p.Timestamp = parsed.UTC().Format(time.RFC3339Nano)
		key := p.Model + "\x00" + p.Hash + "\x00" + p.Timestamp
		a := s.assemblies[key]
		if a == nil {
			if len(s.assemblies) >= 128 {
				var oldest string
				var at time.Time
				for k, v := range s.assemblies {
					if oldest == "" || v.received.Before(at) {
						oldest = k
						at = v.received
					}
				}
				delete(s.assemblies, oldest)
			}
			a = &promptAssembly{received: now, source: p.SourceLabel, count: p.ChunkCount, chunks: map[int]string{}}
			s.assemblies[key] = a
		}
		if a.count != p.ChunkCount || a.source != p.SourceLabel {
			delete(s.assemblies, key)
			return result, fmt.Errorf("conflicting prompt chunk metadata")
		}
		if old, ok := a.chunks[p.ChunkIndex]; ok {
			if old != p.Text {
				delete(s.assemblies, key)
				return result, fmt.Errorf("conflicting prompt chunk")
			}
			continue
		}
		a.chunks[p.ChunkIndex] = p.Text
		a.bytes += len(p.Text)
		if a.bytes > event.MaxPromptBytes {
			delete(s.assemblies, key)
			return result, fmt.Errorf("prompt exceeds byte limit")
		}
		if len(a.chunks) != a.count {
			continue
		}
		var text strings.Builder
		for i := 0; i < a.count; i++ {
			text.WriteString(a.chunks[i])
		}
		full := text.String()
		delete(s.assemblies, key)
		if fmt.Sprintf("%x", sha256.Sum256([]byte(full))) != p.Hash {
			return result, fmt.Errorf("prompt hash mismatch")
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return result, err
		}
		res, err := tx.ExecContext(ctx, `insert or ignore into prompt_observations(model,hash,ts) values(?,?,?)`, p.Model, p.Hash, p.Timestamp)
		if err != nil {
			tx.Rollback()
			return result, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			tx.Rollback()
			return result, err
		}
		if n == 0 {
			tx.Rollback()
			result.Duplicates++
			continue
		}
		var first, last string
		err = tx.QueryRowContext(ctx, `select first_seen,last_seen from prompt_versions where model=? and hash=?`, p.Model, p.Hash).Scan(&first, &last)
		if err != nil && err != sql.ErrNoRows {
			tx.Rollback()
			return result, err
		}
		if first == "" {
			first = p.Timestamp
			last = p.Timestamp
		} else {
			f, _ := time.Parse(time.RFC3339Nano, first)
			l, _ := time.Parse(time.RFC3339Nano, last)
			if parsed.Before(f) {
				first = p.Timestamp
			}
			if parsed.After(l) {
				last = p.Timestamp
			}
		}
		_, err = tx.ExecContext(ctx, `insert into prompt_versions values(?,?,?,?,?,?,1,?) on conflict(model,hash) do update set first_seen=excluded.first_seen,last_seen=excluded.last_seen,observations=prompt_versions.observations+1`, p.Model, p.Hash, full, p.SourceLabel, first, last, utf8.RuneCountInString(full))
		if err != nil {
			tx.Rollback()
			return result, err
		}
		if err = tx.Commit(); err != nil {
			return result, err
		}
		result.Inserted++
	}
	return result, nil
}

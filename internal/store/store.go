package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cornelia/oai-response-meter/internal/event"
	_ "modernc.org/sqlite"
)

type Store struct {
	db    *sql.DB
	jsonl *os.File
}

type WriteResult struct {
	Inserted   int
	Duplicates int
}

func Open(ctx context.Context, dbPath, jsonlPath string) (*Store, error) {
	if err := ensureParent(dbPath); err != nil {
		return nil, err
	}
	if err := ensureParent(jsonlPath); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	store := &Store{db: db}
	if err := store.init(ctx); err != nil {
		db.Close()
		return nil, err
	}

	jsonl, err := os.OpenFile(jsonlPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open jsonl: %w", err)
	}
	store.jsonl = jsonl
	return store, nil
}

func (s *Store) Close() error {
	var err error
	if s.jsonl != nil {
		err = errors.Join(err, s.jsonl.Close())
	}
	if s.db != nil {
		err = errors.Join(err, s.db.Close())
	}
	return err
}

func (s *Store) WriteBatch(ctx context.Context, events []event.Usage) (WriteResult, error) {
	if len(events) == 0 {
		return WriteResult{}, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WriteResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
insert or ignore into usage_events (
  ts, source, transport, host, path, response_id, model,
  input_tokens, output_tokens, total_tokens, cached_tokens, reasoning_tokens
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		return WriteResult{}, fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	result := WriteResult{}
	for _, usage := range events {
		res, err := stmt.ExecContext(ctx,
			usage.Timestamp,
			usage.Source,
			usage.Transport,
			usage.Host,
			usage.Path,
			usage.ResponseID,
			usage.Model,
			usage.InputTokens,
			usage.OutputTokens,
			usage.TotalTokens,
			usage.CachedTokens,
			usage.ReasoningTokens,
		)
		if err != nil {
			return WriteResult{}, fmt.Errorf("insert usage %q: %w", usage.ResponseID, err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return WriteResult{}, fmt.Errorf("rows affected: %w", err)
		}
		if affected == 0 {
			result.Duplicates++
			continue
		}
		result.Inserted++
		line, err := usage.MarshalJSONLine()
		if err != nil {
			return WriteResult{}, fmt.Errorf("marshal jsonl: %w", err)
		}
		if _, err := s.jsonl.Write(line); err != nil {
			return WriteResult{}, fmt.Errorf("write jsonl: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return WriteResult{}, fmt.Errorf("commit tx: %w", err)
	}
	if err := s.jsonl.Sync(); err != nil {
		return WriteResult{}, fmt.Errorf("sync jsonl: %w", err)
	}
	return result, nil
}

func (s *Store) init(ctx context.Context) error {
	statements := []string{
		`pragma journal_mode = WAL`,
		`pragma synchronous = NORMAL`,
		`pragma busy_timeout = 1000`,
		`create table if not exists usage_events (
  id integer primary key autoincrement,
  ts text not null,
  source text not null,
  transport text not null,
  host text not null,
  path text not null,
  response_id text not null unique,
  model text,
  input_tokens integer not null default 0,
  output_tokens integer not null default 0,
  total_tokens integer not null default 0,
  cached_tokens integer not null default 0,
  reasoning_tokens integer not null default 0,
  created_at text not null default current_timestamp
)`,
		`create index if not exists idx_usage_events_ts on usage_events(ts)`,
		`create index if not exists idx_usage_events_model_ts on usage_events(model, ts)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("init sqlite: %w", err)
		}
	}
	return nil
}

func ensureParent(path string) error {
	if path == "" {
		return errors.New("path is empty")
	}
	parent := filepath.Dir(path)
	if parent == "." || parent == "" {
		return nil
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create parent %q: %w", parent, err)
	}
	return nil
}

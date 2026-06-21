package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
  ts, source, transport, host, path, response_id, previous_response_id, chain_root_response_id, model,
  input_tokens, output_tokens, total_tokens, cached_tokens, reasoning_tokens
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		return WriteResult{}, fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	result := WriteResult{}
	for _, usage := range events {
		usage.ChainRootResponseID = s.chainRoot(ctx, tx, usage)
		res, err := stmt.ExecContext(ctx,
			usage.Timestamp,
			usage.Source,
			usage.Transport,
			usage.Host,
			usage.Path,
			usage.ResponseID,
			usage.PreviousResponseID,
			usage.ChainRootResponseID,
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
		if err := s.repairDescendants(ctx, tx, usage.ResponseID, usage.ChainRootResponseID); err != nil {
			return WriteResult{}, err
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

func (s *Store) WriteRateLimitBatch(ctx context.Context, events []event.RateLimits) (WriteResult, error) {
	if len(events) == 0 {
		return WriteResult{}, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WriteResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
insert into codex_rate_limit_events (
  ts, source, transport, host, path, plan_type, allowed, limit_reached,
  primary_used_percent, primary_window_minutes, primary_reset_after_seconds, primary_reset_at,
  secondary_used_percent, secondary_window_minutes, secondary_reset_after_seconds, secondary_reset_at,
  raw_json
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		return WriteResult{}, fmt.Errorf("prepare rate limit insert: %w", err)
	}
	defer stmt.Close()

	result := WriteResult{}
	for _, item := range events {
		if _, err := stmt.ExecContext(ctx,
			item.Timestamp,
			item.Source,
			item.Transport,
			item.Host,
			item.Path,
			item.PlanType,
			item.Allowed,
			item.LimitReached,
			item.PrimaryUsedPercent,
			item.PrimaryWindowMinutes,
			item.PrimaryResetAfterSeconds,
			item.PrimaryResetAt,
			item.SecondaryUsedPercent,
			item.SecondaryWindowMinutes,
			item.SecondaryResetAfterSeconds,
			item.SecondaryResetAt,
			item.RawJSON,
		); err != nil {
			return WriteResult{}, fmt.Errorf("insert rate limits event: %w", err)
		}
		result.Inserted++
		line, err := item.MarshalJSONLine()
		if err != nil {
			return WriteResult{}, fmt.Errorf("marshal rate limits jsonl: %w", err)
		}
		if _, err := s.jsonl.Write(line); err != nil {
			return WriteResult{}, fmt.Errorf("write rate limits jsonl: %w", err)
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

func (s *Store) chainRoot(ctx context.Context, tx *sql.Tx, usage event.Usage) string {
	if usage.PreviousResponseID == "" {
		return usage.ResponseID
	}
	var parentRoot string
	err := tx.QueryRowContext(ctx,
		`select chain_root_response_id from usage_events where response_id = ?`,
		usage.PreviousResponseID,
	).Scan(&parentRoot)
	if err == nil && parentRoot != "" {
		return parentRoot
	}
	return usage.PreviousResponseID
}

func (s *Store) repairDescendants(ctx context.Context, tx *sql.Tx, responseID, rootID string) error {
	if responseID == "" || rootID == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
with recursive descendants(response_id) as (
  select response_id
  from usage_events
  where previous_response_id = ?

  union all

  select child.response_id
  from usage_events child
  join descendants d on child.previous_response_id = d.response_id
)
update usage_events
set chain_root_response_id = ?
where response_id in (select response_id from descendants)
`, responseID, rootID)
	if err != nil {
		return fmt.Errorf("repair descendants for %q: %w", responseID, err)
	}
	return nil
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
  previous_response_id text not null default '',
  chain_root_response_id text not null default '',
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
		`alter table usage_events add column previous_response_id text not null default ''`,
		`alter table usage_events add column chain_root_response_id text not null default ''`,
		`update usage_events set chain_root_response_id = response_id where chain_root_response_id = ''`,
		`create index if not exists idx_usage_events_previous_response_id on usage_events(previous_response_id)`,
		`create index if not exists idx_usage_events_chain_root_response_id on usage_events(chain_root_response_id)`,
		`create table if not exists codex_rate_limit_events (
  id integer primary key autoincrement,
  ts text not null,
  source text not null,
  transport text not null,
  host text not null,
  path text not null,
  plan_type text not null default '',
  allowed integer not null,
  limit_reached integer not null,
  primary_used_percent integer not null default 0,
  primary_window_minutes integer not null default 0,
  primary_reset_after_seconds integer not null default 0,
  primary_reset_at integer not null default 0,
  secondary_used_percent integer not null default 0,
  secondary_window_minutes integer not null default 0,
  secondary_reset_after_seconds integer not null default 0,
  secondary_reset_at integer not null default 0,
  raw_json text not null,
  created_at text not null default current_timestamp
)`,
		`create index if not exists idx_codex_rate_limit_events_ts on codex_rate_limit_events(ts)`,
		`create index if not exists idx_codex_rate_limit_events_primary_reset_at on codex_rate_limit_events(primary_reset_at)`,
		`create index if not exists idx_codex_rate_limit_events_secondary_reset_at on codex_rate_limit_events(secondary_reset_at)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			if isDuplicateColumn(err) {
				continue
			}
			return fmt.Errorf("init sqlite: %w", err)
		}
	}
	return nil
}

func isDuplicateColumn(err error) bool {
	return strings.Contains(err.Error(), "duplicate column name")
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

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/cornelia/oai-response-meter/internal/event"
)

const speedTableSQL = `create table if not exists response_speed_events (
 response_id text primary key,
 ts text not null, source text not null, transport text not null, host text not null, path text not null,
 model text not null, request_at text not null, created_at text not null, updated_at text not null,
 completed integer not null, output_tokens integer not null, output_tokens_known integer not null,
 output_characters integer not null, output_items integer not null
)`

const ActiveSpeedTTL = time.Hour

func (s *Store) PruneActiveSpeeds(ctx context.Context, now time.Time) error {
	s.promptMu.Lock()
	s.prunePromptAssemblies(now)
	s.promptMu.Unlock()
	_, err := s.db.ExecContext(ctx, `delete from response_speed_events where completed = 0 and julianday(created_at) <= julianday(?)`, now.Add(-ActiveSpeedTTL).Format(time.RFC3339Nano))
	return err
}

// InitSpeedSchema also permits dashboards to open databases predating speed events.
func InitSpeedSchema(ctx context.Context, db *sql.DB) error {
	for _, statement := range []string{speedTableSQL,
		`create index if not exists idx_response_speed_created on response_speed_events(julianday(created_at))`,
		`create index if not exists idx_response_speed_active_updated on response_speed_events(julianday(updated_at)) where completed = 0`} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	rows, err := db.QueryContext(ctx, `pragma table_info(response_speed_events)`)
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, column := range []string{"first_visible_at", "tools_json"} {
		if !columns[column] {
			if _, err := db.ExecContext(ctx, `alter table response_speed_events add column `+column+` text not null default ''`); err != nil && !isDuplicateColumn(err) {
				return err
			}
		}
	}
	return InitPromptSchema(ctx, db)
}

func (s *Store) WriteSpeedBatch(ctx context.Context, events []event.Speed) (WriteResult, error) {
	if len(events) == 0 {
		return WriteResult{}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WriteResult{}, err
	}
	defer tx.Rollback()
	result := WriteResult{}
	for _, item := range events {
		if err := item.Validate(); err != nil {
			return WriteResult{}, err
		}
		created, _ := time.Parse(time.RFC3339Nano, item.CreatedAt)
		updatedAt, _ := time.Parse(time.RFC3339Nano, item.UpdatedAt)
		if !item.Completed && updatedAt.Sub(created) >= ActiveSpeedTTL {
			result.Duplicates++
			continue
		}
		var updated string
		var completed bool
		err := tx.QueryRowContext(ctx, `select updated_at, completed from response_speed_events where response_id=?`, item.ResponseID).Scan(&updated, &completed)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return WriteResult{}, err
		}
		if err == nil {
			old, _ := time.Parse(time.RFC3339Nano, updated)
			next, _ := time.Parse(time.RFC3339Nano, item.UpdatedAt)
			if completed || next.Before(old) || (next.Equal(old) && completed == item.Completed) {
				result.Duplicates++
				continue
			}
		}
		tools, err := json.Marshal(item.Tools)
		if err != nil {
			return WriteResult{}, err
		}
		_, err = tx.ExecContext(ctx, `insert into response_speed_events (response_id,ts,source,transport,host,path,model,request_at,created_at,updated_at,completed,output_tokens,output_tokens_known,output_characters,output_items,first_visible_at,tools_json) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
  on conflict(response_id) do update set ts=excluded.ts,source=excluded.source,transport=excluded.transport,host=excluded.host,path=excluded.path,
  model=excluded.model,request_at=excluded.request_at,created_at=excluded.created_at,updated_at=excluded.updated_at,completed=excluded.completed,
  output_tokens=excluded.output_tokens,output_tokens_known=excluded.output_tokens_known,output_characters=excluded.output_characters,output_items=excluded.output_items,first_visible_at=excluded.first_visible_at,tools_json=excluded.tools_json`,
			item.ResponseID, item.Timestamp, item.Source, item.Transport, item.Host, item.Path, item.Model, item.RequestAt, item.CreatedAt, item.UpdatedAt, item.Completed, item.OutputTokens, item.OutputTokensKnown, item.OutputCharacters, item.OutputItems, item.FirstVisibleAt, string(tools))
		if err != nil {
			return WriteResult{}, err
		}
		line, err := json.Marshal(item)
		if err != nil {
			return WriteResult{}, err
		}
		if _, err = s.jsonl.Write(append(line, '\n')); err != nil {
			return WriteResult{}, err
		}
		result.Inserted++
	}
	if err := tx.Commit(); err != nil {
		return WriteResult{}, err
	}
	return result, s.jsonl.Sync()
}
